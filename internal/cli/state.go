package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/config"
	"github.com/mwtrigg/zbxctl/internal/output"
)

// The before-state files that --apply writes are an audit trail of every change
// zbxctl made. "state" lets you look at them and clear out old ones.

var (
	stateDirFlag    string
	stateCommand    string
	stateOlderThan  string
	stateLimit      int
	stateShowFilter string
	stateKeepFlag   = -1
)

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Look at and clean up the before-state files written by changes made with --apply",
	Long: `Every change made with --apply saves a before-state file (what the target looked like,
and what was done to it) unless that is turned off with before_state: false in the config,
ZBXCTL_BEFORE_STATE=false or --no-state. Together they are an audit trail of what zbxctl changed.

  zbxctl state list                    the files, newest first
  zbxctl state show <file>             what one file recorded
  zbxctl state prune --older-than 30d  delete old files (a dry run until --apply); --keep N keeps the newest N

The directory is --state-dir, else ZBXCTL_STATE_DIR, else state_dir in the config, else
~/.local/state/zbxctl. Only the newest 200 files are kept (state_keep in the config,
ZBXCTL_STATE_KEEP or --state-keep; 0 keeps everything); older ones are deleted as new ones are
written, and zbxctl says so when it does.`,
}

// resolveStateDir finds the directory without needing a Zabbix connection.
func resolveStateDir() (string, error) {
	if stateDirFlag != "" {
		return stateDirFlag, nil
	}
	if v := os.Getenv("ZBXCTL_STATE_DIR"); v != "" {
		return v, nil
	}
	if cfg, err := config.LoadConfigFile(); err == nil {
		name := flagContext
		if name == "" {
			name = os.Getenv("ZBXCTL_CONTEXT")
		}
		if name == "" {
			name = cfg.CurrentContext
		}
		if c, ok := cfg.Contexts[name]; ok && c.StateDir != "" {
			return c.StateDir, nil
		}
	}
	return defaultStateDir()
}

type stateFile struct {
	Path    string
	Name    string
	Time    time.Time
	Command string
	Items   int
	Actions []string
	Size    int64
}

// readStateFiles lists the before-state files in dir, newest first. Files that
// are not ours (no command and items) are left alone and not listed.
func readStateFiles(dir string) ([]stateFile, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []stateFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Command string           `json:"command"`
			Time    string           `json:"time"`
			Items   []map[string]any `json:"items"`
		}
		if json.Unmarshal(b, &doc) != nil || doc.Command == "" || doc.Items == nil {
			continue
		}
		t, _ := time.Parse(time.RFC3339, doc.Time)
		sf := stateFile{Path: path, Name: e.Name(), Time: t, Command: doc.Command, Items: len(doc.Items), Size: int64(len(b))}
		for _, it := range doc.Items {
			if a, ok := it["action"].(string); ok && len(sf.Actions) < 3 {
				sf.Actions = append(sf.Actions, a)
			}
		}
		out = append(out, sf)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

func stateRows(files []stateFile) []map[string]any {
	rows := make([]map[string]any, len(files))
	for i, f := range files {
		rows[i] = map[string]any{
			"file": f.Name, "time": f.Time.In(displayLoc).Format(time.RFC3339), "command": f.Command,
			"targets": f.Items, "first_actions": f.Actions, "bytes": f.Size, "path": f.Path,
		}
	}
	return rows
}

var stateCols = []output.Column{
	{Key: "time", Header: "TIME"}, {Key: "command", Header: "COMMAND"}, {Key: "targets", Header: "TARGETS"}, {Key: "file", Header: "FILE"},
}

func filterStateFiles(files []stateFile, command string, olderThan time.Duration) []stateFile {
	var out []stateFile
	for _, f := range files {
		if command != "" && f.Command != command {
			continue
		}
		if olderThan > 0 && time.Since(f.Time) < olderThan {
			continue
		}
		out = append(out, f)
	}
	return out
}

func init() {
	list := &cobra.Command{
		Use: "list", Short: "List before-state files, newest first", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			outFmt := output.Format(flagOutput)
			dir, err := resolveStateDir()
			if err != nil {
				return cliFail(outFmt, err)
			}
			files, err := readStateFiles(dir)
			if err != nil {
				return cliFail(outFmt, err)
			}
			var older time.Duration
			if stateOlderThan != "" {
				if older, err = parseWindow(stateOlderThan); err != nil {
					return cliPrintErr(outFmt, fmt.Sprintf("invalid --older-than %q: use a duration like 30d", stateOlderThan), output.ExitUsage)
				}
			}
			files = filterStateFiles(files, stateCommand, older)
			if stateLimit > 0 && len(files) > stateLimit {
				files = files[:stateLimit]
			}
			return output.Print(outFmt, stateCols, stateRows(files))
		},
	}
	list.Flags().StringVar(&stateCommand, "command", "", "only files written by this command, e.g. delete-host")
	list.Flags().StringVar(&stateOlderThan, "older-than", "", "only files older than this, e.g. 30d")
	list.Flags().IntVar(&stateLimit, "limit", 0, "max files")

	show := &cobra.Command{
		Use: "show <file>", Short: "Show what one before-state file recorded (a name from 'state list', or a path)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outFmt := output.Format(flagOutput)
			path := args[0]
			if !strings.Contains(path, "/") {
				dir, err := resolveStateDir()
				if err != nil {
					return cliFail(outFmt, err)
				}
				path = filepath.Join(dir, path)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return cliFail(outFmt, &usageError{fmt.Sprintf("cannot read %s: %v", path, err)})
			}
			var doc struct {
				Command string           `json:"command"`
				Time    string           `json:"time"`
				Items   []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(b, &doc); err != nil || doc.Command == "" {
				return cliFail(outFmt, &usageError{path + " is not a zbxctl before-state file"})
			}
			rows := make([]map[string]any, len(doc.Items))
			for i, it := range doc.Items {
				rows[i] = map[string]any{"target": it["target"], "action": it["action"], "before": it["before"], "command": doc.Command, "time": doc.Time}
			}
			return output.Print(outFmt, []output.Column{{Key: "target", Header: "TARGET"}, {Key: "action", Header: "ACTION"}}, rows)
		},
	}

	prune := &cobra.Command{
		Use: "prune", Short: "Delete old before-state files (a dry run unless --apply)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			outFmt := output.Format(flagOutput)
			if stateOlderThan == "" && stateKeepFlag < 0 {
				return cliPrintErr(outFmt, "say what to delete: --older-than 30d and/or --keep 200 (nothing is deleted without one)", output.ExitUsage)
			}
			var older time.Duration
			if stateOlderThan != "" {
				var err error
				older, err = parseWindow(stateOlderThan)
				if err != nil || older <= 0 {
					return cliPrintErr(outFmt, fmt.Sprintf("invalid --older-than %q: use a duration like 30d", stateOlderThan), output.ExitUsage)
				}
			}
			dir, err := resolveStateDir()
			if err != nil {
				return cliFail(outFmt, err)
			}
			all, err := readStateFiles(dir)
			if err != nil {
				return cliFail(outFmt, err)
			}
			victims := filterStateFiles(all, stateCommand, older)
			if stateKeepFlag >= 0 { // --keep N: also everything beyond the newest N
				beyond := map[string]bool{}
				if len(all) > stateKeepFlag {
					for _, f := range all[stateKeepFlag:] {
						beyond[f.Path] = true
					}
				}
				if stateOlderThan == "" {
					victims = nil
				}
				seen := map[string]bool{}
				for _, v := range victims {
					seen[v.Path] = true
				}
				for _, f := range all {
					if beyond[f.Path] && !seen[f.Path] && (stateCommand == "" || f.Command == stateCommand) {
						victims = append(victims, f)
					}
				}
				sort.SliceStable(victims, func(i, j int) bool { return victims[i].Time.After(victims[j].Time) })
			}
			if bulkExpect >= 0 && len(victims) != bulkExpect {
				return cliFail(outFmt, &usageError{fmt.Sprintf("--expect %d but %d file(s) match; nothing was deleted", bulkExpect, len(victims))})
			}
			rows := stateRows(victims)
			for _, r := range rows {
				r["deleted"] = false
			}
			if !flagApply {
				var why []string
				if stateOlderThan != "" {
					why = append(why, "older than "+stateOlderThan)
				}
				if stateKeepFlag >= 0 {
					why = append(why, fmt.Sprintf("beyond the newest %d", stateKeepFlag))
				}
				fmt.Fprintf(os.Stderr, "dry run: %d of %d file(s) in %s are %s; add --apply to delete them\n", len(victims), len(all), dir, strings.Join(why, " or "))
				return output.Print(outFmt, append(append([]output.Column{}, stateCols...), output.Column{Key: "deleted", Header: "DELETED"}), rows)
			}
			for i, v := range victims {
				if err := os.Remove(v.Path); err != nil {
					return cliFail(outFmt, fmt.Errorf("%s: %w (%d of %d deleted)", v.Name, err, i, len(victims)))
				}
				rows[i]["deleted"] = true
			}
			fmt.Fprintf(os.Stderr, "deleted %d file(s) from %s\n", len(victims), dir)
			return output.Print(outFmt, append(append([]output.Column{}, stateCols...), output.Column{Key: "deleted", Header: "DELETED"}), rows)
		},
	}
	prune.Flags().StringVar(&stateOlderThan, "older-than", "", "delete files older than this, e.g. 30d")
	prune.Flags().IntVar(&stateKeepFlag, "keep", -1, "delete everything beyond the newest N files")
	prune.Flags().StringVar(&stateCommand, "command", "", "only files written by this command")
	prune.Flags().IntVar(&bulkExpect, "expect", -1, "refuse to delete unless exactly N files match")

	stateCmd.PersistentFlags().StringVar(&stateDirFlag, "state-dir", "", "the before-state directory (default: ZBXCTL_STATE_DIR, the config's state_dir, or ~/.local/state/zbxctl)")
	stateCmd.AddCommand(list, show, prune)
	rootCmd.AddCommand(stateCmd)
}
