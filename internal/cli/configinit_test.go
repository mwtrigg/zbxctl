package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execIn runs the command tree with HOME set to home and text on standard input.
func execIn(t *testing.T, home, stdin string, args ...string) error {
	t.Helper()
	t.Setenv("HOME", home)
	for _, k := range []string{"ZBXCTL_URL", "ZBXCTL_API_TOKEN", "ZBXCTL_CONTEXT", "ZBXCTL_PERMISSIONS", "ZBXCTL_MODE", "ZBXCTL_FROM_MCP"} {
		t.Setenv(k, "")
	}
	r, w, _ := os.Pipe()
	_, _ = w.WriteString(stdin)
	w.Close()
	oldIn, oldErr := os.Stdin, os.Stderr
	devnull, _ := os.Open(os.DevNull)
	os.Stdin, os.Stderr = r, devnull
	defer func() { os.Stdin, os.Stderr = oldIn, oldErr; devnull.Close() }()
	// Flags keep their value between runs of the same command tree: say each one.
	full := []string{"config", "init", "--context", "default", "--url", "", "--permissions", "readonly", "--token-stdin=false"}
	if len(args) > 0 {
		full = append([]string{"config", "init"}, args...)
	}
	rootCmd.SetArgs(full)
	return rootCmd.Execute()
}

func TestConfigInitWritesAPrivateFileOnce(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "zbxctl", "config.yaml")
	// A plain http address is refused and nothing is written.
	if err := execIn(t, home, "tok\n", "--context", "lab", "--url", "http://zabbix.example.com", "--permissions", "readonly", "--token-stdin"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("http must be refused: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a refused init wrote a file")
	}
	// The token comes in on standard input, never as a flag.
	if err := execIn(t, home, "fake-token\n", "--context", "lab", "--url", "https://zabbix.example.com/", "--permissions", "operator", "--token-stdin"); err != nil {
		t.Fatalf("config init: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the file is private: %v %v", err, info)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"current_context: lab", "url: https://zabbix.example.com\n", `api_token: "fake-token"`, "permissions: operator"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the file lacks %q:\n%s", want, data)
		}
	}
	// Never over an existing file.
	if err := execIn(t, home, "", "--context", "other", "--url", "https://other.example.com", "--permissions", "write", "--token-stdin=false"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second init: %v", err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(data) {
		t.Error("the existing file was changed")
	}
	other := t.TempDir()
	for want, args := range map[string][]string{
		"--permissions is": {"--context", "x", "--url", "https://z.example.com", "--permissions", "admin", "--token-stdin=false"},
		"--context is":     {"--context", "a b", "--url", "https://z.example.com", "--permissions", "readonly", "--token-stdin=false"},
		"no token":         {"--context", "x", "--url", "https://z.example.com", "--permissions", "readonly", "--token-stdin"},
	} {
		if err := execIn(t, other, "", args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: %v (want %q)", args, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(other, ".config", "zbxctl", "config.yaml")); err == nil {
		t.Error("a refused init wrote a file")
	}
}

// One object as a table is printed down the page, one field a line.
func TestOneObjectAsATableIsARecord(t *testing.T) {
	out, _ := runCLI(t, "get", "host", "web1", "-o", "table")
	if lastErr != nil {
		t.Fatal(lastErr)
	}
	found := false
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		found = found || (len(f) == 2 && f[0] == "host" && f[1] == "web1")
	}
	if !found || strings.Contains(out, "HOST_ID") {
		t.Errorf("one field a line, by field name:\n%s", out)
	}
	// A list stays a table, and JSON is unchanged.
	if out, _ := runCLI(t, "get", "hosts", "-o", "table"); !strings.Contains(out, "HOST") {
		t.Errorf("a list is still a table:\n%s", out)
	}
	if out, _ := runCLI(t, "get", "host", "web1", "-o", "json"); !strings.Contains(out, `"host": "web1"`) {
		t.Errorf("JSON is as before:\n%s", out)
	}
}

// A context that applies by default is a person's convenience: a command
// started by the MCP server still only shows what it would do.
func TestMCPChildDoesNotApplyBecauseOfTheContextMode(t *testing.T) {
	handler := func(call rpcCall) fakeReply {
		switch call.Method {
		case "hostgroup.get":
			return fakeReply{Result: "[]"}
		case "hostgroup.create":
			return fakeReply{Result: `{"groupids":["50"]}`}
		}
		return fakeReply{}
	}
	t.Setenv("ZBXCTL_FROM_MCP", "1")
	t.Setenv("ZBXCTL_MODE", "apply")
	f := &fakeZabbix{Handler: handler}
	if _, err := runFake(t, f, "create", "group", "--name", "G1"); err != nil || len(f.called("hostgroup.create")) != 0 {
		t.Fatalf("mode: apply must not reach an MCP child: %v, calls %v", err, f.called("hostgroup.create"))
	}
	// A person with the same mode does apply.
	t.Setenv("ZBXCTL_FROM_MCP", "")
	f = &fakeZabbix{Handler: handler}
	if _, err := runFake(t, f, "create", "group", "--name", "G1"); err != nil || len(f.called("hostgroup.create")) != 1 {
		t.Fatalf("mode: apply for a person: %v", err)
	}
}
