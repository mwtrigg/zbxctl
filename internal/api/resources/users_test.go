package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/config"
)

// newFake returns a client wired to a fake Zabbix that records each request's
// method and params and answers with the canned result for that method.
func newFake(t *testing.T, results map[string]string) (*api.Client, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params any    `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls = append(calls, map[string]any{"method": req.Method, "params": req.Params, "auth": r.Header.Get("Authorization")})
		res, ok := results[req.Method]
		if !ok {
			res = "[]"
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":` + res + `,"id":1}`))
	}))
	t.Cleanup(srv.Close)
	c, err := api.NewClient(context.Background(), &config.Context{URL: srv.URL, APIToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return c, &calls
}

// Zabbix 7.x removed user.type; asking for it makes user.get fail.
func TestGetUsersDoesNotRequestRemovedTypeField(t *testing.T) {
	c, calls := newFake(t, map[string]string{"user.get": `[{"userid":"1","username":"Admin","name":"Z","surname":"A","roleid":"3","role":{"name":"Super admin role"}}]`})
	users, err := GetUsers(c, UserFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if users[0].Role.Name != "Super admin role" {
		t.Fatalf("role = %q", users[0].Role.Name)
	}
	out := (*calls)[0]["params"].(map[string]any)["output"].([]any)
	if slices.Contains(out, any("type")) {
		t.Fatalf("output still requests type: %v", out)
	}
}

// Zabbix 7.x reports availability on interfaces, not hosts.
func TestHostAvailabilityFromInterfaces(t *testing.T) {
	c, calls := newFake(t, map[string]string{"host.get": `[{"hostid":"1","host":"h","name":"h","status":"0","hostgroups":[{"name":"G"}],"parentTemplates":[{"name":"T"}],
		"interfaces":[{"ip":"10.0.0.1","port":"10050","main":"1","available":"1"},{"ip":"10.0.0.1","port":"161","main":"1","available":"2"}],
		"macros":[{"macro":"{$S}","type":"1"}],"tags":[{"tag":"env","value":"lab"}]}]`})
	h, err := GetHost(c, "h")
	if err != nil {
		t.Fatal(err)
	}
	if h.AvailableState() != "2" || h.GroupNames()[0] != "G" || h.TemplateNames()[0] != "T" || h.Tags[0].Tag != "env" {
		t.Fatalf("bad host: %+v", h)
	}
	p := (*calls)[0]["params"].(map[string]any)
	for _, k := range []string{"selectHostGroups", "selectParentTemplates", "selectInterfaces", "selectTags", "selectMacros"} {
		if p[k] == nil {
			t.Errorf("missing %s", k)
		}
	}
	for _, f := range p["output"].([]any) {
		if f == "available" {
			t.Error("host output still requests available")
		}
	}
}

// A limit must count items that have data, not items that never collected.
func TestLatestDataLimitSkipsNeverCollected(t *testing.T) {
	c, calls := newFake(t, map[string]string{"item.get": `[
		{"itemid":"1","name":"a","lastclock":"0","hosts":[{"host":"h"}]},
		{"itemid":"2","name":"b","lastclock":"100","lastvalue":"1","hosts":[{"host":"h"}]},
		{"itemid":"3","name":"c","lastclock":"200","lastvalue":"2","hosts":[{"host":"h"}]},
		{"itemid":"4","name":"d","lastclock":"300","lastvalue":"3","hosts":[{"host":"h"}]}]`})
	items, err := GetLatestData(c, ItemFilter{HostIDs: []string{"1"}, Limit: 2, WithHost: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ItemID != "2" {
		t.Fatalf("got %+v", items)
	}
	if _, sent := (*calls)[0]["params"].(map[string]any)["limit"]; sent {
		t.Fatal("limit must not be sent to item.get")
	}
}

// maintenance.create takes host/group objects; "hostids"/"groupids" no longer exist.
func TestCreateMaintenanceUsesHostAndGroupObjects(t *testing.T) {
	c, calls := newFake(t, map[string]string{"maintenance.create": `{"maintenanceids":["7"]}`})
	id, err := CreateMaintenance(c, CreateMaintenanceParams{Name: "m", HostIDs: []string{"10"}, GroupIDs: []string{"4"}, ActiveSince: 100, ActiveTill: 400})
	if err != nil || id != "7" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	p := (*calls)[0]["params"].(map[string]any)
	if _, bad := p["hostids"]; bad {
		t.Fatal("still sends hostids")
	}
	if _, bad := p["groupids"]; bad {
		t.Fatal("still sends groupids")
	}
	h := p["hosts"].([]any)[0].(map[string]any)
	g := p["groups"].([]any)[0].(map[string]any)
	if h["hostid"] != "10" || g["groupid"] != "4" {
		t.Fatalf("hosts=%v groups=%v", h, g)
	}
}

func TestAcknowledgeActionMasks(t *testing.T) {
	cases := []struct {
		name string
		p    AcknowledgeParams
		want float64
	}{
		{"ack", AcknowledgeParams{EventID: "1", Severity: -1}, 2},
		{"ack close msg", AcknowledgeParams{EventID: "1", Close: true, Message: "m", Severity: -1}, 7},
		{"unack", AcknowledgeParams{EventID: "1", Action: ActionUnacknowledge, Severity: -1}, 16},
		{"suppress", AcknowledgeParams{EventID: "1", Action: ActionSuppress, SuppressUntil: 99, Severity: -1}, 32},
		{"unsuppress msg", AcknowledgeParams{EventID: "1", Action: ActionUnsuppress, Message: "m", Severity: -1}, 68},
		{"ack severity", AcknowledgeParams{EventID: "1", Severity: 4}, 10},
	}
	for _, c := range cases {
		cl, calls := newFake(t, map[string]string{"event.acknowledge": `{"eventids":[1]}`})
		if err := AcknowledgeEvent(cl, c.p); err != nil {
			t.Fatal(err)
		}
		p := (*calls)[0]["params"].(map[string]any)
		if p["action"].(float64) != c.want {
			t.Errorf("%s: action %v want %v", c.name, p["action"], c.want)
		}
		if c.p.Action == ActionSuppress && p["suppress_until"].(float64) != 99 {
			t.Errorf("%s: suppress_until %v", c.name, p["suppress_until"])
		}
	}
}

func TestHostTagMerging(t *testing.T) {
	cur := []HostTag{{"env", "prod"}, {"env", "lab"}, {"team", "x"}}
	if _, changed := HostTagsAfterAdd(cur, "env", "prod"); changed {
		t.Error("duplicate add should be a no-op")
	}
	out, changed := HostTagsAfterAdd(cur, "tier", "1")
	if !changed || len(out) != 4 || len(cur) != 3 {
		t.Errorf("add: %v changed=%v (input mutated? %v)", out, changed, cur)
	}
	if out, n := HostTagsAfterRemove(cur, "env", nil); n != 2 || len(out) != 1 {
		t.Errorf("remove all by name: %v %d", out, n)
	}
	v := "lab"
	if out, n := HostTagsAfterRemove(cur, "env", &v); n != 1 || len(out) != 2 {
		t.Errorf("remove pair: %v %d", out, n)
	}
	c, calls := newFake(t, map[string]string{"host.update": `{"hostids":["1"]}`})
	if err := SetHostTags(c, "1", nil); err != nil {
		t.Fatal(err)
	}
	if tags, ok := (*calls)[0]["params"].(map[string]any)["tags"].([]any); !ok || len(tags) != 0 {
		t.Errorf("empty tag list must encode as []: %v", (*calls)[0]["params"])
	}
}

func TestCreateUserAndGroupParams(t *testing.T) {
	c, calls := newFake(t, map[string]string{"user.create": `{"userids":["5"]}`, "usergroup.create": `{"usrgrpids":["14"]}`, "user.delete": `{"userids":["5"]}`})
	id, err := CreateUser(c, CreateUserInput{Username: "bob", Password: "pw", RoleID: "1", GroupIDs: []string{"14"}})
	if err != nil || id != "5" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	p := (*calls)[0]["params"].(map[string]any)
	if g := p["usrgrps"].([]any)[0].(map[string]any); g["usrgrpid"] != "14" || p["roleid"] != "1" || p["passwd"] != "pw" {
		t.Fatalf("bad user.create params: %v", p)
	}
	if gid, err := CreateUserGroup(c, "g", "1", true); err != nil || gid != "14" {
		t.Fatalf("group: %q %v", gid, err)
	}
	gp := (*calls)[1]["params"].(map[string]any)
	if gp["gui_access"] != "1" || gp["users_status"].(float64) != 1 {
		t.Fatalf("bad usergroup.create params: %v", gp)
	}
	if err := DeleteUser(c, "5"); err != nil {
		t.Fatal(err)
	}
	if ids, ok := (*calls)[2]["params"].([]any); !ok || ids[0] != "5" {
		t.Fatalf("user.delete params should be an ID array: %v", (*calls)[2]["params"])
	}
}

func TestFlattenComparison(t *testing.T) {
	raw := []byte(`{"templates":{"updated":[{"before":{"name":"T","description":"a"},"after":{"name":"T","description":"a"},
		"items":{"updated":[{"before":{"name":"I","delay":"30s"},"after":{"name":"I","delay":"60s"}}],"created":[{"name":"New","key":"k"}]}}],
		"deleted":[{"name":"Gone"}]}}`)
	got, err := FlattenComparison(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"templates/Gone":        "deleted",
		"templates/T/items/I":   "updated",
		"templates/T/items/New": "created",
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for _, c := range got {
		if want[c.Path] != c.Action {
			t.Errorf("%s: %s", c.Path, c.Action)
		}
		if c.Path == "templates/T/items/I" && (len(c.Fields) != 1 || c.Fields[0] != "delay: 30s -> 60s") {
			t.Errorf("fields: %v", c.Fields)
		}
	}
	none, err := FlattenComparison([]byte(`[]`))
	if err != nil || len(none) != 0 {
		t.Fatalf("[] must mean no changes: %v %v", none, err)
	}
}

func TestGetTemplateFallsBackToVisibleName(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		res := `[]`
		if n == 2 {
			res = `[{"templateid":"9","host":"tech","name":"Visible Name"}]`
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":` + res + `,"id":1}`))
	}))
	defer srv.Close()
	c, _ := api.NewClient(context.Background(), &config.Context{URL: srv.URL, APIToken: "x"})
	tpl, err := GetTemplate(c, "Visible Name")
	if err != nil || tpl.TemplateID != "9" || n != 2 {
		t.Fatalf("tpl=%v err=%v calls=%d", tpl, err, n)
	}
}
