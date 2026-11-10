package cli

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// hostWithTemplates answers host.get with a host linked to templates 20 and 21.
func hostWithTemplates(call rpcCall) fakeReply {
	if call.Method == "host.get" {
		return fakeReply{Result: `[{"hostid":"10","host":"web1","name":"Web 1","status":"0","description":"d",
			"hostgroups":[{"groupid":"1","name":"G"}],"parentTemplates":[{"templateid":"20","name":"T20"},{"templateid":"21","name":"T21"}],
			"interfaces":[],"tags":[],"macros":[]}]`}
	}
	if call.Method == "host.update" {
		return fakeReply{Result: `{"hostids":["10"]}`}
	}
	if call.Method == "hostgroup.get" {
		return groupByID(call)
	}
	return templateByID(call)
}

// groupByID answers hostgroup.get for an ID lookup (group N is called GN) and finds nothing by name.
func groupByID(call rpcCall) fakeReply {
	if call.Method != "hostgroup.get" {
		return fakeReply{}
	}
	if ids, ok := call.Params["groupids"].([]any); ok && len(ids) == 1 {
		id := ids[0].(string)
		return fakeReply{Result: `[{"groupid":"` + id + `","name":"G` + id + `"}]`}
	}
	return fakeReply{Result: "[]"}
}

// templateByID answers template.get for an ID lookup and finds nothing by name.
func templateByID(call rpcCall) fakeReply {
	if call.Method != "template.get" {
		return fakeReply{}
	}
	if ids, ok := call.Params["templateids"].([]any); ok && len(ids) == 1 {
		id := ids[0].(string)
		return fakeReply{Result: `[{"templateid":"` + id + `","host":"T` + id + `","name":"T` + id + `"}]`}
	}
	return fakeReply{Result: "[]"}
}

func templateIDs(t *testing.T, call rpcCall, key string) []string {
	t.Helper()
	list, _ := call.Params[key].([]any)
	var ids []string
	for _, x := range list {
		ids = append(ids, x.(map[string]any)["templateid"].(string))
	}
	return ids
}

func TestRemoveTemplateOnlyUnlinks(t *testing.T) {
	f := &fakeZabbix{Handler: hostWithTemplates}
	if _, err := runFake(t, f, "update", "host", "web1", "--remove-template", "20", "--apply", "--no-verify"); err != nil {
		t.Fatal(err)
	}
	up := f.called("host.update")
	if len(up) != 1 {
		t.Fatalf("want one host.update, got %d", len(up))
	}
	if _, has := up[0].Params["templates_clear"]; has {
		t.Fatalf("--remove-template must not send templates_clear, which deletes items and history: %v", up[0].Params)
	}
	if got := templateIDs(t, up[0], "templates"); len(got) != 1 || got[0] != "21" {
		t.Fatalf("templates left on the host: %v", got)
	}
}

func TestClearTemplateIsExplicitAndSaysWhatItDestroys(t *testing.T) {
	f := &fakeZabbix{Handler: hostWithTemplates}
	out, err := runFake(t, f, "update", "host", "web1", "--clear-template", "20")
	if err != nil || len(f.called("host.update")) != 0 {
		t.Fatalf("a dry run must not write: err=%v calls=%v", err, f.Calls)
	}
	for _, want := range []string{"UNLINK AND CLEAR", "deletes the items, triggers, graphs and history", "cannot be undone"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the dry run must say %q:\n%s", want, out)
		}
	}
	if _, err := runFake(t, f, "update", "host", "web1", "--clear-template", "20", "--apply", "--no-verify"); err != nil {
		t.Fatal(err)
	}
	up := f.called("host.update")
	if len(up) != 1 {
		t.Fatalf("want one host.update, got %d", len(up))
	}
	if got := templateIDs(t, up[0], "templates_clear"); len(got) != 1 || got[0] != "20" {
		t.Fatalf("templates_clear: %v", got)
	}
}

func TestUpdateHostDryRunListsEveryChangeAndApplySavesBeforeState(t *testing.T) {
	f := &fakeZabbix{Handler: hostWithTemplates}
	out, _ := runFake(t, f, "update", "host", "web1", "--add-group", "2", "--remove-group", "3", "--add-template", "22", "--remove-template", "21", "--visible-name", "New")
	for _, want := range []string{`add group \"G2\" (id 2)`, `remove group \"G3\" (id 3)`, `link template \"22\"`, `unlink`, `visible name to \"New\"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("the dry run must list %q:\n%s", want, out)
		}
	}
	if len(xdgStateFiles(t)) != 0 {
		t.Fatal("a dry run must not write a before-state file")
	}
	if _, err := runFake(t, f, "update", "host", "web1", "--add-group", "2", "--apply", "--no-verify"); err != nil {
		t.Fatal(err)
	}
	files := xdgStateFiles(t)
	if len(files) != 1 {
		t.Fatalf("want one before-state file, got %v", files)
	}
	b, _ := os.ReadFile(files[0])
	var doc struct {
		Items []struct {
			Before map[string]any `json:"before"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Items) != 1 {
		t.Fatalf("state file: %s", b)
	}
	before := doc.Items[0].Before
	if before["description"] != "d" || before["template_ids"] == nil || before["group_ids"] == nil {
		t.Fatalf("the before-state must hold what a restore needs: %v", before)
	}
}

func TestUpdateHostNeedsAFlag(t *testing.T) {
	f := &fakeZabbix{Handler: hostWithTemplates}
	_, err := runFake(t, f, "update", "host", "web1", "--apply")
	var ce *cliErr
	if !errors.As(err, &ce) || ce.code != 5 {
		t.Fatalf("no update flags is a usage error (exit 5), got %v", err)
	}
}

// A stateful fake: the host keeps the templates it is given, so verification
// can be exercised end to end.
func TestUpdateHostVerifiesTheResult(t *testing.T) {
	linked := []string{"20", "21"}
	f := &fakeZabbix{}
	f.Handler = func(call rpcCall) fakeReply {
		switch call.Method {
		case "host.update":
			linked = nil
			for _, id := range templateIDs(t, call, "templates") {
				linked = append(linked, id)
			}
			return fakeReply{Result: `{"hostids":["10"]}`}
		case "host.get":
			var refs []string
			for _, id := range linked {
				refs = append(refs, `{"templateid":"`+id+`","name":"T`+id+`"}`)
			}
			return fakeReply{Result: `[{"hostid":"10","host":"web1","name":"Web 1","status":"0","hostgroups":[{"groupid":"1","name":"G"}],"parentTemplates":[` + strings.Join(refs, ",") + `],"interfaces":[],"tags":[],"macros":[]}]`}
		}
		return templateByID(call)
	}
	out, err := runFake(t, f, "update", "host", "web1", "--remove-template", "20", "--apply")
	if err != nil || !strings.Contains(out, `"verified": true`) {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

// R3-1 (correctness): a group called 2024 is not the group with ID 2024, and the dry run says
// which group was found.
func TestAllDigitGroupNamesAreNotTakenForIDs(t *testing.T) {
	handler := func(c rpcCall) fakeReply {
		switch c.Method {
		case "hostgroup.get":
			if ids, ok := c.Params["groupids"].([]any); ok && len(ids) == 1 && ids[0] == "2024" {
				return fakeReply{Result: `[{"groupid":"2024","name":"Decommission-NOW"}]`}
			}
			if flt, ok := c.Params["filter"].(map[string]any); ok {
				if l, _ := flt["name"].([]any); len(l) == 1 && l[0] == "2024" {
					return fakeReply{Result: `[{"groupid":"7","name":"2024"}]`}
				}
			}
			return fakeReply{Result: "[]"}
		}
		return hostWithTemplates(c)
	}
	f := &fakeZabbix{Handler: handler}
	_, err := runFake(t, f, "update", "host", "web1", "--add-group", "2024", "--apply")
	if exitOf(err) != 5 || len(f.called("host.update")) != 0 {
		t.Fatalf("an ambiguous group must stop (exit 5), nothing written: %v", err)
	}
	f = &fakeZabbix{Handler: handler}
	out, err := runFake(t, f, "update", "host", "web1", "--add-group", "name:2024")
	if err != nil || !strings.Contains(out, `add group \"2024\" (id 7)`) {
		t.Fatalf("name: picks the group called 2024 and the dry run shows it: %v\n%s", err, out)
	}
	f = &fakeZabbix{Handler: handler}
	out, err = runFake(t, f, "update", "host", "web1", "--add-group", "id:2024")
	if err != nil || !strings.Contains(out, `Decommission-NOW`) {
		t.Fatalf("id: picks the ID and the dry run shows its name: %v\n%s", err, out)
	}
}
