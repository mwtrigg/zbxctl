package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// rpcCall is one request the fake Zabbix received, params included, so a test
// can assert exactly what was sent to a write method.
type rpcCall struct {
	Method string
	Params map[string]any
}

// fakeReply is the fake server's answer to one call. Result is raw JSON; Status
// other than 0 or 200 is sent as the HTTP status instead (a proxy's 504, say);
// RPCError is a JSON-RPC error message.
type fakeReply struct {
	Result   string
	Status   int
	RPCError string
}

// fakeZabbix records every call and answers through Handler (which may be nil,
// meaning "[]" for everything but apiinfo.version).
type fakeZabbix struct {
	Calls   []rpcCall
	Handler func(call rpcCall) fakeReply
	// Context is YAML for the fields of the active context (permissions, allowed_scripts...),
	// written to a config file; the URL and token still come from the environment.
	Context string
}

func (f *fakeZabbix) called(method string) []rpcCall {
	var out []rpcCall
	for _, c := range f.Calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// runFake executes the command tree against f with a throwaway HOME and returns
// stdout and the command's error. Flag state is global in these tests, so it
// is cleared before the run.
func runFake(t *testing.T, f *fakeZabbix, args ...string) (string, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			// params that are an array (host.delete) do not fit the map
			var loose struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(body, &loose)
			req.Method = loose.Method
		}
		call := rpcCall{Method: req.Method, Params: req.Params}
		f.Calls = append(f.Calls, call)
		rep := fakeReply{Result: "[]"}
		if req.Method == "apiinfo.version" {
			rep.Result = `"7.4.0"`
		} else if f.Handler != nil {
			if h := f.Handler(call); h != (fakeReply{}) {
				rep = h
			}
		}
		if rep.Status != 0 && rep.Status != 200 {
			http.Error(w, "upstream", rep.Status)
			return
		}
		if rep.RPCError != "" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32500,"message":"Application error.","data":` + jsonString(rep.RPCError) + `},"id":1}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":` + rep.Result + `,"id":1}`))
	}))
	defer srv.Close()
	t.Setenv("ZBXCTL_URL", srv.URL)
	t.Setenv("ZBXCTL_API_TOKEN", "test-token")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ZBXCTL_STATE_DIR", "")

	if f.Context != "" {
		dir := os.Getenv("HOME") + "/.config/zbxctl"
		_ = os.MkdirAll(dir, 0o700)
		body := "current_context: t\ncontexts:\n  t:\n"
		for _, line := range strings.Split(strings.TrimSpace(f.Context), "\n") {
			body += "    " + line + "\n"
		}
		_ = os.WriteFile(dir+"/config.yaml", []byte(body), 0o600)
	}
	clearFlagState()
	defer clearFlagState()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	rootCmd.SetArgs(nil)
	return buf.String(), err
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

// xdgStateFiles lists the before-state files under the test's XDG_STATE_HOME.
func xdgStateFiles(t *testing.T) []string {
	t.Helper()
	dir := os.Getenv("XDG_STATE_HOME") + "/zbxctl"
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, dir+"/"+e.Name())
		}
	}
	return out
}

func writeTestFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }

func readFileString(path string) string { b, _ := os.ReadFile(path); return string(b) }
