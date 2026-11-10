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

	"github.com/mwtrigg/zbxctl/internal/output"
)

// lastErr is the error from the most recent runCLI.
var lastErr error

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
	lastErr = err
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

func TestFilterLandsInFilterObject(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "host.get" {
			got = req.Params
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":[{"hostid":"1","host":"a","name":"A","status":"1"},{"hostid":"2","host":"b","name":"B","status":"1"}],"id":1}`))
	}))
	defer srv.Close()
	t.Setenv("ZBXCTL_URL", srv.URL)
	t.Setenv("ZBXCTL_API_TOKEN", "x")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	rootCmd.SetArgs([]string{"--apply=false", "--raw=false", "--filter", "status=1", "--filter", "status=0", "--field", "host", "get", "hosts"})
	err := rootCmd.Execute()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	rootCmd.SetArgs(nil)
	flagFilter, flagField = nil, ""
	output.Field = ""
	if err != nil {
		t.Fatal(err)
	}
	f, ok := got["filter"].(map[string]any)
	if !ok || len(f["status"].([]any)) != 2 {
		t.Fatalf("filter not wrapped/merged: %v", got)
	}
	if _, top := got["status"]; top {
		t.Fatal("status at top level")
	}
	if buf.String() != "a\nb\n" {
		t.Fatalf("--field output %q", buf.String())
	}
}

func TestAcknowledgeFlagCombosAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"acknowledge", "5", "--suppress", "--unsuppress"},
		{"acknowledge", "5", "--unacknowledge", "--close"},
		{"acknowledge", "5", "--until", "7d"},
		{"acknowledge", "5", "--severity", "huge"},
	} {
		_, methods := runCLI(t, args...)
		if lastErr == nil {
			t.Errorf("%v: expected an error", args)
		}
		if slices.Contains(methods, "event.acknowledge") {
			t.Errorf("%v reached event.acknowledge", args)
		}
	}
	// flags are package-level; reset what these cases set
	ackSuppress, ackUnsuppress, ackUnack, ackClose, ackSuppressUntil, ackSeverity = false, false, false, false, "", ""
}

func TestAcknowledgeSuppressAppliesWithUntil(t *testing.T) {
	_, methods := runCLI(t, "acknowledge", "5", "--suppress", "--until", "7d", "--apply")
	ackSuppress, ackSuppressUntil = false, ""
	if lastErr != nil || !slices.Contains(methods, "event.acknowledge") {
		t.Fatalf("err=%v methods=%v", lastErr, methods)
	}
}
