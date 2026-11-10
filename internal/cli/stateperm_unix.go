//go:build unix

package cli

import (
	"fmt"
	"os"
	"syscall"
)

// checkStateDir refuses a before-state directory that someone else could tamper with: one
// that belongs to another user, or that other users can write to (they could delete
// records or plant files that look like them). A symlink is followed, and it is the target
// that is checked. The directory zbxctl creates itself is 0700 and passes.
func checkStateDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("the state directory %s is not a directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("the state directory %s belongs to another user (uid %d): not keeping audit records there", dir, st.Uid)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("the state directory %s can be written by other users (mode %04o): run chmod 700 on it, or point state_dir at a directory of your own", dir, fi.Mode().Perm())
	}
	return nil
}
