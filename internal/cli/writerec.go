package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/config"
	"github.com/mwtrigg/zbxctl/internal/output"
)

// addStateFlags registers the audit flags on a single-object write, which has
// no targets to batch but still saves a before-state file under --apply.
func addStateFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&bulkStateDir, "state-dir", "", "where the before-state file is written (default ~/.local/state/zbxctl)")
	f.BoolVar(&bulkNoState, "no-state", false, "do not write a before-state file")
}

// writeRecord is the audit record of one single-object write: begun before the
// API call, closed with its outcome after. It is the single-object counterpart
// of the bulk flow, so every write in zbxctl leaves the same kind of file.
type writeRecord struct {
	rec *stateRecorder
}

// beginWrite is what a single-object write calls instead of confirm. Without
// --apply it prints what would happen and returns proceed=false. With --apply
// it saves the before-state (unless --no-state) and returns proceed=true; the
// caller makes the API call and then reports the outcome with ok or failed.
//
//	name   the command, for the file name (e.g. "delete-user")
//	target what is changed (an ID or a name)
//	desc   the sentence shown in the dry run
//	before what a person would need to undo it
func beginWrite(cmd *cobra.Command, ctx *config.Context, name, target, desc string, before map[string]any) (*writeRecord, bool, error) {
	if !flagApply {
		_ = output.Print(output.Format(flagOutput),
			[]output.Column{{Key: "target", Header: "TARGET"}, {Key: "would", Header: "WOULD"}, {Key: "applied", Header: "APPLIED"}},
			[]map[string]any{{"target": target, "would": desc, "applied": false, "dry_run": true}})
		return nil, false, nil
	}
	applyStateSettings(cmd, ctx)
	w := &writeRecord{}
	if bulkNoState {
		return w, true, nil
	}
	r, err := newStateRecorder(name, []*step{{Key: target, Desc: desc, Before: before}})
	if err != nil {
		return nil, false, fmt.Errorf("cannot save the before-state (use --no-state to skip): %w", err)
	}
	w.rec = r
	fmt.Fprintf(os.Stderr, "before-state saved: %s\n", r.path)
	if err := r.mark(0, stateSent, nil); err != nil { // the caller makes the request next
		return nil, false, fmt.Errorf("the before-state file %s cannot be updated (%v): nothing was sent", r.path, err)
	}
	return w, true, nil
}

func (w *writeRecord) note(status string, err error) {
	if w == nil || w.rec == nil {
		return
	}
	if serr := w.rec.mark(0, status, err); serr != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot update the before-state file %s: %v\n", w.rec.path, serr)
	}
}

// ok records that the change went through.
func (w *writeRecord) ok() {
	w.note(stateApplied, nil)
	w.prune()
}

// prune applies state_keep once the outcome is recorded.
func (w *writeRecord) prune() {
	if w != nil && w.rec != nil {
		pruneOldStates(filepath.Dir(w.rec.path), w.rec.path)
	}
}

// failed records a failed write and returns the error to report. If Zabbix
// refused the request nothing changed and the file is removed; if the outcome
// is unclear (timeout, 502/503/504) the file is kept, marked unknown, and the
// error says so.
func (w *writeRecord) failed(err error) error {
	if definitive(err) {
		if w != nil && w.rec != nil {
			_ = os.Remove(w.rec.path)
			fmt.Fprintln(os.Stderr, "nothing was changed; the before-state file was removed")
		}
		return err
	}
	w.note(stateUnknown, err)
	w.prune()
	where := "(none was written: --no-state)"
	if w != nil && w.rec != nil {
		where = w.rec.path
	}
	return &outcomeUnknownError{fmt.Sprintf("%v. The request may or may not have been executed; check before retrying. The before-state file is kept: %s", err, where)}
}

// fieldsOf turns an API object into the plain map saved as before-state.
func fieldsOf(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{"value": string(b)}
	}
	return m
}

// singleWriteCommands are the commands that change one object and save a
// before-state through beginWrite; they all take --state-dir and --no-state.
var singleWriteCommands = []string{
	"create user", "delete user", "create usergroup", "delete usergroup",
	"set macro", "tag host", "untag host", "create host", "maintenance create",
	"create group", "update group", "create item", "update item", "create trigger", "update trigger",
	"create valuemap", "update valuemap", "create regexp", "update regexp",
}

func init() {
	for _, path := range singleWriteCommands {
		c, _, err := rootCmd.Find(strings.Fields(path))
		if err != nil || c == rootCmd || c.Flags().Lookup("no-state") != nil {
			continue
		}
		addStateFlags(c)
	}
}
