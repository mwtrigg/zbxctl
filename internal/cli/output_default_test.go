package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A developer may have ZBXCTL_OUTPUT=table in their shell: the tests read JSON, so
// they start without it.
func init() { os.Unsetenv("ZBXCTL_OUTPUT") }

func TestDefaultOutputFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZBXCTL_CONTEXT", "")
	t.Setenv("ZBXCTL_OUTPUT", "")
	oldContext, oldOutput := flagContext, flagOutput
	flagContext, flagOutput = "", "json" // as at the start of a run, whatever an earlier test left
	t.Cleanup(func() { flagContext, flagOutput = oldContext, oldOutput })

	if got, err := defaultOutput(); err != nil || got != "json" {
		t.Fatalf("nothing set: %q %v, want json", got, err)
	}
	dir := filepath.Join(home, ".config", "zbxctl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "current_context: lab\ncontexts:\n  lab:\n    url: https://ctl.example.com\n    output: csv\n  other:\n    url: https://ctl.example.com\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := defaultOutput(); err != nil || got != "csv" {
		t.Errorf("the context's output setting: %q %v, want csv", got, err)
	}
	flagContext = "other" // a context without the setting
	if got, _ := defaultOutput(); got != "json" {
		t.Errorf("a context without output falls back to json: %q", got)
	}
	flagContext = ""
	t.Setenv("ZBXCTL_OUTPUT", "table")
	if got, err := defaultOutput(); err != nil || got != "table" {
		t.Errorf("the environment beats the config: %q %v, want table", got, err)
	}
	if errorFormat() != "table" {
		t.Errorf("an early error uses the environment's format too: %q", errorFormat())
	}
	t.Setenv("ZBXCTL_OUTPUT", "xml")
	if _, err := defaultOutput(); err == nil {
		t.Error("an unknown format in the environment must be refused")
	}
	if errorFormat() != "json" {
		t.Errorf("an invalid environment value must not decide how its own error is printed: %q", errorFormat())
	}
}
