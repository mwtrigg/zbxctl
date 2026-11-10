package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func mcpExchange(t *testing.T, allowWrites bool, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, allowWrites); err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad reply %q: %v", l, err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func toolNames(t *testing.T, allowWrites bool) []string {
	t.Helper()
	msgs := mcpExchange(t, allowWrites, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var names []string
	for _, x := range msgs[0]["result"].(map[string]any)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}

func TestMCPInitializeAndNotifications(t *testing.T) {
	msgs := mcpExchange(t, false,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope"}`)
	if len(msgs) != 3 { // the notification gets no reply
		t.Fatalf("replies: %v", msgs)
	}
	r := msgs[0]["result"].(map[string]any)
	if r["protocolVersion"] != "2025-03-26" || r["serverInfo"].(map[string]any)["name"] != "zbxctl" {
		t.Fatalf("initialize: %v", r)
	}
	if msgs[2]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("unknown method: %v", msgs[2])
	}
}

func TestMCPReadOnlyOffersNoWritesAndNothingThatTouchesFiles(t *testing.T) {
	ro := toolNames(t, false)
	for _, must := range []string{"zbxctl_get_hosts", "zbxctl_get_problems", "zbxctl_backtest", "zbxctl_diagnose_trigger", "zbxctl_state_list"} {
		if !slices.Contains(ro, must) {
			t.Errorf("read-only server lacks %s", must)
		}
	}
	for _, never := range []string{"zbxctl_disable_host", "zbxctl_delete_host", "zbxctl_acknowledge", "zbxctl_api_call", "zbxctl_set_macro", "zbxctl_create_user"} {
		if slices.Contains(ro, never) {
			t.Errorf("read-only server must not offer %s", never)
		}
	}
	rw := toolNames(t, true)
	for _, want := range []string{"zbxctl_disable_host", "zbxctl_acknowledge", "zbxctl_api_call"} {
		if !slices.Contains(rw, want) {
			t.Errorf("--allow-writes must offer %s", want)
		}
	}
	for _, never := range []string{"zbxctl_run_script", "zbxctl_upgrade", "zbxctl_context_use", "zbxctl_state_prune", "zbxctl_export_template", "zbxctl_import_template", "zbxctl_diff_template", "zbxctl_mcp", "zbxctl_llm_capabilities"} {
		if slices.Contains(rw, never) {
			t.Errorf("%s must never be offered", never)
		}
	}
	// no tool takes a file path or a policy flag
	for _, tool := range buildMCPTools(true) {
		for flag := range tool.InputSchema["properties"].(map[string]any) {
			if mcpHiddenFlags[flag] {
				t.Errorf("%s exposes %s", tool.Name, flag)
			}
		}
	}
}

func TestMCPToolCallBuildsTheCommandLineAndReportsFailure(t *testing.T) {
	old := mcpRunner
	defer func() { mcpRunner = old }()
	mcpRunner = func(_ context.Context, args []string) (string, string, int) {
		if slices.Contains(args, "fail") {
			return "", `{"error":"boom","kind":"error","code":1}`, 1
		}
		return `{"data":[]}`, "", 0
	}
	msgs := mcpExchange(t, true,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"zbxctl_get_problems","arguments":{"args":[],"min_x":1}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"zbxctl_get_problems","arguments":{"severity":"high","limit":5,"exclude-orphans":true}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"zbxctl_disable_host","arguments":{"args":["web1","web2"],"apply":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"zbxctl_disable_host","arguments":{"args":["fail"]}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"zbxctl_nope","arguments":{}}}`)
	if msgs[0]["result"].(map[string]any)["isError"] != true { // an unknown argument is reported, not run
		t.Fatalf("unknown argument must be an error result: %v", msgs[0])
	}
	line := mcpRunnerCapture(t, `{"severity":"high","limit":5,"exclude-orphans":true}`, "zbxctl_get_problems")
	if want := "get problems --exclude-orphans --limit 5 --severity high --output json"; strings.Join(line, " ") != want {
		t.Fatalf("command line: %v", line)
	}
	third := msgs[2]["result"].(map[string]any)
	if third["isError"] != false {
		t.Fatalf("success: %v", third)
	}
	fourth := msgs[3]["result"].(map[string]any)
	if fourth["isError"] != true || !strings.Contains(fourth["content"].([]any)[0].(map[string]any)["text"].(string), "boom") {
		t.Fatalf("a failing command must come back as an error result carrying its message: %v", fourth)
	}
	if msgs[4]["error"] == nil {
		t.Fatalf("an unknown tool is a protocol error: %v", msgs[4])
	}
}

// mcpRunnerCapture returns the arguments the runner receives for one tool call.
func mcpRunnerCapture(t *testing.T, argsJSON, tool string) []string {
	t.Helper()
	old := mcpRunner
	defer func() { mcpRunner = old }()
	var got []string
	mcpRunner = func(_ context.Context, args []string) (string, string, int) { got = args; return "{}", "", 0 }
	mcpExchange(t, true, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":`+argsJSON+`}}`)
	return got
}

func TestMCPApplyIsExplicit(t *testing.T) {
	dry := mcpRunnerCapture(t, `{"args":["web1"]}`, "zbxctl_disable_host")
	if slices.Contains(dry, "--apply") {
		t.Fatalf("a write without apply=true must stay a dry run: %v", dry)
	}
	applied := mcpRunnerCapture(t, `{"args":["web1"],"apply":true}`, "zbxctl_disable_host")
	if !slices.Contains(applied, "--apply") {
		t.Fatalf("apply=true must pass --apply: %v", applied)
	}
}
