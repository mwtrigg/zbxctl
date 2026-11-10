package config

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestCheckPermission(t *testing.T) {
	tests := []struct {
		name     string
		tier     string
		required string
		wantErr  bool
	}{
		{"write permits write", "write", "write", false},
		{"write permits operator", "write", "operator", false},
		{"write permits readonly", "write", "readonly", false},
		{"operator permits operator", "operator", "operator", false},
		{"operator permits readonly", "operator", "readonly", false},
		{"operator blocks write", "operator", "write", true},
		{"readonly permits readonly", "readonly", "readonly", false},
		{"readonly blocks operator", "readonly", "operator", true},
		{"readonly blocks write", "readonly", "write", true},
		{"empty defaults to readonly: no write", "", "write", true},
		{"empty defaults to readonly: no operator", "", "operator", true},
		{"empty still allows reads", "", "readonly", false},
		{"unknown tier is refused for write", "superadmin", "write", true},
		{"unknown tier is refused even for readonly", "read-only", "readonly", true},
		{"tier is case and space tolerant", " ReadOnly ", "write", true},
		{"tier is case and space tolerant, allowed", " Operator ", "operator", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &Context{Permissions: tt.tier}
			err := ctx.CheckPermission(tt.required)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckPermission(%q) with tier %q: err=%v wantErr=%v",
					tt.required, tt.tier, err, tt.wantErr)
			}
		})
	}
}

func TestBeforeStateSettingDefaultsOnAndEnvOverridesFile(t *testing.T) {
	if !(&Context{}).SaveBeforeState() {
		t.Fatal("before-state files default to on")
	}
	off := false
	if (&Context{BeforeState: &off}).SaveBeforeState() {
		t.Fatal("before_state: false must turn them off")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := home + "/.config/zbxctl"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "current_context: lab\ncontexts:\n  lab:\n    url: http://z\n    api_token: x\n    before_state: false\n    state_dir: /var/tmp/zstate\n"
	if err := os.WriteFile(dir+"/config.yaml", []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZBXCTL_BEFORE_STATE", "")
	t.Setenv("ZBXCTL_STATE_DIR", "")
	_, ctx, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if ctx.SaveBeforeState() || ctx.StateDir != "/var/tmp/zstate" {
		t.Fatalf("file settings not read: %+v", ctx)
	}
	t.Setenv("ZBXCTL_BEFORE_STATE", "true")
	t.Setenv("ZBXCTL_STATE_DIR", "/elsewhere")
	_, ctx, _ = LoadConfig("")
	if !ctx.SaveBeforeState() || ctx.StateDir != "/elsewhere" {
		t.Fatalf("the environment must override the file: %+v", ctx)
	}
}

// writeConfig puts a config file under a throwaway HOME and returns nothing.
func writeConfig(t *testing.T, yaml string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := home + "/.config/zbxctl"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/config.yaml", []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ZBXCTL_CONTEXT", "ZBXCTL_URL", "ZBXCTL_API_TOKEN"} {
		t.Setenv(k, "")
	}
}

func TestLoadConfigFailsClosed(t *testing.T) {
	const base = "current_context: lab\ncontexts:\n  lab:\n    url: https://zbx.example.com\n    api_token: t\n    permissions: readonly\n"
	t.Run("a good config loads", func(t *testing.T) {
		writeConfig(t, base)
		if _, ctx, err := LoadConfig(""); err != nil || ctx.CheckPermission("write") == nil {
			t.Fatalf("readonly must stay readonly: %v", err)
		}
	})
	t.Run("a misspelt tier is a config error", func(t *testing.T) {
		writeConfig(t, "contexts:\n  lab:\n    url: https://zbx.example.com\n    permissions: read-only\ncurrent_context: lab\n")
		if _, _, err := LoadConfig(""); err == nil || !contains(err.Error(), "not a known tier") {
			t.Fatalf("want a tier error, got %v", err)
		}
	})
	t.Run("an unknown context named by flag is an error even with env credentials", func(t *testing.T) {
		writeConfig(t, base)
		t.Setenv("ZBXCTL_URL", "https://zbx.example.com")
		t.Setenv("ZBXCTL_API_TOKEN", "t")
		if _, _, err := LoadConfig("nosuch"); err == nil || !contains(err.Error(), "not defined") {
			t.Fatalf("want a context error, got %v", err)
		}
	})
	t.Run("an unknown context named by ZBXCTL_CONTEXT is an error", func(t *testing.T) {
		writeConfig(t, base)
		t.Setenv("ZBXCTL_CONTEXT", "lab-ro")
		t.Setenv("ZBXCTL_URL", "https://zbx.example.com")
		if _, _, err := LoadConfig(""); err == nil {
			t.Fatal("a misspelt ZBXCTL_CONTEXT must not fall back to an empty context")
		}
	})
	t.Run("an unknown key is an error", func(t *testing.T) {
		writeConfig(t, "contexts:\n  lab:\n    url: https://zbx.example.com\n    permisions: readonly\ncurrent_context: lab\n")
		if _, _, err := LoadConfig(""); err == nil || !contains(err.Error(), "permisions") {
			t.Fatalf("a misspelt key must be named in the error, got %v", err)
		}
		if _, err := LoadConfigFile(); err == nil {
			t.Fatal("LoadConfigFile must reject unknown keys too")
		}
	})
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestContextUseKeepsCommentsAndOtherSettings(t *testing.T) {
	const src = `# my zbxctl config
current_context: lab   # the default
contexts:
  lab:
    url: https://lab.example.com
    permissions: readonly   # agents get this one
  prod:
    url: https://prod.example.com
`
	writeConfig(t, src)
	if err := WriteCurrentContext("prod"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(os.Getenv("HOME") + "/.config/zbxctl/config.yaml")
	got := string(b)
	for _, keep := range []string{"# my zbxctl config", "# agents get this one", "permissions: readonly", "url: https://lab.example.com"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("%q was lost:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, "current_context: prod") || strings.Contains(got, "null") {
		t.Fatalf("only current_context should change:\n%s", got)
	}
	if _, ctx, err := LoadConfig(""); err != nil || ctx.URL != "https://prod.example.com" {
		t.Fatalf("the new context must be active: %v", err)
	}
	if err := WriteCurrentContext("nosuch"); err == nil {
		t.Fatal("an unknown context must be refused")
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestLoadConfigWarnsAboutAReadableCredentialsFile(t *testing.T) {
	const src = "current_context: lab\ncontexts:\n  lab:\n    url: https://zbx.example.com\n    api_token: t\n"
	writeConfig(t, src)
	path := os.Getenv("HOME") + "/.config/zbxctl/config.yaml"
	if out := captureStderr(t, func() { _, _, _ = LoadConfig("") }); out != "" {
		t.Fatalf("a 0600 file needs no warning: %q", out)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := captureStderr(t, func() { _, _, _ = LoadConfig("") }); !strings.Contains(out, "chmod 600") {
		t.Fatalf("a world-readable file with a token must warn: %q", out)
	}
}

func TestStateKeepDefaultsToTwoHundredAndEnvOverridesFile(t *testing.T) {
	if (&Context{}).KeepStates() != 200 || DefaultStateKeep != 200 {
		t.Fatal("the default is 200")
	}
	zero, fifty := 0, 50
	if (&Context{StateKeep: &zero}).KeepStates() != 0 || (&Context{StateKeep: &fifty}).KeepStates() != 50 {
		t.Fatal("explicit values must be used, 0 meaning keep everything")
	}
	neg := -3
	if (&Context{StateKeep: &neg}).KeepStates() != 200 {
		t.Fatal("a negative value falls back to the default")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := home + "/.config/zbxctl"
	os.MkdirAll(dir, 0o700)
	os.WriteFile(dir+"/config.yaml", []byte("current_context: lab\ncontexts:\n  lab:\n    url: http://z\n    api_token: x\n    state_keep: 25\n"), 0o600)
	t.Setenv("ZBXCTL_STATE_KEEP", "")
	_, ctx, err := LoadConfig("")
	if err != nil || ctx.KeepStates() != 25 {
		t.Fatalf("file: %v %v", ctx, err)
	}
	t.Setenv("ZBXCTL_STATE_KEEP", "7")
	_, ctx, _ = LoadConfig("")
	if ctx.KeepStates() != 7 {
		t.Fatalf("the environment must override the file: %d", ctx.KeepStates())
	}
	t.Setenv("ZBXCTL_STATE_KEEP", "banana")
	_, ctx, _ = LoadConfig("")
	if ctx.KeepStates() != 25 {
		t.Fatalf("an unreadable value must be ignored: %d", ctx.KeepStates())
	}
}

func TestModeDefaultsToDryRunAndEnvOverridesFile(t *testing.T) {
	if (&Context{}).ApplyByDefault() || (&Context{Mode: "dry-run"}).ApplyByDefault() {
		t.Fatal("the default is dry-run")
	}
	if !(&Context{Mode: "apply"}).ApplyByDefault() || !(&Context{Mode: " Apply "}).ApplyByDefault() {
		t.Fatal("mode: apply applies by default")
	}
	for _, ok := range []string{"", "dry-run", "apply", "APPLY"} {
		if err := (&Context{Mode: ok}).ValidateMode(); err != nil {
			t.Errorf("%q must be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"yes", "true", "force", "apply!"} {
		if err := (&Context{Mode: bad}).ValidateMode(); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := home + "/.config/zbxctl"
	os.MkdirAll(dir, 0o700)
	os.WriteFile(dir+"/config.yaml", []byte("current_context: lab\ncontexts:\n  lab:\n    url: http://z\n    api_token: x\n    mode: apply\n"), 0o600)
	t.Setenv("ZBXCTL_MODE", "")
	_, ctx, err := LoadConfig("")
	if err != nil || !ctx.ApplyByDefault() {
		t.Fatalf("file: %+v %v", ctx, err)
	}
	t.Setenv("ZBXCTL_MODE", "dry-run")
	_, ctx, _ = LoadConfig("")
	if ctx.ApplyByDefault() {
		t.Fatal("the environment must override the file")
	}
}

func TestDanglingCurrentContextAndDefaultTierFailClosed(t *testing.T) {
	// current_context names a context that is not in the file; credentials come from the environment
	writeConfig(t, "current_context: gone\ncontexts:\n  ro:\n    url: https://zbx.example.com\n    permissions: readonly\n")
	t.Setenv("ZBXCTL_URL", "https://zbx.example.com")
	t.Setenv("ZBXCTL_API_TOKEN", "t")
	if _, _, err := LoadConfig(""); err == nil || !contains(err.Error(), "not defined") {
		t.Fatalf("a dangling current_context must be an error, got %v", err)
	}
	// no config file at all, environment credentials, no tier: readonly, not write
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZBXCTL_CONTEXT", "")
	t.Setenv("ZBXCTL_PERMISSIONS", "")
	_, ctx, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if ctx.CheckPermission("write") == nil {
		t.Fatal("a connection that was never given a tier must not be able to write")
	}
	t.Setenv("ZBXCTL_PERMISSIONS", "write")
	if _, ctx, _ := LoadConfig(""); ctx.CheckPermission("write") != nil {
		t.Fatal("ZBXCTL_PERMISSIONS=write is the explicit way to allow it")
	}
	t.Setenv("ZBXCTL_PERMISSIONS", "wirte")
	if _, _, err := LoadConfig(""); err == nil {
		t.Fatal("a misspelt ZBXCTL_PERMISSIONS must be an error")
	}
}

func TestActiveContextName(t *testing.T) {
	writeConfig(t, "current_context: lab\ncontexts:\n  lab:\n    url: https://a.example.com\n  prod:\n    url: https://b.example.com\n")
	if n, err := ActiveContextName(""); err != nil || n != "lab" {
		t.Fatalf("%q %v", n, err)
	}
	t.Setenv("ZBXCTL_CONTEXT", "prod")
	if n, _ := ActiveContextName(""); n != "prod" {
		t.Fatalf("the environment outranks current_context: %q", n)
	}
	if n, _ := ActiveContextName("lab"); n != "lab" {
		t.Fatalf("the flag outranks the environment: %q", n)
	}
	if _, err := ActiveContextName("nosuch"); err == nil {
		t.Fatal("an unknown name is an error")
	}
}

func TestZBXCTLPermissionsOnlyLowersAConfiguredTier(t *testing.T) {
	cfg := func(tier string) {
		writeConfig(t, "current_context: c\ncontexts:\n  c:\n    url: https://zbx.example.com\n    permissions: "+tier+"\n")
	}
	canWrite := func() bool {
		var ctx *Context
		captureStderr(t, func() { _, ctx, _ = LoadConfig("") })
		return ctx.CheckPermission("write") == nil
	}
	cfg("readonly")
	t.Setenv("ZBXCTL_PERMISSIONS", "write")
	var warn string
	warn = captureStderr(t, func() { _, _, _ = LoadConfig("") })
	if canWrite() || !strings.Contains(warn, "never raise") {
		t.Fatalf("the environment must not raise a readonly context (warn %q)", warn)
	}
	cfg("write")
	t.Setenv("ZBXCTL_PERMISSIONS", "readonly")
	if canWrite() {
		t.Fatal("the environment may lower a write context")
	}
	cfg("operator")
	t.Setenv("ZBXCTL_PERMISSIONS", "")
	if canWrite() {
		t.Fatal("operator cannot write")
	}
}
