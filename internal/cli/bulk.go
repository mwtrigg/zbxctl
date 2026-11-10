package cli

import (
	"encoding/json"
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
	Key         string         // canonical ID, used to drop duplicate targets
	Desc        string         // what will happen, e.g. `disable host "web-01"`
	Before      map[string]any // state captured before the change (saved to the state file)
	Apply       func() error
	Verify      func() error // optional: re-read and check; nil result means confirmed
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

func stateFilePath(name string) (string, error) {
	dir := bulkStateDir
	if dir == "" {
		var err error
		if dir, err = defaultStateDir(); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%s.json", name, time.Now().UTC().Format("20060102T150405Z"))), nil
}

func writeState(name string, steps []*step) (string, error) {
	path, err := stateFilePath(name)
	if err != nil {
		return "", err
	}
	items := make([]map[string]any, len(steps))
	for i, s := range steps {
		items[i] = map[string]any{"target": s.Key, "action": s.Desc, "before": s.Before}
	}
	b, _ := json.MarshalIndent(map[string]any{"command": name, "time": time.Now().UTC().Format(time.RFC3339), "items": items}, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return path, err
	}
	pruneOldStates(filepath.Dir(path), path)
	return path, nil
}

// pruneOldStates keeps the newest stateKeep before-state files in dir and
// deletes the rest. It never deletes justWritten, only touches files zbxctl
// wrote, says so on stderr when it deletes anything, and never fails the change
// that triggered it.
func pruneOldStates(dir, justWritten string) {
	if stateKeep <= 0 {
		return
	}
	files, err := readStateFiles(dir)
	if err != nil || len(files) <= stateKeep {
		return
	}
	removed := 0
	for _, f := range files[stateKeep:] { // files are newest first
		if f.Path == justWritten {
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

	if !flagApply {
		cols := []output.Column{{Key: "target", Header: "TARGET"}, {Key: "would", Header: "WOULD"}, {Key: "applied", Header: "APPLIED"}}
		rows := make([]map[string]any, len(steps))
		for i, s := range steps {
			rows[i] = map[string]any{"target": s.Key, "would": s.Desc, "applied": false, "dry_run": true}
		}
		return output.Print(outFmt, cols, rows)
	}

	statePath := ""
	if !bulkNoState {
		path, err := writeState(spec.Name, steps)
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("cannot save the before-state (use --no-state to skip): %w", err))
		}
		statePath = path
		fmt.Fprintf(os.Stderr, "before-state saved: %s\n", path)
	}

	var rows []map[string]any
	var applyErr error
	applied := steps[:0:0]
	for _, s := range steps {
		if err := s.Apply(); err != nil {
			applyErr = fmt.Errorf("%s: %w (%d of %d applied)", s.Key, err, len(applied), len(steps))
			break
		}
		applied = append(applied, s)
	}

	if applyErr != nil && len(applied) == 0 && statePath != "" {
		_ = os.Remove(statePath) // nothing changed, so there is nothing to undo
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
	cols := append(append([]output.Column{}, spec.Cols...), output.Column{Key: "verified", Header: "VERIFIED"})
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
