package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mwtrigg/zbxctl/internal/output"
)

// extraResults overrides the fake server answer for a method in the next runCLI.
var extraResults map[string]string

// lastErr is the error from the most recent runCLI.
var lastErr error

// runCLI executes the command tree against a fake Zabbix and returns stdout
// and the list of API methods that were called.
func runCLI(t *testing.T, args ...string) (string, []string) {
	t.Helper()
	var methods []string
	disabled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		methods = append(methods, req.Method)
		res := "[]"
		if r, ok := extraResults[req.Method]; ok {
			res = r
		}
		switch req.Method {
		case "host.get":
			st := "0"
			if disabled {
				st = "1"
			}
			res = `[{"hostid":"10","host":"web1","name":"Web 1","status":"` + st + `"}]`
		case "host.update":
			disabled = true
			res = `{"hostids":["10"]}`
		case "event.get":
			res = `[{"eventid":"5","name":"E","acknowledged":"0","r_eventid":"0","objectid":"7"}]`
		case "trigger.get":
			res = `[{"triggerid":"7","manual_close":"1"}]`
		}
		if r, ok := extraResults[req.Method]; ok {
			res = r
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
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Data) != 1 || env.Data[0]["dry_run"] != true || env.Data[0]["applied"] != false {
		t.Fatalf("no dry-run record: %q (%v)", out, err)
	}
}

func TestApplyExecutesWrite(t *testing.T) {
	_, methods := runCLI(t, "disable", "host", "web1", "--apply", "--state-dir", t.TempDir())
	if !slices.Contains(methods, "host.update") {
		t.Fatalf("--apply did not call host.update: %v", methods)
	}
}

func TestYesIsAliasForApply(t *testing.T) {
	_, methods := runCLI(t, "disable", "host", "web1", "--yes", "--state-dir", t.TempDir())
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
	_, methods := runCLI(t, "acknowledge", "5", "--suppress", "--until", "7d", "--apply", "--state-dir", t.TempDir())
	ackSuppress, ackSuppressUntil = false, ""
	if lastErr != nil || !slices.Contains(methods, "event.acknowledge") {
		t.Fatalf("err=%v methods=%v", lastErr, methods)
	}
}

func resetBulkFlags() {
	bulkIDFile, bulkExpect, bulkStateDir, bulkNoState, bulkNoVerify = "", -1, "", false, false
}

func TestBulkExpectGuardRefusesBeforeChangingAnything(t *testing.T) {
	defer resetBulkFlags()
	_, methods := runCLI(t, "disable", "host", "web1", "web1", "--expect", "2", "--apply", "--state-dir", t.TempDir())
	if lastErr == nil || slices.Contains(methods, "host.update") {
		t.Fatalf("err=%v methods=%v", lastErr, methods)
	}
}

func TestBulkIDFileDedupeAndStateFile(t *testing.T) {
	defer resetBulkFlags()
	dir := t.TempDir()
	idf := dir + "/ids.txt"
	if err := os.WriteFile(idf, []byte("# targets\nweb1\n\n10   # same host by id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, methods := runCLI(t, "disable", "host", "--id-file", idf, "--expect", "1", "--apply", "--state-dir", dir)
	if lastErr != nil {
		t.Fatal(lastErr)
	}
	updates := 0
	for _, m := range methods {
		if m == "host.update" {
			updates++
		}
	}
	if updates != 1 {
		t.Fatalf("host.update called %d times, want 1 (duplicates collapse)", updates)
	}
	files, _ := filepath.Glob(dir + "/disable-host-*.json")
	if len(files) != 1 {
		t.Fatalf("state files: %v", files)
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), `"status": "monitored"`) {
		t.Fatalf("state file lacks the before-state: %s", b)
	}
	if fi, _ := os.Stat(files[0]); fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode %v", fi.Mode().Perm())
	}
}

func TestDryRunWritesNoStateFile(t *testing.T) {
	defer resetBulkFlags()
	dir := t.TempDir()
	runCLI(t, "disable", "host", "web1", "--state-dir", dir)
	if files, _ := filepath.Glob(dir + "/*.json"); len(files) != 0 {
		t.Fatalf("dry run wrote %v", files)
	}
}

const lostItemJSON = `[
	{"itemid":"100","name":"Lost fs /data","key_":"vfs.fs.size[/data,free]","hostid":"10","hosts":[{"hostid":"10","host":"web1"}],"itemDiscovery":{"status":"1","lastcheck":"1000","ts_delete":"5000","parent_itemid":"900"}},
	{"itemid":"101","name":"Live fs /","key_":"vfs.fs.size[/,free]","hostid":"10","hosts":[{"hostid":"10","host":"web1"}],"itemDiscovery":{"status":"0","lastcheck":"2000","ts_delete":"0","parent_itemid":"900"}}]`

func TestDeleteLostOnlyDeletesLostItems(t *testing.T) {
	defer resetBulkFlags()
	extraResults = map[string]string{"item.get": lostItemJSON, "itemprototype.get": `[{"itemid":"900","discoveryRule":{"itemid":"50"}}]`, "item.delete": `{"itemids":["100"]}`}
	defer func() { extraResults = nil; discHosts, discGroups = nil, nil }()

	_, methods := runCLI(t, "delete", "lost", "items", "--host", "web1", "--apply", "--no-verify", "--state-dir", t.TempDir())
	if lastErr != nil || !slices.Contains(methods, "item.delete") {
		t.Fatalf("err=%v methods=%v", lastErr, methods)
	}

	_, methods = runCLI(t, "delete", "lost", "items", "101", "--apply", "--no-verify", "--state-dir", t.TempDir())
	if lastErr == nil || slices.Contains(methods, "item.delete") {
		t.Fatalf("a live discovered item must be refused: err=%v methods=%v", lastErr, methods)
	}

	discHosts, discGroups = nil, nil // package-level flag values persist between runs in one process
	_, methods = runCLI(t, "delete", "lost", "items")
	if lastErr == nil || slices.Contains(methods, "item.delete") {
		t.Fatalf("no scope must be refused: err=%v", lastErr)
	}
}

func TestDeleteTemplateRefusesLinkedUnlessAllowed(t *testing.T) {
	defer resetBulkFlags()
	defer func() { extraResults = nil; tplAllowLinked = false }()
	extraResults = map[string]string{
		"template.get":         `[{"templateid":"7","host":"tpl","name":"Tpl","hosts":[{"host":"web1"}],"templates":[]}]`,
		"configuration.export": `"zabbix_export: yaml"`,
		"template.delete":      `{"templateids":["7"]}`,
	}
	_, methods := runCLI(t, "delete", "template", "tpl", "--apply", "--no-verify", "--state-dir", t.TempDir())
	if lastErr == nil || slices.Contains(methods, "template.delete") {
		t.Fatalf("a linked template must be refused: err=%v methods=%v", lastErr, methods)
	}
	dir := t.TempDir()
	_, methods = runCLI(t, "delete", "template", "tpl", "--allow-linked", "--apply", "--no-verify", "--state-dir", dir)
	if lastErr != nil || !slices.Contains(methods, "template.delete") {
		t.Fatalf("--allow-linked: err=%v methods=%v", lastErr, methods)
	}
	files, _ := filepath.Glob(dir + "/delete-template-*.json")
	if len(files) != 1 {
		t.Fatalf("state files: %v", files)
	}
	if b, _ := os.ReadFile(files[0]); !strings.Contains(string(b), "export_yaml") {
		t.Fatalf("before-state must keep the export: %s", b)
	}
}

func TestBacktestJudgesFiresAgainstRealProblems(t *testing.T) {
	defer func() { extraResults = nil; btHosts, btRealProblem, btSummary = nil, "", false }()
	now := time.Now().Unix()
	var hist []string
	for i := 0; i < 30; i++ { // one sample a minute; 95 between minutes 10 and 15, otherwise 20
		v := "20"
		if i >= 10 && i < 15 {
			v = "95"
		}
		hist = append(hist, `{"itemid":"1","clock":"`+strconv.FormatInt(now-int64(30-i)*60, 10)+`","ns":"0","value":"`+v+`"}`)
	}
	start := now - int64(20)*60 // the real problem began 10 minutes after the fire
	extraResults = map[string]string{
		"item.get":    `[{"itemid":"1","hostid":"10","key_":"m","value_type":"0","hosts":[{"hostid":"10","host":"web1"}]}]`,
		"history.get": "[" + strings.Join(hist, ",") + "]",
		"event.get":   `[{"eventid":"9","clock":"` + strconv.FormatInt(start+600, 10) + `","r_eventid":"0","name":"M high","hosts":[{"hostid":"10"}]}]`,
	}
	out, _ := runCLI(t, "backtest", "--host", "web1", "--item", "m", "--when", "last>90", "--since", "2h", "--real-problem", "M high", "--summary")
	if lastErr != nil {
		t.Fatal(lastErr)
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Data) != 1 {
		t.Fatalf("out=%q err=%v", out, err)
	}
	r := env.Data[0]
	if r["fires"].(float64) != 1 || r["true_alerts"].(float64) != 1 || r["false_alarms"].(float64) != 0 || r["missed"].(float64) != 0 {
		t.Fatalf("summary %v", r)
	}
}

func TestAllDryRunsShareOneShape(t *testing.T) {
	extraResults = map[string]string{"hostgroup.get": `[{"groupid":"5","name":"G"}]`}
	defer func() { extraResults = nil }()
	for _, args := range [][]string{
		{"disable", "host", "web1"},
		{"create", "host", "--name", "x", "--group", "G", "--ip", "192.0.2.1"},
	} {
		defer resetBulkFlags()
		out, _ := runCLI(t, args...)
		var env struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Data) != 1 || env.Data[0]["dry_run"] != true || env.Data[0]["applied"] != false {
			t.Errorf("%v: dry run is not {data:[{dry_run,applied,would}]}: %q (%v)", args, out, err)
		}
	}
}

func TestImportSavesPreviousExportAndHonoursExpect(t *testing.T) {
	defer resetBulkFlags()
	defer func() { extraResults = nil; importDir = "" }()
	extraResults = map[string]string{
		"configuration.importcompare": `{"templates":{"updated":[{"before":{"name":"T"},"after":{"name":"T"},"items":{"updated":[{"before":{"name":"I","delay":"30s"},"after":{"name":"I","delay":"60s"}}]}}]}}`,
		"configuration.export":        `"zabbix_export: previous"`,
		"configuration.import":        `true`,
		"template.get":                `[{"templateid":"7","host":"t","name":"T"}]`,
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/t.yaml", []byte("zabbix_export: new"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := t.TempDir()
	_, methods := runCLI(t, "import", "template", "--dir", dir, "--expect", "2", "--apply", "--state-dir", st)
	if lastErr == nil || slices.Contains(methods, "configuration.import") {
		t.Fatalf("--expect mismatch must stop before importing: err=%v", lastErr)
	}
	importDir = ""
	_, methods = runCLI(t, "import", "template", "--dir", dir, "--expect", "1", "--apply", "--state-dir", st)
	if lastErr != nil || !slices.Contains(methods, "configuration.import") {
		t.Fatalf("err=%v methods=%v", lastErr, methods)
	}
	files, _ := filepath.Glob(st + "/import-template-*.json")
	if len(files) != 1 {
		t.Fatalf("state files: %v", files)
	}
	if b, _ := os.ReadFile(files[0]); !strings.Contains(string(b), "zabbix_export: previous") {
		t.Fatalf("the previous export of the changed template must be saved: %s", b)
	}
	// dry run changes nothing and saves nothing
	st2 := t.TempDir()
	importDir = ""
	_, methods = runCLI(t, "import", "template", "--dir", dir, "--state-dir", st2)
	if slices.Contains(methods, "configuration.import") {
		t.Fatal("dry run imported")
	}
	if f, _ := filepath.Glob(st2 + "/*.json"); len(f) != 0 {
		t.Fatalf("dry run wrote %v", f)
	}
}

func TestMaintenanceCreateDefaultsToNowAndAcceptsHumanDurations(t *testing.T) {
	defer func() { extraResults = nil; maintCreateName, maintCreateHost, maintCreateDuration = "", "", "" }()
	extraResults = map[string]string{"host.get": `[{"hostid":"10","host":"web1","name":"W","status":"0"}]`}
	out, _ := runCLI(t, "maintenance", "create", "--name", "w", "--host", "web1", "--duration", "2h")
	if lastErr != nil || !strings.Contains(out, "create maintenance") {
		t.Fatalf("no --start and a 2h duration must work: err=%v out=%q", lastErr, out)
	}
	_, _ = runCLI(t, "maintenance", "create", "--name", "w", "--host", "web1", "--duration", "soon")
	if lastErr == nil {
		t.Fatal("a bad duration must be refused")
func TestDeleteGroupGuards(t *testing.T) {
	defer resetBulkFlags()
	defer func() { extraResults = nil; groupAllow = false }()
	extraResults = map[string]string{"hostgroup.get": `[{"groupid":"5","name":"G","hosts":"3"}]`, "hostgroup.delete": `{"groupids":["5"]}`}
	_, methods := runCLI(t, "delete", "group", "G", "--apply", "--no-verify", "--state-dir", t.TempDir())
	if lastErr == nil || slices.Contains(methods, "hostgroup.delete") {
		t.Fatalf("a group with hosts must be refused: err=%v", lastErr)
	}
	extraResults["hostgroup.get"] = `[{"groupid":"7","name":"Empty","hosts":"0"}]`
	_, methods = runCLI(t, "delete", "group", "Empty", "--apply", "--no-verify", "--state-dir", t.TempDir())
	if lastErr != nil || !slices.Contains(methods, "hostgroup.delete") {
		t.Fatalf("an empty group may be deleted: err=%v methods=%v", lastErr, methods)
	}
}

func TestSecretMacroValueNeverFromCommandLine(t *testing.T) {
	defer func() { extraResults = nil; smSecret, smValue = false, "" }()
	extraResults = map[string]string{"host.get": `[{"hostid":"10","host":"web1","name":"W","status":"0"}]`}
	_, methods := runCLI(t, "set", "macro", "--host", "web1", "--macro", "{$S}", "--secret", "--value", "hunter2", "--apply")
	if lastErr == nil || slices.Contains(methods, "usermacro.create") {
		t.Fatalf("a secret on the command line must be refused: err=%v methods=%v", lastErr, methods)
	}
}

func TestDeleteItemAndTriggerRefuseDiscoveredAndTemplated(t *testing.T) {
	defer resetBulkFlags()
	defer func() { extraResults = nil }()
	for _, c := range []struct{ name, flags, method string }{
		{"discovered item", `{"itemid":"1","hostid":"10","name":"n","key_":"k","flags":"4","templateid":"0","hosts":[{"host":"web1"}]}`, "item.delete"},
		{"templated item", `{"itemid":"1","hostid":"10","name":"n","key_":"k","flags":"0","templateid":"77","hosts":[{"host":"web1"}]}`, "item.delete"},
	} {
		extraResults = map[string]string{"item.get": "[" + c.flags + "]", "item.delete": `{"itemids":["1"]}`}
		_, methods := runCLI(t, "delete", "item", "1", "--apply", "--no-verify", "--state-dir", t.TempDir())
		if lastErr == nil || slices.Contains(methods, c.method) {
			t.Errorf("%s must be refused: err=%v methods=%v", c.name, lastErr, methods)
		}
	}
	extraResults = map[string]string{"trigger.get": `[{"triggerid":"5","description":"d","expression":"x","priority":"3","status":"0","flags":"4","templateid":"0","hosts":[{"host":"web1"}]}]`, "trigger.delete": `{"triggerids":["5"]}`}
	_, methods := runCLI(t, "delete", "trigger", "5", "--apply", "--no-verify", "--state-dir", t.TempDir())
	if lastErr == nil || slices.Contains(methods, "trigger.delete") {
		t.Errorf("discovered trigger must be refused: err=%v", lastErr)
	}
	extraResults["trigger.get"] = `[{"triggerid":"5","description":"d","expression":"x","priority":"3","status":"0","flags":"0","templateid":"0","hosts":[{"host":"web1"}]}]`
	_, methods = runCLI(t, "delete", "trigger", "5", "--apply", "--no-verify", "--state-dir", t.TempDir())
	if lastErr != nil || !slices.Contains(methods, "trigger.delete") {
		t.Errorf("a plain trigger may be deleted: err=%v methods=%v", lastErr, methods)
	}
}

func TestCreateItemNeedsDelayAndKnownType(t *testing.T) {
	defer func() {
		extraResults = nil
		itHost, itName, itKey, itType, itValueType, itDelay = "", "", "", "agent", "float", ""
	}()
	_, methods := runCLI(t, "create", "item", "--host", "web1", "--name", "n", "--key", "k", "--type", "bogus", "--apply")
	if lastErr == nil || slices.Contains(methods, "item.create") {
		t.Fatalf("bad type: %v", lastErr)
	}
	_, methods = runCLI(t, "create", "item", "--host", "web1", "--name", "n", "--key", "k", "--type", "agent", "--apply")
	if lastErr == nil || slices.Contains(methods, "item.create") {
		t.Fatalf("agent item without --delay: %v", lastErr)
	}
}
