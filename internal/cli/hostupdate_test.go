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
	return fakeReply{}
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
	for _, want := range []string{`add group \"2\"`, `remove group \"3\"`, `link template \"22\"`, `unlink`, `visible name to \"New\"`} {
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
		return fakeReply{}
	}
	out, err := runFake(t, f, "update", "host", "web1", "--remove-template", "20", "--apply")
	if err != nil || !strings.Contains(out, `"verified": true`) {
		t.Fatalf("err=%v out=%s", err, out)
	}
}
