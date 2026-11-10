package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/mwtrigg/zbxctl/internal/output"
	"io"
	"os"
	"strings"
)

// scriptPin is the allowlist entry for a script: its name (or ID) and a hash of
// the command it runs. The command is part of the entry because a script's name
// says nothing about what it does: anyone who can edit scripts in Zabbix could
// otherwise swap the command of an allowlisted script after it was reviewed.
func scriptPin(nameOrID, command string) string {
	sum := sha256.Sum256([]byte(command))
	return nameOrID + "@sha256:" + hex.EncodeToString(sum[:])
}

// scriptAllowed reports whether the allowlist permits running this script, and
// when it does not, says why and which entry would allow it.
func scriptAllowed(entries []string, name, id, command string) (bool, string) {
	pinName, pinID := scriptPin(name, command), scriptPin(id, command)
	sameScript := false
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == pinName || e == pinID {
			return true, ""
		}
		base, _, pinned := strings.Cut(e, "@sha256:")
		if base == name || base == id {
			sameScript = true
			if !pinned {
				return false, fmt.Sprintf("allowed_scripts entry %q is not pinned to the command; use %q", e, pinName)
			}
		}
	}
	if sameScript {
		return false, fmt.Sprintf("the command of script %q is not the one that was allowed (it changed, or was never reviewed); if you have reviewed it, the entry is %q", name, pinName)
	}
	return false, fmt.Sprintf("script %q is not in allowed_scripts for this context; to allow it as it is now, add %q", name, pinName)
}

// printable is output.Printable: it makes text safe to show on a terminal: control characters, including
// ESC, carriage return and the rest of what can rewrite or hide earlier output,
// are shown as visible escapes. Newline and tab are kept.
func printable(s string) string { return output.Printable(s) }

// isTerminal reports whether f is an interactive terminal: a character device
// other than /dev/null (which is one too, and is what a detached process gets).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
}

// openConfirmationInput returns where the confirmation token is read from: the
// controlling terminal, never stdin, so a script or an agent cannot read the
// token from stderr and feed it back through a pipe. It is replaced in tests.
var openConfirmationInput = func() (io.ReadCloser, error) {
	if !isTerminal(os.Stderr) {
		return nil, fmt.Errorf("run script needs a person at a terminal: stderr is not a terminal")
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return nil, fmt.Errorf("run script needs a person at a terminal: cannot open /dev/tty: %w", err)
	}
	return tty, nil
}
