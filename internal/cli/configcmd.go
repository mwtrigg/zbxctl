package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	exportFormat string
	exportFile   string
	importFormat string
	importDir    string
	diffDir      string
	exportDir    string
	exportSearch string
	exportGroup  string
	exportAll    bool
)

var exportCmd = &cobra.Command{Use: "export", Short: "Export a template or host as yaml, json or xml"}
var importCmd = &cobra.Command{Use: "import", Short: "Import a template or host from a file (dry run shows the diff)"}
var diffCmd = &cobra.Command{Use: "diff", Short: "Show what importing a file would change (read-only)"}

func kindOf(noun string) string {
	if noun == "host" {
		return "hosts"
	}
	return "templates"
}

func init() {
	for _, noun := range []string{"template", "host"} {
		noun := noun
		ex := &cobra.Command{
			Use:   noun + " <id|name>",
			Short: "Export a " + noun + "; prints to stdout unless --file is given; --dir exports several, one file each",
			Args:  cobra.ArbitraryArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return runExport(noun, args) },
		}
		ex.Flags().StringVar(&exportFormat, "format", "yaml", "yaml | json | xml")
		ex.Flags().StringVar(&exportFile, "file", "", "write to this file instead of stdout")
		ex.Flags().StringVar(&exportDir, "dir", "", "write one file per object into this directory (name them, or use --search/--all"+map[bool]string{true: "/--group", false: ""}[noun == "host"]+")")
		ex.Flags().StringVar(&exportSearch, "search", "", "with --dir: every "+noun+" whose name contains this")
		ex.Flags().BoolVar(&exportAll, "all", false, "with --dir: every "+noun)
		if noun == "host" {
			ex.Flags().StringVar(&exportGroup, "group", "", "with --dir: every host in this host group")
		}
		ex.Flags().IntVar(&bulkExpect, "expect", -1, "with --dir: refuse to write unless exactly N objects match")
		exportCmd.AddCommand(ex)

		im := &cobra.Command{
			Use:   noun + " <file> | --dir <dir>",
			Short: "Import a " + noun + " export file (or every file in a directory); dry run (shows the diff) unless --apply; saves the current state of what it changes",
			Args:  cobra.MaximumNArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return runImport(noun, args, importDir, true) },
		}
		im.Flags().StringVar(&importFormat, "format", "", "yaml | json | xml (default: from the file extension)")
		im.Flags().StringVar(&importDir, "dir", "", "import every .yaml/.yml/.json/.xml file in this directory")
		im.Flags().IntVar(&bulkExpect, "expect", -1, "refuse to run unless exactly N files are found")
		im.Flags().StringVar(&bulkStateDir, "state-dir", "", "where the before-state file is written (default ~/.local/state/zbxctl)")
		im.Flags().BoolVar(&bulkNoState, "no-state", false, "do not save the current state of what the import changes")
		importCmd.AddCommand(im)

		df := &cobra.Command{
			Use:   noun + " <file> | --dir <dir>",
			Short: "Show what importing this " + noun + " file (or every file in a directory) would change",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if diffDir != "" {
					if len(args) > 0 {
						return &usageError{"give a file or --dir, not both"}
					}
					return runDiffDir(noun, diffDir)
				}
				if len(args) != 1 {
					return &usageError{"give a file or --dir <directory>"}
				}
				return runImport(noun, args, "", false)
			},
		}
		df.Flags().StringVar(&importFormat, "format", "", "yaml | json | xml (default: from the file extension)")
		df.Flags().StringVar(&diffDir, "dir", "", "compare every .yaml/.yml/.json/.xml file in this directory against the live server")
		diffCmd.AddCommand(df)
	}
	rootCmd.AddCommand(exportCmd, importCmd, diffCmd)
}

func runExport(noun string, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if exportFormat != "yaml" && exportFormat != "json" && exportFormat != "xml" {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --format %q: use yaml, json or xml", exportFormat), output.ExitUsage)
	}
	type target struct{ id, name string }
	resolve := func(ref string) (target, error) {
		if noun == "host" {
			h, err := resources.GetHost(c, ref)
			if err != nil {
				return target{}, err
			}
			return target{h.HostID, h.Host}, nil
		}
		t, err := resources.GetTemplate(c, ref)
		if err != nil {
			return target{}, err
		}
		return target{t.TemplateID, t.Host}, nil
	}

	bulk := exportDir != ""
	if !bulk {
		if len(args) != 1 {
			return cliFail(outFmt, &usageError{"give one " + noun + ", or --dir to export several"})
		}
		t, err := resolve(args[0])
		if err != nil {
			return cliFail(outFmt, err)
		}
		body, err := resources.ExportConfig(c, kindOf(noun), t.id, exportFormat)
		if err != nil {
			return cliFail(outFmt, err)
		}
		if exportFile == "" {
			fmt.Print(body)
			if !strings.HasSuffix(body, "\n") {
				fmt.Println()
			}
			return nil
		}
		if err := os.WriteFile(exportFile, []byte(body), 0o600); err != nil {
			return cliFail(outFmt, fmt.Errorf("writing %s: %w", exportFile, err))
		}
		return output.Print(outFmt, []output.Column{{Key: "name", Header: "NAME"}, {Key: "file", Header: "FILE"}, {Key: "format", Header: "FORMAT"}},
			[]map[string]any{{"name": t.name, "file": exportFile, "format": exportFormat, "bytes": len(body)}})
	}

	// --dir: one file per object, chosen by arguments, --search, --group (hosts) or --all.
	if exportFile != "" {
		return cliFail(outFmt, &usageError{"--file and --dir are exclusive"})
	}
	var targets []target
	switch {
	case len(args) > 0:
		for _, a := range args {
			t, err := resolve(a)
			if err != nil {
				return cliFail(outFmt, err)
			}
			targets = append(targets, t)
		}
	case noun == "template" && (exportAll || exportSearch != ""):
		ts, err := resources.GetTemplates(c, resources.TemplateFilter{Search: exportSearch})
		if err != nil {
			return cliFail(outFmt, err)
		}
		for _, t := range ts {
			targets = append(targets, target{t.TemplateID, t.Host})
		}
	case noun == "host" && (exportAll || exportSearch != "" || exportGroup != ""):
		f := resources.HostFilter{Search: exportSearch}
		if exportGroup != "" {
			g, err := resources.GetGroupByName(c, exportGroup)
			if err != nil {
				return cliFail(outFmt, err)
			}
			f.GroupIDs = []string{g.GroupID}
		}
		hs, err := resources.GetHosts(c, f)
		if err != nil {
			return cliFail(outFmt, err)
		}
		for _, h := range hs {
			targets = append(targets, target{h.HostID, h.Host})
		}
	default:
		return cliFail(outFmt, &usageError{"with --dir, name the " + noun + "s, or use --search, --all" + map[bool]string{true: " or --group", false: ""}[noun == "host"]})
	}
	if bulkExpect >= 0 && len(targets) != bulkExpect {
		return cliFail(outFmt, &usageError{fmt.Sprintf("--expect %d but %d %s(s) matched; nothing was written", bulkExpect, len(targets), noun)})
	}
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return cliFail(outFmt, err)
	}
	rows := make([]map[string]any, 0, len(targets))
	for _, t := range targets {
		body, err := resources.ExportConfig(c, kindOf(noun), t.id, exportFormat)
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("%s: %w", t.name, err))
		}
		file := filepath.Join(exportDir, safeFileName(t.name)+"."+exportFormat)
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			return cliFail(outFmt, err)
		}
		rows = append(rows, map[string]any{"name": t.name, "file": file, "format": exportFormat, "bytes": len(body)})
	}
	return output.Print(outFmt, []output.Column{{Key: "name", Header: "NAME"}, {Key: "file", Header: "FILE"}, {Key: "bytes", Header: "BYTES"}}, rows)
}

// safeFileName keeps an object name usable as a file name.
func safeFileName(n string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == 0 {
			return '_'
		}
		return r
	}, n)
}

func formatFor(path, override string) (string, error) {
	f := override
	if f == "" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml":
			f = "yaml"
		case ".json":
			f = "json"
		case ".xml":
			f = "xml"
		}
	}
	if f != "yaml" && f != "json" && f != "xml" {
		return "", &usageError{"cannot tell the file format: use --format yaml|json|xml"}
	}
	return f, nil
}

// importFiles lists the export files to look at: the one given, or every export
// file in --dir (sorted by name).
func importFiles(args []string, dir string) ([]string, error) {
	if dir == "" {
		if len(args) != 1 {
			return nil, &usageError{"give a file or --dir <directory>"}
		}
		return args, nil
	}
	if len(args) > 0 {
		return nil, &usageError{"give a file or --dir, not both"}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, err := formatFor(e.Name(), ""); err == nil {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	if len(files) == 0 {
		return nil, &usageError{"no .yaml, .yml, .json or .xml files in " + dir}
	}
	return files, nil
}

// topObjects returns the top-level objects (templates/hosts) touched by the
// changes: updated ones (anything below them changed or they changed) and created ones.
func topObjects(changes []resources.ConfigChange, typ string) (updated, created []string) {
	seenU, seenC := map[string]bool{}, map[string]bool{}
	for _, ch := range changes {
		parts := strings.Split(ch.Path, "/")
		if len(parts) < 2 || parts[0] != typ {
			continue
		}
		name := parts[1]
		if len(parts) == 2 && ch.Action == "created" {
			if !seenC[name] {
				seenC[name] = true
				created = append(created, name)
			}
			continue
		}
		if !seenU[name] {
			seenU[name] = true
			updated = append(updated, name)
		}
	}
	return updated, created
}

// runImport shows what a file (or every file in --dir) would change; with
// apply (import only) it imports, after saving the current state of every
// object it will modify so the change can be restored.
func runImport(noun string, args []string, dir string, isImport bool) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if isImport {
		if err := ctx.CheckPermission("write"); err != nil {
			return cliFail(outFmt, err)
		}
	}
	applyStateSettings(rootCmd, ctx)
	files, err := importFiles(args, dir)
	if err != nil {
		return cliFail(outFmt, err)
	}
	if bulkExpect >= 0 && len(files) != bulkExpect {
		return cliFail(outFmt, &usageError{fmt.Sprintf("--expect %d but %d file(s) found; nothing was changed", bulkExpect, len(files))})
	}
	typ := kindOf(noun)

	type job struct {
		path, format, source string
		changes              []resources.ConfigChange
	}
	var jobs []job
	for _, path := range files { // look at everything first, so a bad file changes nothing
		format, err := formatFor(path, importFormat)
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("%s: %w", path, err))
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("reading %s: %w", path, err))
		}
		raw, err := resources.CompareConfig(c, typ, format, string(b))
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("%s: %w", path, err))
		}
		changes, err := resources.FlattenComparison(raw)
		if err != nil {
			return cliFail(outFmt, err)
		}
		jobs = append(jobs, job{path, format, string(b), changes})
	}

	var rows []map[string]any
	for _, j := range jobs {
		for _, ch := range j.changes {
			rows = append(rows, map[string]any{"file": j.path, "path": ch.Path, "action": ch.Action, "fields": ch.Fields})
		}
	}
	cols := []output.Column{{Key: "file", Header: "FILE"}, {Key: "action", Header: "ACTION"}, {Key: "path", Header: "PATH"}, {Key: "fields", Header: "CHANGES"}}
	if len(files) == 1 {
		cols = cols[1:]
	}
	if !isImport || !flagApply {
		return output.Print(outFmt, cols, rows)
	}

	// Save what is about to be overwritten.
	if !bulkNoState {
		var steps []*step
		for _, j := range jobs {
			updated, created := topObjects(j.changes, typ)
			before := map[string]any{"file": j.path, "created": created}
			exports := map[string]string{}
			for _, name := range updated {
				var id string
				if noun == "host" {
					h, err := resources.GetHost(c, name)
					if err != nil {
						return cliFail(outFmt, fmt.Errorf("cannot save the current state of %s %q before importing (use --no-state to skip): %w", noun, name, err))
					}
					id = h.HostID
				} else {
					t, err := resources.GetTemplate(c, name)
					if err != nil {
						return cliFail(outFmt, fmt.Errorf("cannot save the current state of %s %q before importing (use --no-state to skip): %w", noun, name, err))
					}
					id = t.TemplateID
				}
				body, err := resources.ExportConfig(c, typ, id, "yaml")
				if err != nil {
					return cliFail(outFmt, err)
				}
				exports[name] = body
			}
			before["previous_export_yaml"] = exports
			steps = append(steps, &step{Key: j.path, Desc: "import " + j.path, Before: before})
		}
		path, err := writeState("import-"+noun, steps)
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("cannot save the before-state (use --no-state to skip): %w", err))
		}
		fmt.Fprintf(os.Stderr, "before-state saved: %s\n", path)
	}

	var out []map[string]any
	for i, j := range jobs {
		// Import regardless of what the comparison listed: importcompare does not
		// always report objects that would be newly created.
		if err := resources.ImportConfig(c, typ, j.format, j.source); err != nil {
			return cliFail(outFmt, fmt.Errorf("%s: %w (%d of %d files imported)", j.path, err, i, len(jobs)))
		}
		if len(j.changes) == 0 {
			out = append(out, map[string]any{"file": j.path, "action": "imported", "path": j.path, "fields": []string{}, "applied": true})
		}
		for _, ch := range j.changes {
			out = append(out, map[string]any{"file": j.path, "path": ch.Path, "action": ch.Action, "fields": ch.Fields, "applied": true})
		}
	}
	return output.Print(outFmt, append(cols, output.Column{Key: "applied", Header: "APPLIED"}), out)
}

// runDiffDir compares every export file in dir with the live server and prints
// one flat list; a file with no differences contributes no rows.
func runDiffDir(noun, dir string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return cliFail(outFmt, fmt.Errorf("reading %s: %w", dir, err))
	}
	var rows []map[string]any
	files := 0
	for _, e := range entries { // os.ReadDir is sorted by name
		if e.IsDir() {
			continue
		}
		format, err := formatFor(e.Name(), "")
		if err != nil {
			continue // not an export file
		}
		files++
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return cliFail(outFmt, err)
		}
		raw, err := resources.CompareConfig(c, kindOf(noun), format, string(b))
		if err != nil {
			return cliFail(outFmt, fmt.Errorf("%s: %w", e.Name(), err))
		}
		changes, err := resources.FlattenComparison(raw)
		if err != nil {
			return cliFail(outFmt, err)
		}
		for _, ch := range changes {
			rows = append(rows, map[string]any{"file": e.Name(), "action": ch.Action, "path": ch.Path, "fields": ch.Fields})
		}
	}
	if files == 0 {
		return cliPrintErr(outFmt, "no .yaml, .yml, .json or .xml files in "+dir, output.ExitUsage)
	}
	return output.Print(outFmt, []output.Column{{Key: "file", Header: "FILE"}, {Key: "action", Header: "ACTION"}, {Key: "path", Header: "PATH"}, {Key: "fields", Header: "CHANGES"}}, rows)
}
