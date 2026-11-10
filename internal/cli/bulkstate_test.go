package cli

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type stateDocForTest struct {
	Items []struct {
		Target string `json:"target"`
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"items"`
}

func readOnlyStateDoc(t *testing.T) stateDocForTest {
	t.Helper()
	files := xdgStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("want exactly one before-state file, got %v", files)
	}
	b, _ := os.ReadFile(files[0])
	var d stateDocForTest
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("state file is not valid JSON: %v\n%s", err, b)
	}
	return d
}

func hostGetter(call rpcCall) fakeReply {
	if call.Method == "host.get" {
		return fakeReply{Result: `[{"hostid":"10","host":"web1","name":"Web 1","status":"0","hostgroups":[],"parentTemplates":[],"interfaces":[],"tags":[],"macros":[]}]`}
	}
	return fakeReply{}
}

func TestStateFilesNeverCollideInTheSameInstant(t *testing.T) {
	steps := []*step{{Key: "10", Desc: "x"}}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		p, err := writeState("delete-host", steps)
		if err != nil {
			t.Fatal(err)
		}
		if seen[p] {
			t.Fatalf("state file %s was handed out twice", p)
		}
		seen[p] = true
	}
	if len(xdgStateFiles(t)) != 200 {
		t.Fatalf("every record must survive: %d files", len(xdgStateFiles(t)))
	}
}

func TestATimedOutWriteKeepsTheBeforeStateAndSaysTheOutcomeIsUnknown(t *testing.T) {
	for name, reply := range map[string]fakeReply{
		"504 from a proxy":        {Status: 504},
		"502 from a proxy":        {Status: 502},
		"503 from a proxy":        {Status: 503},
		"a body that is not JSON": {Result: `not json`},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeZabbix{}
			f.Handler = func(call rpcCall) fakeReply {
				if call.Method == "host.delete" {
					return reply
				}
				return hostGetter(call)
			}
			_, err := runFake(t, f, "delete", "host", "web1", "--apply", "--retries", "0")
			var ce *cliErr
			if !errors.As(err, &ce) || ce.code != 7 {
				t.Fatalf("want exit 7 (unknown outcome), got %v", err)
			}
			d := readOnlyStateDoc(t)
			if len(d.Items) != 1 || d.Items[0].Status != "unknown" {
				t.Fatalf("the target must be marked unknown, not removed: %+v", d)
			}
		})
	}
}

func TestARefusedFirstWriteRemovesTheBeforeState(t *testing.T) {
	f := &fakeZabbix{}
	f.Handler = func(call rpcCall) fakeReply {
		if call.Method == "host.delete" {
			return fakeReply{RPCError: "No permissions to referred object"}
		}
		return hostGetter(call)
	}
	_, err := runFake(t, f, "delete", "host", "web1", "--apply")
	if err == nil {
		t.Fatal("want an error")
	}
	if got := xdgStateFiles(t); len(got) != 0 {
		t.Fatalf("Zabbix refused it, so nothing changed and there is nothing to keep: %v", got)
	}
}

func TestAnUnclearFailureThatVerifiesIsReportedAsApplied(t *testing.T) {
	deleted := false
	f := &fakeZabbix{}
	f.Handler = func(call rpcCall) fakeReply {
		switch call.Method {
		case "host.delete":
			deleted = true // PHP finished after the proxy gave up
			return fakeReply{Status: 504}
		case "host.get":
			if deleted {
				return fakeReply{Result: "[]"}
			}
		}
		return hostGetter(call)
	}
	_, err := runFake(t, f, "delete", "host", "web1", "--apply", "--retries", "0")
	if err != nil {
		t.Fatalf("a re-read shows the host is gone, so this succeeded: %v", err)
	}
	d := readOnlyStateDoc(t)
	if d.Items[0].Status != "applied" {
		t.Fatalf("status: %+v", d)
	}
}

func TestTheStateFileRecordsWhatHappenedToEachTarget(t *testing.T) {
	f := &fakeZabbix{}
	f.Handler = func(call rpcCall) fakeReply {
		if call.Method == "host.get" {
			return fakeReply{Result: `[{"hostid":"10","host":"web1","name":"Web 1","status":"0","hostgroups":[],"parentTemplates":[],"interfaces":[],"tags":[],"macros":[]}]`}
		}
		return fakeReply{}
	}
	calls := 0
	inner := f.Handler
	f.Handler = func(call rpcCall) fakeReply {
		if call.Method == "host.update" {
			calls++
			if calls == 2 {
				return fakeReply{RPCError: "denied"}
			}
			return fakeReply{Result: `{"hostids":["10"]}`}
		}
		if call.Method == "host.get" {
			// distinct ids per requested host so the two targets do not dedupe
			if ids, ok := call.Params["hostids"].([]any); ok && len(ids) == 1 && ids[0] == "11" {
				return fakeReply{Result: `[{"hostid":"11","host":"web2","name":"Web 2","status":"0","hostgroups":[],"parentTemplates":[],"interfaces":[],"tags":[],"macros":[]}]`}
			}
		}
		return inner(call)
	}
	_, err := runFake(t, f, "disable", "host", "10", "11", "--apply", "--no-verify")
	if err == nil {
		t.Fatal("the second target fails")
	}
	d := readOnlyStateDoc(t)
	if len(d.Items) != 2 || d.Items[0].Status != "applied" || d.Items[1].Status != "failed" || !strings.Contains(d.Items[1].Error, "denied") {
		t.Fatalf("per-target outcomes: %+v", d)
	}
}
