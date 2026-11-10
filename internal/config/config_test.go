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
