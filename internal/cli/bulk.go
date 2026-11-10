package cli

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/config"
	"github.com/mwtrigg/zbxctl/internal/output"
)

// Flags shared by the bulk-capable write commands.
var (
	bulkIDFile   string
	bulkExpect   = -1
	bulkStateDir string
	bulkNoState  bool
	bulkNoVerify bool
	bulkKeep     = -1 // --state-keep; -1 = not given
)

// stateKeep is how many before-state files to keep once the settings are
// merged (flag, ZBXCTL_STATE_KEEP, config, default). 0 keeps everything.
var stateKeep = config.DefaultStateKeep

func addBulkFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&bulkIDFile, "id-file", "", "read more targets from this file, one per line (# comments allowed)")
	f.IntVar(&bulkExpect, "expect", -1, "refuse to run unless exactly N distinct targets resolve")
	f.StringVar(&bulkStateDir, "state-dir", "", "where the before-state file is written (default ~/.local/state/zbxctl)")
	f.BoolVar(&bulkNoState, "no-state", false, "do not write a before-state file")
	f.IntVar(&bulkKeep, "state-keep", -1, "keep at most this many before-state files, deleting the oldest as new ones are written (default 200; 0 keeps all)")
	f.BoolVar(&bulkNoVerify, "no-verify", false, "skip re-reading each target after the change")
}

// applyStateSettings merges the config file / environment settings for the
// before-state file with the flags. Priority: flag > ZBXCTL_* env var > config
// file > default (on, ~/.local/state/zbxctl).
func applyStateSettings(cmd *cobra.Command, ctx *config.Context) {
	if ctx == nil {
		return
	}
	if f := cmd.Flags().Lookup("no-state"); f == nil || !f.Changed {
		if !ctx.SaveBeforeState() {
			bulkNoState = true
		}
	}
	if bulkStateDir == "" && ctx.StateDir != "" {
		bulkStateDir = ctx.StateDir
	}
	if bulkKeep >= 0 {
		stateKeep = bulkKeep
	} else {
		stateKeep = ctx.KeepStates()
	}
}

// step is one target of a write: resolved, described, ready to apply.
type step struct {
	Key    string         // canonical ID, used to drop duplicate targets
	Desc   string         // what will happen, e.g. `disable host "web-01"`
	Before map[string]any // state captured before the change (saved to the state file)
	Apply  func() error
	Verify func() error // optional: re-read and check; nil result means confirmed
	// Proves is true when a passing Verify is proof the change happened, which is the
	// only case in which a re-read may turn an unclear failure (a timeout, a 504) into
	// success. A re-read of a field that already had the wanted value proves nothing
	// (the write may never have arrived), so such steps leave Proves false and the
	// outcome stays unknown.
	Proves      bool
	VerifyAfter time.Duration
	Row         map[string]any // result row after a successful apply
}

type bulkSpec struct {
	Name    string // command name for the state file, e.g. "disable-host"
	Tier    string // permission tier
	Cols    []output.Column
	Prepare func(c *api.Client, target string) (*step, error)
}

// collectTargets merges positional args and --id-file, dropping blanks and repeats.
func collectTargets(args []string) ([]string, error) {
	var all []string
	all = append(all, args...)
	if bulkIDFile != "" {
		b, err := os.ReadFile(bulkIDFile)
		if err != nil {
			return nil, fmt.Errorf("reading --id-file: %w", err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if i := strings.Index(line, "#"); i >= 0 {
				line = line[:i]
			}
			if line = strings.TrimSpace(line); line != "" {
				all = append(all, line)
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range all {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, &usageError{"no targets: give at least one argument or --id-file"}
	}
	return out, nil
}

// defaultStateDir is where before-state files go unless --state-dir,
// ZBXCTL_STATE_DIR or state_dir in the config says otherwise.
func defaultStateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "zbxctl"), nil
}

// stateDir resolves and creates the directory the before-state files go in.
func stateDir() (string, error) {
	dir := bulkStateDir
	if dir == "" {
		var err error
		if dir, err = defaultStateDir(); err != nil {
			return "", err
		}
	}
	return dir, os.MkdirAll(dir, 0o700)
}

// createStateFile reserves a new, empty before-state file. The name carries the
// time to the nanosecond and a random suffix, and the file is created with
// O_EXCL, so two runs in the same instant can never share or overwrite a file.
func createStateFile(name string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	for i := 0; i < 20; i++ {
		var rnd [3]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return "", err
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%s-%x.json", name, time.Now().UTC().Format("20060102T150405.000000000Z"), rnd))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return path, f.Close()
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot create a unique before-state file in %s", dir)
}

// pruneOldStates keeps the newest stateKeep before-state files in dir and
// deletes the rest. It runs after a write's outcome is known, never before. It
// never deletes justWritten, nor a file with a target whose outcome is still
// unresolved (pending or unknown): that is the record someone needs. It only
// touches files zbxctl wrote, says so on stderr when it deletes anything, and never
// fails the change that triggered it. Under MCP it never runs: an agent must not be
// able to rotate the audit trail away, so only a person runs `state prune`.
func pruneOldStates(dir, justWritten string) {
	if stateKeep <= 0 || os.Getenv("ZBXCTL_FROM_MCP") != "" {
		return
	}
	files, err := readStateFiles(dir)
	if err != nil || len(files) <= stateKeep {
		return
	}
	removed := 0
	for _, f := range files[stateKeep:] { // files are newest first
		if f.Path == justWritten || f.Unresolved {
			continue
		}
		if os.Remove(f.Path) == nil {
			removed++
		}
	}
	if removed > 0 {
		fmt.Fprintf(os.Stderr, "pruned %d old before-state file(s) (keeping %d)\n", removed, stateKeep)
	}
}

// Outcomes recorded per target in the before-state file.
const (
	statePending = "pending" // planned, not yet attempted
	stateSent    = "sent"    // the request is being sent; if the run ended here the outcome is unknown
	stateApplied = "applied" // the change went through
	stateFailed  = "failed"  // Zabbix refused it; nothing changed
	stateUnknown = "unknown" // the request may or may not have been executed
)

type stateItem struct {
	Target string         `json:"target"`
	Action string         `json:"action"`
	Before map[string]any `json:"before"`
	Status string         `json:"status"`
	Error  string         `json:"error,omitempty"`
}

// stateRecorder is a before-state file that is rewritten, atomically, as each
// target's outcome becomes known, so the file never claims more than happened.
type stateRecorder struct {
	path  string
	name  string
	time  string
	items []stateItem
}

func (r *stateRecorder) save() error {
	b, err := json.MarshalIndent(map[string]any{"command": r.name, "time": r.time, "items": r.items}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".state-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}

// mark records the outcome of target i. A failure to save is reported to the
// caller but never hides the outcome itself.
func (r *stateRecorder) mark(i int, status string, err error) error {
	r.items[i].Status = status
	if err != nil {
		r.items[i].Error = err.Error()
	}
	return r.save()
}

func newStateRecorder(name string, steps []*step) (*stateRecorder, error) {
	path, err := createStateFile(name)
	if err != nil {
		return nil, err
	}
	r := &stateRecorder{path: path, name: name, time: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, s := range steps {
		r.items = append(r.items, stateItem{Target: s.Key, Action: s.Desc, Before: s.Before, Status: statePending})
	}
	if err := r.save(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return r, nil
}

// writeState saves a before-state file for steps that are applied elsewhere (the
// callers that record a single change themselves).
func writeState(name string, steps []*step) (string, error) {
	r, err := newStateRecorder(name, steps)
	if err != nil {
		return "", err
	}
	return r.path, nil
}

// definitive reports whether err means Zabbix looked at the request and refused
// it, so nothing changed. Anything else (a timeout after the request was sent, a
// 502/503/504 from a proxy in front of a backend that may have finished, a broken
// connection) leaves the outcome unknown.
func definitive(err error) bool {
	var rpc *api.RPCError
	return errors.As(err, &rpc) || errors.Is(err, api.ErrAuth)
}

// outcomeUnknownError is returned when a write may or may not have happened.
type outcomeUnknownError struct{ msg string }

func (e *outcomeUnknownError) Error() string { return e.msg }

// runBulk is the shared flow of every write command: resolve ALL targets first
// (so a typo changes nothing), check --expect, show a dry run unless --apply,
// save the before-state, apply one by one, then verify.
func runBulk(cmd *cobra.Command, args []string, spec bulkSpec) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if err := ctx.CheckPermission(spec.Tier); err != nil {
		return cliFail(outFmt, err)
	}
	applyStateSettings(cmd, ctx)
	targets, err := collectTargets(args)
	if err != nil {
		return cliFail(outFmt, err)
	}

	var steps []*step
	seen := map[string]bool{}
	for _, t := range targets {
		s, err := spec.Prepare(c, t)
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("%s: %w", t, err))
		}
		if seen[s.Key] {
			continue
		}
		seen[s.Key] = true
		steps = append(steps, s)
	}
	if bulkExpect >= 0 && len(steps) != bulkExpect {
		return cliFail(outFmt, &usageError{fmt.Sprintf("--expect %d but %d distinct target(s) resolved; nothing was changed", bulkExpect, len(steps))})
	}

	return runSteps(outFmt, spec.Name, spec.Cols, steps)
}

// runSteps is the part of every write after the targets are resolved: show a dry
// run unless --apply, save the before-state, apply one by one, record each
// outcome, then verify.
func runSteps(outFmt output.Format, name string, specCols []output.Column, steps []*step) error {
	if !flagApply {
		cols := []output.Column{{Key: "target", Header: "TARGET"}, {Key: "would", Header: "WOULD"}, {Key: "applied", Header: "APPLIED"}}
		rows := make([]map[string]any, len(steps))
		for i, s := range steps {
			rows[i] = map[string]any{"target": s.Key, "would": s.Desc, "applied": false, "dry_run": true}
		}
		return output.Print(outFmt, cols, rows)
	}

	var rec *stateRecorder
	if !bulkNoState {
		r, err := newStateRecorder(name, steps)
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("cannot save the before-state (use --no-state to skip): %w", err))
		}
		rec = r
		fmt.Fprintf(os.Stderr, "before-state saved: %s\n", r.path)
	}
	if rec != nil {
		defer func() {
			if _, err := os.Stat(rec.path); err == nil { // it may have been removed: Zabbix refused the first change
				pruneOldStates(filepath.Dir(rec.path), rec.path)
			}
		}()
	}
	// note records an outcome in the before-state file. A failure is returned: the record is
	// what says what happened, so a run that cannot keep it up to date must stop.
	note := func(i int, status string, err error) error {
		if rec == nil {
			return nil
		}
		return rec.mark(i, status, err)
	}
	var stateErr error

	var rows []map[string]any
	var applyErr error
	applied := steps[:0:0]
	unknown := false
	for i, s := range steps {
		if serr := note(i, stateSent, nil); serr != nil { // so a run killed mid-request does not look "not yet attempted"
			stateErr = serr
			break // nothing was sent for this target
		}
		err := s.Apply()
		if err != nil && !definitive(err) && s.Proves && s.Verify != nil {
			// The request may have been executed. Look before deciding.
			time.Sleep(s.VerifyAfter)
			if s.Verify() == nil {
				fmt.Fprintf(os.Stderr, "%s: %v; a re-read shows the change is in place\n", s.Key, err)
				err = nil
			}
		}
		if err != nil {
			if definitive(err) {
				_ = note(i, stateFailed, err)
				applyErr = fmt.Errorf("%s: %w (%d of %d applied)", s.Key, err, len(applied), len(steps))
			} else {
				_ = note(i, stateUnknown, err)
				unknown = true
				applyErr = &outcomeUnknownError{fmt.Sprintf("%s: %v. The request may or may not have been executed; check the target before retrying (%d of %d confirmed). The before-state file is kept: %s", s.Key, err, len(applied), len(steps), statePathOrNone(rec))}
			}
			break
		}
		applied = append(applied, s)
		if serr := note(i, stateApplied, nil); serr != nil {
			stateErr = serr
			break // this target is applied, but the record cannot say so: do not go on
		}
	}
	if stateErr != nil && applyErr == nil {
		applyErr = fmt.Errorf("the before-state file %s can no longer be updated (%v): stopped after %d of %d targets, the rest were NOT applied", statePathOrNone(rec), stateErr, len(applied), len(steps))
	}

	if applyErr != nil && !unknown && len(applied) == 0 && rec != nil {
		_ = os.Remove(rec.path) // Zabbix refused the first change, so there is nothing to undo
		fmt.Fprintln(os.Stderr, "nothing was changed; the before-state file was removed")
	}

	failedVerify := 0
	if !bulkNoVerify {
		var wait time.Duration
		for _, s := range applied {
			if s.Verify != nil && s.VerifyAfter > wait {
				wait = s.VerifyAfter
			}
		}
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	for _, s := range applied {
		row := map[string]any{}
		for k, v := range s.Row {
			row[k] = v
		}
		if !bulkNoVerify && s.Verify != nil {
			if err := s.Verify(); err != nil {
				failedVerify++
				row["verified"] = false
				row["verify_error"] = err.Error()
			} else {
				row["verified"] = true
			}
		}
		rows = append(rows, row)
	}
	cols := append(append([]output.Column{}, specCols...), output.Column{Key: "verified", Header: "VERIFIED"})
	if len(rows) > 0 {
		if err := output.Print(outFmt, cols, rows); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "%d of %d applied, %d verification failure(s)\n", len(applied), len(steps), failedVerify)
	if applyErr != nil {
		return cliFail(outFmt, applyErr)
	}
	if failedVerify > 0 {
		return cliPrintErr(outFmt, fmt.Sprintf("%d change(s) did not verify", failedVerify), output.ExitError)
	}
	return nil
}

func statePathOrNone(r *stateRecorder) string {
	if r == nil {
		return "(none was written: --no-state)"
	}
	return r.path
}
