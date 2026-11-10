//go:build unix

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func exists2(p string) bool { _, err := os.Stat(p); return err == nil }

// After a good upgrade the previous binary is removed (it used to stay behind as .old), unless
// --keep-old; a new binary that does not run is replaced by the old one again.
func TestUpgradeRemovesThePreviousBinaryOnceTheNewOneRuns(t *testing.T) {
	smokeOK := func(string) error { return nil }
	newBin := "#!/bin/sh\necho new\n"

	dir := t.TempDir()
	bin := filepath.Join(dir, "zbxctl")
	writeExe(t, bin, "#!/bin/sh\necho old\n")
	if err := replaceExecutableAt(bin, []byte(newBin), false, smokeOK); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != newBin {
		t.Fatalf("the new binary must be in place: %q", b)
	}
	if exists2(bin + ".old") {
		t.Fatal("the previous binary must be removed after a good upgrade")
	}

	// --keep-old
	dir = t.TempDir()
	bin = filepath.Join(dir, "zbxctl")
	writeExe(t, bin, "#!/bin/sh\necho old\n")
	if err := replaceExecutableAt(bin, []byte(newBin), true, smokeOK); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin + ".old"); !strings.Contains(string(b), "echo old") {
		t.Fatalf("--keep-old must leave the previous binary as .old: %q", b)
	}

	// a new binary that does not run: the old one is put back, nothing is left behind
	dir = t.TempDir()
	bin = filepath.Join(dir, "zbxctl")
	oldBody := "#!/bin/sh\necho old\n"
	writeExe(t, bin, oldBody)
	err := replaceExecutableAt(bin, []byte("garbage"), false, func(string) error { return errors.New("exec format error") })
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("a binary that does not run must be refused: %v", err)
	}
	if b, _ := os.ReadFile(bin); string(b) != oldBody {
		t.Fatalf("the old binary must be back: %q", b)
	}
	if exists2(bin + ".old") {
		t.Fatal("nothing should be left behind after the restore")
	}
}

// The real smoke test starts the file and checks it answers like zbxctl version.
func TestSmokeRunStartsTheBinaryAndChecksItsAnswer(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	writeExe(t, good, "#!/bin/sh\necho '{\"data\":[{\"version\":\"9.9.9\"}]}'\n")
	if err := smokeRun(good); err != nil {
		t.Fatalf("a binary that answers with a version passes: %v", err)
	}
	bad := filepath.Join(dir, "bad")
	writeExe(t, bad, "#!/bin/sh\nexit 3\n")
	if err := smokeRun(bad); err == nil {
		t.Fatal("a binary that exits non-zero fails")
	}
	silent := filepath.Join(dir, "silent")
	writeExe(t, silent, "#!/bin/sh\necho hello\n")
	if err := smokeRun(silent); err == nil {
		t.Fatal("a binary that does not answer like zbxctl version fails")
	}
}
