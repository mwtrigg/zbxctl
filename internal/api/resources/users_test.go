package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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

func problemFixture() map[string]string {
	return map[string]string{
		"problem.get": `[
			{"eventid":"4","objectid":"14","name":"d","severity":"3","clock":"4","suppressed":"1","suppression_data":[{"maintenanceid":"7","suppress_until":"0"}]},
			{"eventid":"3","objectid":"13","name":"c","severity":"3","clock":"3","suppressed":"0"},
			{"eventid":"2","objectid":"12","name":"b","severity":"3","clock":"2","suppressed":"0"},
			{"eventid":"1","objectid":"11","name":"a","severity":"3","clock":"1","suppressed":"0"}]`,
		"trigger.get": `[
			{"triggerid":"11","status":"1","hosts":[{"host":"h1","status":"0"}],"items":[{"status":"0"}]},
			{"triggerid":"12","status":"0","hosts":[{"host":"h2","status":"1"}],"items":[{"status":"0"}]},
			{"triggerid":"13","status":"0","hosts":[{"host":"h3","status":"0"}],"items":[{"status":"1"},{"status":"1"}]},
			{"triggerid":"14","status":"0","hosts":[{"host":"h4","status":"0"}],"items":[{"status":"0"}]}]`,
	}
}

func TestProblemOrphansAndSuppression(t *testing.T) {
	c, _ := newFake(t, problemFixture())
	ps, err := GetProblems(c, ProblemFilter{MinSeverity: -1})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"1": "trigger disabled", "2": "host disabled", "3": "items disabled", "4": ""}
	for _, p := range ps {
		if p.Orphan != want[p.EventID] {
			t.Errorf("event %s orphan=%q want %q", p.EventID, p.Orphan, want[p.EventID])
		}
	}
	if !ps[0].IsSuppressed() || !ps[0].MaintenanceSuppressed() || ps[0].Host != "h4" {
		t.Errorf("suppression/host not filled: %+v", ps[0])
	}
}

func TestProblemClientFiltersApplyLimitAfterFiltering(t *testing.T) {
	c, calls := newFake(t, problemFixture())
	ps, err := GetProblems(c, ProblemFilter{MinSeverity: -1, NoOrphans: true, Limit: 1})
	if err != nil || len(ps) != 1 || ps[0].EventID != "4" {
		t.Fatalf("ps=%+v err=%v", ps, err)
	}
	if _, sent := (*calls)[0]["params"].(map[string]any)["limit"]; sent {
		t.Fatal("limit must not be sent to problem.get when filtering client-side")
	}
	ms, _ := GetProblems(c, ProblemFilter{MinSeverity: -1, Maintenance: true})
	if len(ms) != 1 || ms[0].EventID != "4" {
		t.Fatalf("maintenance filter: %+v", ms)
	}
}

func TestDiagnoseTriggerReasons(t *testing.T) {
	c, _ := newFake(t, map[string]string{
		"trigger.get": `[{"triggerid":"5","description":"d","priority":"3","status":"1","value":"1","state":"0","manual_close":"0",
			"hosts":[{"host":"h","status":"0","maintenance_status":"0"}],"items":[{"itemid":"1","key_":"k","status":"0","state":"1","error":"no such file"}]}]`,
		"problem.get": `[{"eventid":"9","clock":"100","acknowledged":"0","suppressed":"1","suppression_data":[{"maintenanceid":"0","suppress_until":"200"}]}]`,
	})
	d, err := DiagnoseTrigger(c, "5")
	if err != nil {
		t.Fatal(err)
	}
	if d.Recoverable {
		t.Error("a disabled trigger can never recover")
	}
	joined := strings.Join(d.Reasons, " | ")
	for _, want := range []string{"trigger is disabled", "not supported", "no such file", "event 9 is suppressed until 200", "manual close is off"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
}

func TestSetMaintenanceEndMovesActiveTillAndPeriodTogether(t *testing.T) {
	c, calls := newFake(t, map[string]string{"maintenance.update": `{"maintenanceids":["3"]}`})
	m := &Maintenance{MaintenanceID: "3", Name: "w", TimePeriods: []TimePeriod{{Type: "0", StartDate: "1000", Period: "600"}}}
	if err := SetMaintenanceEnd(c, m, 2500); err != nil {
		t.Fatal(err)
	}
	p := (*calls)[0]["params"].(map[string]any)
	tp := p["timeperiods"].([]any)[0].(map[string]any)
	if p["active_till"].(float64) != 2500 || tp["period"].(float64) != 1500 || tp["start_date"].(float64) != 1000 {
		t.Fatalf("bad update: %v", p)
	}
	rec := &Maintenance{Name: "r", TimePeriods: []TimePeriod{{Type: "2"}}}
	if err := SetMaintenanceEnd(c, rec, 9999); err == nil {
		t.Fatal("recurring windows must be refused")
	}
	if err := SetMaintenanceEnd(c, m, 900); err == nil {
		t.Fatal("end before start must be refused")
	}
}

func TestFetchHistoryAllPagesWithoutLosingBoundaryRows(t *testing.T) {
	// 7000 samples, 2 per second for the first 3000 s, so pages end mid-second.
	var all []rawHistoryRecord
	for i := 0; i < 7000; i++ {
		all = append(all, rawHistoryRecord{ItemID: "1", Clock: strconv.Itoa(1000 + i/2), NS: strconv.Itoa(i % 2), Value: strconv.Itoa(i)})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		from := int64(0)
		if v, ok := req.Params["time_from"].(float64); ok {
			from = int64(v)
		}
		limit := int(req.Params["limit"].(float64))
		var page []rawHistoryRecord
		for _, rec := range all {
			c, _ := strconv.ParseInt(rec.Clock, 10, 64)
			if c >= from && len(page) < limit {
				page = append(page, rec)
			}
		}
		b, _ := json.Marshal(page)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":` + string(b) + `,"id":1}`))
	}))
	defer srv.Close()
	c, _ := api.NewClient(context.Background(), &config.Context{URL: srv.URL, APIToken: "x"})
	got, err := fetchHistoryAll(c, []string{"1"}, 3, 0, 0, 0)
	if err != nil || len(got) != 7000 {
		t.Fatalf("got %d rows, err=%v (want 7000: no loss, no duplicates)", len(got), err)
	}
	if _, err := fetchHistoryAll(c, []string{"1"}, 3, 0, 0, 100); err == nil {
		t.Fatal("the row cap must stop runaway queries")
	}
}

func TestGetTrendsOnlyNumericItems(t *testing.T) {
	c, calls := newFake(t, map[string]string{
		"item.get":  `[{"itemid":"1","hostid":"9","key_":"cpu","value_type":"0","hosts":[{"host":"h"}]},{"itemid":"2","hostid":"9","key_":"msg","value_type":"4","hosts":[{"host":"h"}]}]`,
		"trend.get": `[{"itemid":"1","clock":"7200","num":"60","value_min":"1","value_avg":"2","value_max":"3"},{"itemid":"1","clock":"3600","num":"60","value_min":"0","value_avg":"1","value_max":"2"}]`,
	})
	rs, err := GetTrends(c, TrendQuery{HostIDs: []string{"9"}, KeySearch: "c"})
	if err != nil || len(rs) != 2 || rs[0].ClockUnix != 3600 || rs[0].Host != "h" {
		t.Fatalf("rs=%+v err=%v", rs, err)
	}
	ids := (*calls)[1]["params"].(map[string]any)["itemids"].([]any)
	if len(ids) != 1 || ids[0] != "1" {
		t.Fatalf("text item must not be queried for trends: %v", ids)
	}
}

func TestGetLostAndRuleCounts(t *testing.T) {
	c, _ := newFake(t, map[string]string{
		"item.get":             `[{"itemid":"100","name":"a","key_":"k1","hostid":"10","hosts":[{"hostid":"10","host":"h"}],"itemDiscovery":{"status":"1","lastcheck":"1","ts_delete":"9","parent_itemid":"900"}},{"itemid":"101","name":"b","key_":"k2","hostid":"10","hosts":[{"hostid":"10","host":"h"}],"itemDiscovery":{"status":"0","parent_itemid":"900"}}]`,
		"itemprototype.get":    `[{"itemid":"900","discoveryRule":{"itemid":"50"}}]`,
		"trigger.get":          `[]`,
		"triggerprototype.get": `[]`,
		"discoveryrule.get":    `[{"itemid":"50","hostid":"10","name":"FS","key_":"vfs.fs.discovery","status":"0","state":"0","error":"","delay":"1h","items":"2","triggers":"1","hosts":[{"host":"h"}]}]`,
	})
	rules, err := GetDiscoveryRules(c, nil)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules=%+v err=%v", rules, err)
	}
	r := rules[0]
	if r.LostItems != 1 || r.ItemPrototypes != 2 || r.TriggerPrototypes != 1 || !r.Enabled {
		t.Fatalf("bad rule: %+v", r)
	}
	lost, _ := GetLost(c, "item", nil, nil)
	if len(lost) != 1 || lost[0].ID != "100" || lost[0].RuleID != "50" || lost[0].DeletesAt != "9" {
		t.Fatalf("lost: %+v", lost)
	}
}

func TestSetMacroCreatesThenUpdatesWithRightMethods(t *testing.T) {
	c, calls := newFake(t, map[string]string{"usermacro.get": `[]`, "usermacro.create": `{"hostmacroids":["1"]}`})
	act, err := SetMacro(c, MacroInput{HostID: "10", Macro: "{$A}", Value: "v", Type: "0"})
	if err != nil || act != "created" || (*calls)[1]["method"] != "usermacro.create" {
		t.Fatalf("act=%q err=%v calls=%v", act, err, *calls)
	}
	if p := (*calls)[1]["params"].(map[string]any); p["hostid"] != "10" || p["macro"] != "{$A}" {
		t.Fatalf("create params: %v", p)
	}
	c2, calls2 := newFake(t, map[string]string{"usermacro.get": `[{"hostmacroid":"5","hostid":"10","macro":"{$A}","value":"old","type":"0"}]`, "usermacro.update": `{"hostmacroids":["5"]}`})
	act, err = SetMacro(c2, MacroInput{HostID: "10", Macro: "{$A}", Value: "new", Type: "0"})
	p := (*calls2)[1]["params"].(map[string]any)
	if err != nil || act != "updated" || p["hostmacroid"] != "5" || p["macro"] != nil {
		t.Fatalf("act=%q err=%v params=%v", act, err, p)
	}
	c3, calls3 := newFake(t, map[string]string{"usermacro.get": `[]`, "usermacro.createglobal": `{"globalmacroids":["1"]}`})
	if _, err := SetMacro(c3, MacroInput{Global: true, Macro: "{$G}", Value: "v", Type: "0"}); err != nil || (*calls3)[1]["method"] != "usermacro.createglobal" {
		t.Fatalf("global create: %v %v", err, *calls3)
	}
}

func TestUpdateHostProxyParams(t *testing.T) {
	c, calls := newFake(t, map[string]string{"host.get": `[{"hostid":"10","host":"h","name":"h","status":"0","hostgroups":[{"groupid":"1"}],"parentTemplates":[]}]`, "host.update": `{"hostids":["10"]}`})
	for _, tc := range []struct {
		in   UpdateHostInput
		want map[string]float64
	}{
		{UpdateHostInput{HostID: "10", MonitoredBy: "server"}, map[string]float64{"monitored_by": 0}},
		{UpdateHostInput{HostID: "10", MonitoredBy: "proxy", ProxyID: "3"}, map[string]float64{"monitored_by": 1}},
		{UpdateHostInput{HostID: "10", MonitoredBy: "group", ProxyGroupID: "4"}, map[string]float64{"monitored_by": 2}},
	} {
		*calls = nil
		if _, err := UpdateHost(c, tc.in); err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		for _, call := range *calls {
			if call["method"] == "host.update" {
				p = call["params"].(map[string]any)
			}
		}
		if p["monitored_by"].(float64) != tc.want["monitored_by"] {
			t.Errorf("%+v -> %v", tc.in, p)
		}
		if tc.in.MonitoredBy == "proxy" && p["proxyid"] != "3" || tc.in.MonitoredBy == "group" && p["proxy_groupid"] != "4" {
			t.Errorf("ids missing: %v", p)
		}
	}
}

func TestAuditLogParams(t *testing.T) {
	c, calls := newFake(t, map[string]string{"auditlog.get": `[{"auditid":"a","clock":"100","username":"u","ip":"1.2.3.4","action":"2","resourcetype":"4","resourceid":"9","resourcename":"web1","details":""}]`})
	es, err := GetAuditLog(c, AuditQuery{Users: []string{"u"}, ActionCodes: []string{"2"}, ResourceCodes: []string{"4"}, From: 50, Search: "web", Limit: 10})
	if err != nil || len(es) != 1 || es[0].ResourceName != "web1" {
		t.Fatalf("es=%+v err=%v", es, err)
	}
	p := (*calls)[0]["params"].(map[string]any)
	f := p["filter"].(map[string]any)
	if f["username"] == nil || f["action"] == nil || f["resourcetype"] == nil || p["time_from"].(float64) != 50 || p["limit"].(float64) != 10 {
		t.Fatalf("bad params: %v", p)
	}
	if p["sortfield"] != "clock" || p["sortorder"] != "DESC" {
		t.Fatalf("must be newest first: %v", p)
	}
}

func hourlyTrend(item string, t time.Time, num, min, avg, max string) TrendRecord {
	return TrendRecord{ItemID: item, Host: "h", Key: "k", ClockUnix: t.Unix(), Clock: strconv.FormatInt(t.Unix(), 10), Num: num, Min: min, Avg: avg, Max: max}
}

func TestRollupTrendsDayWeightedAndDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tz database not available")
	}
	// 2026-03-08 is the spring-forward day in New York: it has 23 hours.
	day := time.Date(2026, 3, 8, 0, 0, 0, 0, ny)
	var recs []TrendRecord
	for h := 0; h < 23; h++ {
		recs = append(recs, hourlyTrend("1", day.Add(time.Duration(h)*time.Hour), "60", "10", "20", "30"))
	}
	recs = append(recs, hourlyTrend("1", day.Add(5*time.Hour), "60", "2", "20", "99")) // an extra bucket with the day's extremes
	next := time.Date(2026, 3, 9, 0, 0, 0, 0, ny)
	recs = append(recs, hourlyTrend("1", next, "30", "1", "50", "60"))
	got, err := RollupTrends(recs, "day", ny)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v err=%v", got, err)
	}
	d := got[0]
	if d.ClockUnix != day.Unix() || d.Min != "2" || d.Max != "99" || d.Num != "1440" {
		t.Fatalf("23-hour day (24 buckets here): %+v", d)
	}
	if got[1].ClockUnix != next.Unix() || got[1].Avg != "50" || got[1].Min != "1" {
		t.Fatalf("next day: %+v", got[1])
	}
	// weighted average: 60 samples at 10 and 30 samples at 40 -> (600+1200)/90 = 20
	w, _ := RollupTrends([]TrendRecord{hourlyTrend("1", day, "60", "0", "10", "20"), hourlyTrend("1", day.Add(time.Hour), "30", "0", "40", "80")}, "day", ny)
	if w[0].Avg != "20" {
		t.Fatalf("avg must be weighted by sample count: %+v", w[0])
	}
	// weeks start on Monday
	mon := time.Date(2026, 3, 9, 0, 0, 0, 0, ny)
	wk, _ := RollupTrends([]TrendRecord{hourlyTrend("1", day, "60", "1", "1", "1"), hourlyTrend("1", mon.Add(3*24*time.Hour), "60", "1", "1", "1")}, "week", ny)
	if len(wk) != 2 || wk[1].ClockUnix != mon.Unix() {
		t.Fatalf("week buckets: %+v", wk)
	}
	if _, err := RollupTrends(recs, "year", ny); err == nil {
		t.Fatal("unknown period must be refused")
	}
}

func TestMappingAndExpressionParsing(t *testing.T) {
	m, err := ParseMapping("equals", "0=Down")
	if err != nil || m.Type != "0" || m.Value != "0" || m.NewValue != "Down" || m.Label() != "0=Down" {
		t.Fatalf("%+v %v", m, err)
	}
	if m, _ := ParseMapping("ge", "5=High"); m.Type != "1" || m.Label() != ">=5=High" {
		t.Fatalf("ge: %+v", m)
	}
	if m, _ := ParseMapping("range", "1-5=Low"); m.Type != "3" || m.Value != "1-5" {
		t.Fatalf("range: %+v", m)
	}
	if m, _ := ParseMapping("regexp", "^ab=x=y"); m.Value != "^ab=x" || m.NewValue != "y" { // the last = splits
		t.Fatalf("regexp keeps earlier '=': %+v", m)
	}
	if m, _ := ParseMapping("default", "Unknown"); m.Type != "5" || m.NewValue != "Unknown" {
		t.Fatalf("default: %+v", m)
	}
	for _, bad := range []string{"", "novalue", "=Text", "key="} {
		if _, err := ParseMapping("equals", bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	e, err := ParseExpression("matches:^(ext4|xfs)$", true)
	if err != nil || e.Type != "3" || e.Expression != "^(ext4|xfs)$" || e.CaseSensitive != "1" {
		t.Fatalf("%+v %v", e, err)
	}
	if e, _ := ParseExpression("any:a,b", false); e.Type != "1" || e.Delimiter != "," || e.CaseSensitive != "0" {
		t.Fatalf("any: %+v", e)
	}
	for _, bad := range []string{"", "nocolon", "bogus:x"} {
		if _, err := ParseExpression(bad, false); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestValueMapCreateAndOwnerNames(t *testing.T) {
	c, calls := newFake(t, map[string]string{
		"valuemap.create": `{"valuemapids":["9"]}`,
		"valuemap.get":    `[{"valuemapid":"9","hostid":"7","name":"S","mappings":[{"type":"0","value":"1","newvalue":"Up"}]}]`,
		"template.get":    `[{"templateid":"7","host":"tpl"}]`,
		"host.get":        `[]`,
	})
	id, err := CreateValueMap(c, "7", "S", []Mapping{{Type: "0", Value: "1", NewValue: "Up"}})
	if err != nil || id != "9" {
		t.Fatalf("%q %v", id, err)
	}
	p := (*calls)[0]["params"].(map[string]any)
	if p["hostid"] != "7" || len(p["mappings"].([]any)) != 1 {
		t.Fatalf("create params: %v", p)
	}
	vms, err := GetValueMaps(c, ValueMapFilter{})
	if err != nil || len(vms) != 1 || vms[0].Owner != "tpl" {
		t.Fatalf("owner name must come from the template: %+v %v", vms, err)
	}
}
