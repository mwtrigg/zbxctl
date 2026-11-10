//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A before-state directory that other users can write to (or that is not ours) could have
// its records deleted or replaced, so writes refuse to use it.
func TestStateDirMustNotBeWritableByOthers(t *testing.T) {
	defer resetBulkFlags()
	steps := []*step{{Key: "1", Desc: "x"}}
	// a directory of our own, mode 0700: fine
	ok := t.TempDir()
	bulkStateDir = ok
	if _, err := writeState("enable-host", steps); err != nil {
		t.Fatalf("a private directory is fine: %v", err)
	}
	// group- or world-writable: refused
	for _, mode := range []os.FileMode{0o770, 0o777, 0o702} {
		d := filepath.Join(t.TempDir(), "state")
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, mode); err != nil {
			t.Fatal(err)
		}
		bulkStateDir = d
		if _, err := writeState("enable-host", steps); err == nil || !strings.Contains(err.Error(), "written by other users") {
			t.Errorf("mode %04o must be refused: %v", mode, err)
		}
	}
	// a symlink to such a directory is judged by its target
	target := filepath.Join(t.TempDir(), "open")
	_ = os.Mkdir(target, 0o700)
	_ = os.Chmod(target, 0o777)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	bulkStateDir = link
	if _, err := writeState("enable-host", steps); err == nil {
		t.Error("a symlink to a world-writable directory must be refused")
	}
	// a path that is a file
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	bulkStateDir = f
	if _, err := writeState("enable-host", steps); err == nil {
		t.Error("a file is not a state directory")
	}
}
