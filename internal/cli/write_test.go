package cli

import (
	"bytes"
	"encoding/json"
	"errors"
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

// serverVersion, when set, is what the fake server reports for apiinfo.version.
var serverVersion string

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
		if req.Method == "apiinfo.version" && serverVersion != "" {
			res = `"` + serverVersion + `"`
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
	}
}

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

func TestAPICallGuards(t *testing.T) {
	defer func() { extraResults = nil; apiParams = "" }()
	extraResults = map[string]string{"proxy.get": `[{"name":"p1"}]`, "host.massupdate": `{"hostids":["1"]}`}

	out, methods := runCLI(t, "api", "call", "proxy.get", "--params", `{"output":["name"]}`)
	if lastErr != nil || !slices.Contains(methods, "proxy.get") || !strings.Contains(out, `"p1"`) {
		t.Fatalf("reads run directly: err=%v out=%q", lastErr, out)
	}
	for _, denied := range []string{"user.login", "token.generate", "script.execute", "user.checkAuthentication"} {
		_, methods = runCLI(t, "api", "call", denied)
		if lastErr == nil || slices.Contains(methods, denied) {
			t.Errorf("%s must be refused", denied)
		}
	}
	out, methods = runCLI(t, "api", "call", "host.massupdate", "--params", `{"hosts":[{"hostid":"1"}],"passwd":"hunter2"}`)
	if slices.Contains(methods, "host.massupdate") || strings.Contains(out, "hunter2") || !strings.Contains(out, "******") {
		t.Fatalf("a write must be a dry run with secrets masked: methods=%v out=%q", methods, out)
	}
	_, methods = runCLI(t, "api", "call", "host.massupdate", "--params", `{"hosts":[{"hostid":"1"}]}`, "--apply", "--state-dir", t.TempDir())
	if lastErr != nil || !slices.Contains(methods, "host.massupdate") {
		t.Fatalf("--apply runs it: err=%v", lastErr)
	}
	_, _ = runCLI(t, "api", "call", "not a method")
	if lastErr == nil {
		t.Fatal("malformed method names must be refused")
	}
	_, _ = runCLI(t, "api", "call", "host.get", "--params", `not json`)
	if lastErr == nil {
		t.Fatal("bad JSON must be refused")
	}
}

func TestEventVerbsAreSeparateCommands(t *testing.T) {
	defer func() {
		ackUnack, ackSuppress, ackUnsuppress, ackSuppressUntil, ackMessage = false, false, false, "", ""
	}()
	for verb, want := range map[string]string{"unacknowledge": "event.acknowledge", "suppress": "event.acknowledge", "unsuppress": "event.acknowledge"} {
		out, methods := runCLI(t, verb, "5", "--apply", "--no-verify", "--state-dir", t.TempDir())
		if lastErr != nil || !slices.Contains(methods, want) {
			t.Errorf("%s: err=%v methods=%v out=%s", verb, lastErr, methods, out)
		}
		ackUnack, ackSuppress, ackUnsuppress = false, false, false
	}
	out, _ := runCLI(t, "suppress", "5", "--until", "7d")
	ackSuppress, ackSuppressUntil = false, ""
	if !strings.Contains(out, "suppress event 5") || !strings.Contains(out, " until ") {
		t.Fatalf("the dry run must say what and until when: %s", out)
	}
	out, _ = runCLI(t, "unacknowledge", "5")
	ackUnack = false
	if !strings.Contains(out, "unacknowledge event 5") {
		t.Fatalf("dry run of unacknowledge: %s", out)
	}
}

func stateFiles(dir string) []string { f, _ := filepath.Glob(dir + "/*.json"); return f }

func TestBeforeStateCanBeDisabledByEnvAndOverriddenByFlag(t *testing.T) {
	defer resetBulkFlags()
	// env var off: --apply writes no file
	dir := t.TempDir()
	t.Setenv("ZBXCTL_BEFORE_STATE", "false")
	runCLI(t, "disable", "host", "web1", "--apply", "--state-dir", dir)
	if lastErr != nil || len(stateFiles(dir)) != 0 {
		t.Fatalf("ZBXCTL_BEFORE_STATE=false must skip the file: err=%v files=%v", lastErr, stateFiles(dir))
	}
	// an explicit --no-state=false wins over the env var
	resetBulkFlags()
	dir2 := t.TempDir()
	runCLI(t, "disable", "host", "web1", "--apply", "--state-dir", dir2, "--no-state=false")
	if lastErr != nil || len(stateFiles(dir2)) != 1 {
		t.Fatalf("the flag must win: err=%v files=%v", lastErr, stateFiles(dir2))
	}
	// env var on (default): the file is written
	resetBulkFlags()
	t.Setenv("ZBXCTL_BEFORE_STATE", "true")
	dir3 := t.TempDir()
	runCLI(t, "disable", "host", "web1", "--apply", "--state-dir", dir3)
	if lastErr != nil || len(stateFiles(dir3)) != 1 {
		t.Fatalf("on by default: err=%v files=%v", lastErr, stateFiles(dir3))
	}
}

func writeStateFixture(t *testing.T, dir, name, command string, age time.Duration, items int) {
	t.Helper()
	its := make([]map[string]any, items)
	for i := range its {
		its[i] = map[string]any{"target": strconv.Itoa(i), "action": "do " + command, "before": map[string]any{"x": i}}
	}
	b, _ := json.Marshal(map[string]any{"command": command, "time": time.Now().Add(-age).UTC().Format(time.RFC3339), "items": its})
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStateListShowAndPrune(t *testing.T) {
	defer func() { stateDirFlag, stateCommand, stateOlderThan = "", "", "" }()
	dir := t.TempDir()
	writeStateFixture(t, dir, "delete-host-old.json", "delete-host", 100*24*time.Hour, 2)
	writeStateFixture(t, dir, "disable-host-new.json", "disable-host", time.Hour, 1)
	writeStateFixture(t, dir, "delete-host-mid.json", "delete-host", 40*24*time.Hour, 1)
	os.WriteFile(filepath.Join(dir, "notes.json"), []byte(`{"hello":"not ours"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o600)

	out, _ := runCLI(t, "state", "list", "--state-dir", dir)
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Data) != 3 {
		t.Fatalf("list should show the 3 zbxctl files and skip the others: %q (%v)", out, err)
	}
	if env.Data[0]["file"] != "disable-host-new.json" || env.Data[2]["file"] != "delete-host-old.json" {
		t.Fatalf("newest first: %v", env.Data)
	}
	stateCommand = ""
	out, _ = runCLI(t, "state", "list", "--state-dir", dir, "--command", "delete-host")
	stateCommand = ""
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Data) != 2 {
		t.Fatalf("--command filter: %q", out)
	}

	out, _ = runCLI(t, "state", "show", "delete-host-old.json", "--state-dir", dir)
	if lastErr != nil || !strings.Contains(out, `"do delete-host"`) {
		t.Fatalf("show: err=%v out=%q", lastErr, out)
	}
	_, _ = runCLI(t, "state", "show", "notes.json", "--state-dir", dir)
	if lastErr == nil {
		t.Fatal("show must refuse a file that is not a before-state file")
	}

	// prune needs --older-than, is a dry run by default, and only removes matching zbxctl files
	stateOlderThan = ""
	_, _ = runCLI(t, "state", "prune", "--state-dir", dir)
	if lastErr == nil {
		t.Fatal("prune without --older-than must refuse")
	}
	stateOlderThan = ""
	runCLI(t, "state", "prune", "--state-dir", dir, "--older-than", "30d")
	if len(stateFiles(dir)) != 4 { // 3 ours + notes.json
		t.Fatalf("dry run deleted something: %v", stateFiles(dir))
	}
	stateOlderThan = ""
	_, _ = runCLI(t, "state", "prune", "--state-dir", dir, "--older-than", "30d", "--expect", "5", "--apply")
	if lastErr == nil || len(stateFiles(dir)) != 4 {
		t.Fatalf("--expect mismatch must delete nothing: err=%v", lastErr)
	}
	bulkExpect, stateOlderThan = -1, ""
	runCLI(t, "state", "prune", "--state-dir", dir, "--older-than", "30d", "--apply")
	left := stateFiles(dir)
	if lastErr != nil || len(left) != 2 {
		t.Fatalf("prune should leave the new file and the foreign one: err=%v left=%v", lastErr, left)
	}
	for _, f := range left {
		if strings.Contains(f, "old") || strings.Contains(f, "mid") {
			t.Fatalf("an old file survived: %v", left)
		}
	}
	bulkExpect = -1
}

func TestStateDirComesFromEnvWhenNoFlag(t *testing.T) {
	defer func() { stateDirFlag = "" }()
	dir := t.TempDir()
	writeStateFixture(t, dir, "a.json", "disable-host", time.Hour, 1)
	t.Setenv("ZBXCTL_STATE_DIR", dir)
	out, _ := runCLI(t, "state", "list")
	if !strings.Contains(out, "a.json") {
		t.Fatalf("ZBXCTL_STATE_DIR not used: %q", out)
	}
}

func TestOldServersAreRefusedAndNewOnesWarned(t *testing.T) {
	defer func() { serverVersion = ""; flagAllowUnsupported = false; versionWarned = false }()
	serverVersion = "7.0.5"
	_, methods := runCLI(t, "get", "hosts")
	if lastErr == nil || exitCodeOf(lastErr) != output.ExitVersion || slices.Contains(methods, "host.get") {
		t.Fatalf("a 7.0 server must be refused before any other call: err=%v methods=%v", lastErr, methods)
	}
	flagAllowUnsupported = true
	_, methods = runCLI(t, "get", "hosts", "--allow-unsupported")
	flagAllowUnsupported = false
	if lastErr != nil || !slices.Contains(methods, "host.get") {
		t.Fatalf("--allow-unsupported must let it through: err=%v", lastErr)
	}
	serverVersion = "8.0.1"
	_, methods = runCLI(t, "get", "hosts")
	if lastErr != nil || !slices.Contains(methods, "host.get") {
		t.Fatalf("a newer server works (with a warning): err=%v", lastErr)
	}
	serverVersion = "7.4.16"
	_, methods = runCLI(t, "get", "hosts")
	if lastErr != nil || !slices.Contains(methods, "host.get") {
		t.Fatalf("7.4 is supported: err=%v", lastErr)
	}
}

func TestPinnedVersionSkipsDetectionAndEnvOverrides(t *testing.T) {
	defer func() { serverVersion = ""; versionWarned = false }()
	serverVersion = "7.4.16"
	t.Setenv("ZBXCTL_API_VERSION", "8.0")
	_, methods := runCLI(t, "get", "hosts")
	if lastErr != nil || slices.Contains(methods, "apiinfo.version") || !slices.Contains(methods, "host.get") {
		t.Fatalf("a pinned version must not ask the server: err=%v methods=%v", lastErr, methods)
	}
	t.Setenv("ZBXCTL_API_VERSION", "6.0")
	_, methods = runCLI(t, "get", "hosts")
	if lastErr == nil || exitCodeOf(lastErr) != output.ExitVersion {
		t.Fatalf("a pin below 7.4 is refused too: err=%v", lastErr)
	}
	t.Setenv("ZBXCTL_API_VERSION", "banana")
	_, _ = runCLI(t, "get", "hosts")
	if lastErr == nil {
		t.Fatal("an unreadable pin must be an error")
	}
}

// exitCodeOf is the exit code a command error ends in: commands print their own
// structured error and return a cliErr carrying the code.
func exitCodeOf(err error) int {
	var ce *cliErr
	if errors.As(err, &ce) {
		return ce.code
	}
	return classify(err)
}
