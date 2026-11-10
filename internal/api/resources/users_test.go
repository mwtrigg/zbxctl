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
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
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
