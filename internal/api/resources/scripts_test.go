package resources

import "testing"

func TestScriptTypeLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0", "script"},
		{"1", "ipmi"},
		{"2", "ssh"},
		{"3", "telnet"},
		{"5", "webhook"},
		{"99", "99"},
	}
	for _, tt := range tests {
		if got := ScriptTypeLabel(tt.in); got != tt.want {
			t.Errorf("ScriptTypeLabel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExecuteOnLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0", "server"},
		{"1", "agent"},
		{"2", "server-proxy"},
		{"99", "99"},
	}
	for _, tt := range tests {
		if got := ExecuteOnLabel(tt.in); got != tt.want {
			t.Errorf("ExecuteOnLabel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
