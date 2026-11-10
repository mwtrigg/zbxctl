package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// runCLI executes the command tree against a fake Zabbix and returns stdout
// and the list of API methods that were called.
func runCLI(t *testing.T, args ...string) (string, []string) {
	t.Helper()
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		methods = append(methods, req.Method)
		res := "[]"
		switch req.Method {
		case "host.get":
			res = `[{"hostid":"10","host":"web1","name":"Web 1","status":"0"}]`
		case "host.update":
			res = `{"hostids":["10"]}`
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":` + res + `,"id":1}`))
	}))
	defer srv.Close()
	t.Setenv("ZBXCTL_URL", srv.URL)
	t.Setenv("ZBXCTL_API_TOKEN", "test-token")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	rootCmd.SetArgs(append([]string{"--apply=false", "--yes=false", "--raw=false"}, args...))
	err := rootCmd.Execute()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return buf.String(), methods
}

func TestWritesAreDryRunByDefault(t *testing.T) {
	out, methods := runCLI(t, "disable", "host", "web1")
	if slices.Contains(methods, "host.update") {
		t.Fatalf("dry run called host.update: %v", methods)
	}
	if !strings.Contains(out, `"dry_run":true`) || !strings.Contains(out, `"applied":false`) {
		t.Fatalf("no dry-run record: %q", out)
	}
}

func TestApplyExecutesWrite(t *testing.T) {
	_, methods := runCLI(t, "disable", "host", "web1", "--apply")
	if !slices.Contains(methods, "host.update") {
		t.Fatalf("--apply did not call host.update: %v", methods)
	}
}

func TestYesIsAliasForApply(t *testing.T) {
	_, methods := runCLI(t, "disable", "host", "web1", "--yes")
	if !slices.Contains(methods, "host.update") {
		t.Fatalf("--yes did not call host.update: %v", methods)
	}
}
