package config

import (
	"os"
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
		{"empty defaults to write", "", "write", false},
		{"unknown tier defaults to write", "superadmin", "write", false},
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
