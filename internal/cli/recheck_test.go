package cli

import (
	"errors"
	"strings"
	"testing"
)

func hostJSON(name, desc, monitoredBy, proxyID string) string {
	return `[{"hostid":"10","host":"web1","name":"` + name + `","status":"0","description":"` + desc + `","monitored_by":"` + monitoredBy + `","proxyid":"` + proxyID + `","proxy_groupid":"0",
		"hostgroups":[{"groupid":"1","name":"G"}],"parentTemplates":[],"interfaces":[],"tags":[],"macros":[]}]`
}

func exitOf(err error) int {
	var ce *cliErr
	if errors.As(err, &ce) {
		return ce.code
	}
	if err == nil {
		return 0
	}
	return classify(err)
}

// Sabotage S8 and finding N2: after a 504 the re-read must compare the values that were
// asked for. A 504 that never applied, behind update host --visible-name, is unknown.
func TestUpdateHostAfterA504ChecksTheRequestedValues(t *testing.T) {
	run := func(applied bool, nameBefore string, args ...string) (*fakeZabbix, error) {
		name := nameBefore
		f := &fakeZabbix{}
		f.Handler = func(c rpcCall) fakeReply {
			switch c.Method {
			case "host.update":
				if applied {
					name = "New name"
				}
				return fakeReply{Status: 504}
			case "host.get":
				return fakeReply{Result: hostJSON(name, "d", "0", "0")}
			}
			return fakeReply{}
		}
		_, err := runFake(t, f, append([]string{"update", "host", "web1", "--apply", "--retries", "0"}, args...)...)
		return f, err
	}
	// the 504 never reached Zabbix: nothing changed, so we must not say it did
	_, err := run(false, "Old name", "--visible-name", "New name")
	if exitOf(err) != 7 {
		t.Fatalf("a 504 that applied nothing must be exit 7 (unknown), got %v", err)
	}
	if d := readOnlyStateDoc(t); d.Items[0].Status != "unknown" {
		t.Fatalf("the target must be unknown: %+v", d)
	}
	// the 504 hid a success: the re-read shows the new name, so it is confirmed
	if _, err := run(true, "Old name", "--visible-name", "New name"); err != nil {
		t.Fatalf("a re-read that shows the requested name confirms it: %v", err)
	}
	// the host already had the requested name: a re-read proves nothing about this request
	if _, err := run(false, "New name", "--visible-name", "New name"); exitOf(err) != 7 {
		t.Fatalf("when the value was already there, a passing re-read cannot confirm the write: %v", err)
	}
}

func TestEnableOnAHostAlreadyEnabledStaysUnknownAfterA504(t *testing.T) {
	f := &fakeZabbix{Handler: func(c rpcCall) fakeReply {
		if c.Method == "host.update" {
			return fakeReply{Status: 504}
		}
		return hostGetter(c) // status 0 = already monitored
	}}
	_, err := runFake(t, f, "enable", "host", "web1", "--apply", "--retries", "0")
	if exitOf(err) != 7 {
		t.Fatalf("exit 7 expected, got %v", err)
	}
}

// Sabotage S13: an unclear failure of a single-object write must keep the file.
func TestSingleObjectWriteTimeoutKeepsTheBeforeState(t *testing.T) {
	for _, status := range []int{502, 503, 504} {
		f := &fakeZabbix{Handler: func(c rpcCall) fakeReply {
			switch c.Method {
			case "hostgroup.get":
				return fakeReply{Result: "[]"}
			case "hostgroup.create":
				return fakeReply{Status: status}
			}
			return fakeReply{}
		}}
		_, err := runFake(t, f, "create", "group", "--name", "G", "--apply", "--retries", "0")
		if exitOf(err) != 7 {
			t.Fatalf("%d: want exit 7, got %v", status, err)
		}
		if d := readOnlyStateDoc(t); d.Items[0].Status != "unknown" {
			t.Fatalf("%d: the record must be kept as unknown: %+v", status, d)
		}
	}
}

func TestAPICallTimeoutKeepsTheRecordAndExits7(t *testing.T) {
	f := &fakeZabbix{Handler: func(c rpcCall) fakeReply {
		if c.Method == "host.update" {
			return fakeReply{Status: 504}
		}
		return fakeReply{}
	}}
	_, err := runFake(t, f, "api", "call", "host.update", "--params", `{"hostid":"10"}`, "--apply", "--retries", "0")
	if exitOf(err) != 7 {
		t.Fatalf("api call after a 504 must be exit 7, got %v", err)
	}
	if d := readOnlyStateDoc(t); d.Items[0].Status != "unknown" {
		t.Fatalf("the audit record must say unknown, not stay pending: %+v", d)
	}
	// and a success marks it applied
	f2 := &fakeZabbix{}
	if _, err := runFake(t, f2, "api", "call", "host.update", "--params", `{"hostid":"10"}`, "--apply"); err != nil {
		t.Fatal(err)
	}
	if d := readOnlyStateDoc(t); d.Items[0].Status != "applied" {
		t.Fatalf("a successful api call must record applied: %+v", d)
	}
}

func TestImportTimeoutKeepsTheBeforeState(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/t.yaml"
	_ = writeTestFile(file, "zabbix_export:\n  version: '7.4'\n  templates: []\n")
	f := &fakeZabbix{Handler: func(c rpcCall) fakeReply {
		switch c.Method {
		case "configuration.importcompare":
			return fakeReply{Result: `{"templates":{"updated":{"templates":{"T":{}}}}}`}
		case "configuration.import":
			return fakeReply{Status: 504}
		}
		return fakeReply{}
	}}
	_, err := runFake(t, f, "import", "template", file, "--apply", "--retries", "0", "--no-state=false")
	if exitOf(err) != 7 {
		t.Fatalf("an import lost to a 504 is exit 7, got %v (calls %v)", err, f.Calls)
	}
	if len(xdgStateFiles(t)) != 1 || !strings.Contains(readFileString(xdgStateFiles(t)[0]), "unknown") {
		t.Fatal("the import's before-state must be kept and say unknown")
	}
}

// Finding N5 (correctness): zbxctl's own re-reads by ID must not trip over another
// host that is named like that ID.
func TestDeleteHostIsNotConfusedByAHostNamedLikeItsID(t *testing.T) {
	deleted := false
	f := &fakeZabbix{}
	f.Handler = func(c rpcCall) fakeReply {
		switch c.Method {
		case "host.delete":
			deleted = true
			return fakeReply{Result: `{"hostids":["10084"]}`}
		case "host.get":
			one := func(id, host string) string {
				return `{"hostid":"` + id + `","host":"` + host + `","name":"` + host + `","status":"0","hostgroups":[],"parentTemplates":[],"interfaces":[],"tags":[],"macros":[]}`
			}
			if ids, ok := c.Params["hostids"].([]any); ok && len(ids) == 1 {
				if ids[0] == "10084" && !deleted {
					return fakeReply{Result: "[" + one("10084", "web-01") + "]"}
				}
				return fakeReply{Result: "[]"}
			}
			// by name: web-01 is host 10084; and another host is NAMED 10084
			if flt, ok := c.Params["filter"].(map[string]any); ok {
				for _, v := range flt {
					if l, _ := v.([]any); len(l) == 1 && l[0] == "web-01" && !deleted {
						return fakeReply{Result: "[" + one("10084", "web-01") + "]"}
					}
					if l, _ := v.([]any); len(l) == 1 && l[0] == "10084" {
						return fakeReply{Result: "[" + one("10090", "10084") + "]"}
					}
				}
			}
			return fakeReply{Result: "[]"}
		}
		return fakeReply{}
	}
	if _, err := runFake(t, f, "delete", "host", "web-01", "--apply"); err != nil || !deleted {
		t.Fatalf("delete host must send the delete and verify it by ID: err=%v deleted=%v", err, deleted)
	}
}
