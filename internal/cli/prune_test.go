package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// putState writes a before-state file with an explicit time string and status.
func putState(t *testing.T, dir, name, when, status string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"command": "enable-host", "time": when,
		"items": []map[string]any{{"target": "1", "action": "x", "before": map[string]any{}, "status": status}}})
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Sabotage S14: pruning must keep the NEWEST files when several share a second, and
// must never delete the file that was just written.
func TestPruneKeepsTheNewestWhenSeveralShareASecond(t *testing.T) {
	defer resetBulkFlags()
	dir := t.TempDir()
	same := time.Now().UTC().Format(time.RFC3339)
	var paths []string
	for i := 1; i <= 6; i++ { // names carry the instant, so f..-1 is the oldest
		paths = append(paths, putState(t, dir, fmt.Sprintf("enable-host-20261110T000000.%09dZ-aa.json", i), same, "applied"))
	}
	just := paths[0] // even the OLDEST name, if it is the one just written, must survive
	stateKeep = 3
	pruneOldStates(dir, just)
	for i, p := range paths {
		want := i >= 3 || p == just // the three newest by name, plus the one just written
		if exists(p) != want {
			t.Errorf("file %d exists=%v want %v", i+1, exists(p), want)
		}
	}
}

func TestPruneNeverDeletesUnresolvedRecords(t *testing.T) {
	defer resetBulkFlags()
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	unknown := putState(t, dir, "a-unknown.json", old, "unknown")
	pending := putState(t, dir, "b-pending.json", old, "pending")
	sent := putState(t, dir, "c-sent.json", old, "sent")
	done := putState(t, dir, "d-done.json", old, "applied")
	newest := putState(t, dir, "e-new.json", time.Now().UTC().Format(time.RFC3339), "applied")
	stateKeep = 1
	pruneOldStates(dir, newest)
	for _, p := range []string{unknown, pending, sent, newest} {
		if !exists(p) {
			t.Errorf("%s is the only record of an unresolved write and must be kept", filepath.Base(p))
		}
	}
	if exists(done) {
		t.Error("an old, resolved record beyond the limit should go")
	}
}

// N1: over MCP nothing prunes, and no flag can ask it to.
func TestNoPruningUnderMCPAndNoStateKeepFlag(t *testing.T) {
	defer resetBulkFlags()
	dir := t.TempDir()
	var ps []string
	for i := 0; i < 5; i++ {
		ps = append(ps, putState(t, dir, fmt.Sprintf("old-%d.json", i), time.Now().Add(-time.Duration(i+1)*time.Hour).UTC().Format(time.RFC3339), "applied"))
	}
	stateKeep = 1
	t.Setenv("ZBXCTL_FROM_MCP", "1")
	pruneOldStates(dir, "")
	for _, p := range ps {
		if !exists(p) {
			t.Fatalf("an MCP call must never delete audit records: %s", filepath.Base(p))
		}
	}
	for _, tool := range buildMCPTools(true) {
		for _, hidden := range []string{"state-keep", "no-verify", "no-state", "state-dir"} {
			if _, ok := tool.flags[hidden]; ok {
				t.Errorf("%s offers %s", tool.Name, hidden)
			}
		}
	}
	if err := runChildArgs("enable", "host", "x", "--state-keep", "1"); err == nil || !strings.Contains(err.Error(), "over MCP") {
		t.Fatalf("the child must refuse --state-keep: %v", err)
	}
}

// The prune runs after the write, never before: a write Zabbix refuses must not have
// rotated anything away.
func TestARefusedWriteDoesNotPruneAnything(t *testing.T) {
	f := &fakeZabbix{Handler: func(call rpcCall) fakeReply {
		if call.Method == "host.update" {
			return fakeReply{RPCError: "denied"}
		}
		return hostGetter(call)
	}}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := os.Getenv("XDG_STATE_HOME") + "/zbxctl"
	_ = os.MkdirAll(dir, 0o700)
	var ps []string
	for i := 0; i < 5; i++ {
		ps = append(ps, putState(t, dir, fmt.Sprintf("old-%d.json", i), time.Now().Add(-time.Duration(i+1)*time.Hour).UTC().Format(time.RFC3339), "applied"))
	}
	// runFake resets XDG_STATE_HOME, so point the command at our directory instead
	_, err := runFake(t, f, "disable", "host", "web1", "--apply", "--state-dir", dir, "--state-keep", "1")
	if err == nil {
		t.Fatal("the write is refused")
	}
	for _, p := range ps {
		if !exists(p) {
			t.Fatalf("a refused write pruned %s", filepath.Base(p))
		}
	}
}
