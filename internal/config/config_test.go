package config

import "testing"

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
