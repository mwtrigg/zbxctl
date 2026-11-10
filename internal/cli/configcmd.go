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
			Short: "Export a " + noun + "; prints to stdout unless --file is given",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return runExport(noun, args[0]) },
		}
		ex.Flags().StringVar(&exportFormat, "format", "yaml", "yaml | json | xml")
		ex.Flags().StringVar(&exportFile, "file", "", "write to this file instead of stdout")
		exportCmd.AddCommand(ex)

		im := &cobra.Command{
			Use:   noun + " <file>",
			Short: "Import a " + noun + " export file; dry run (shows the diff) unless --apply",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return runImport(noun, args[0], true) },
		}
		im.Flags().StringVar(&importFormat, "format", "", "yaml | json | xml (default: from the file extension)")
		importCmd.AddCommand(im)

		df := &cobra.Command{
			Use:   noun + " <file>",
			Short: "Show what importing this " + noun + " file would change",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return runImport(noun, args[0], false) },
		}
		df.Flags().StringVar(&importFormat, "format", "", "yaml | json | xml (default: from the file extension)")
		diffCmd.AddCommand(df)
	}
	rootCmd.AddCommand(exportCmd, importCmd, diffCmd)
}

func runExport(noun, ref string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if exportFormat != "yaml" && exportFormat != "json" && exportFormat != "xml" {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --format %q: use yaml, json or xml", exportFormat), output.ExitUsage)
	}
	var id, name string
	if noun == "host" {
		h, err := resources.GetHost(c, ref)
		if err != nil {
			return cliFail(outFmt, err)
		}
		id, name = h.HostID, h.Host
	} else {
		t, err := resources.GetTemplate(c, ref)
		if err != nil {
			return cliFail(outFmt, err)
		}
		id, name = t.TemplateID, t.Host
	}
	body, err := resources.ExportConfig(c, kindOf(noun), id, exportFormat)
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
	if err := os.WriteFile(exportFile, []byte(body), 0o644); err != nil {
		return cliFail(outFmt, fmt.Errorf("writing %s: %w", exportFile, err))
	}
	return output.Print(outFmt, []output.Column{{Key: "name", Header: "NAME"}, {Key: "file", Header: "FILE"}, {Key: "format", Header: "FORMAT"}},
		[]map[string]any{{"name": name, "file": exportFile, "format": exportFormat, "bytes": len(body)}})
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

// runImport shows the diff for a file; with apply (import only) it then imports.
func runImport(noun, path string, isImport bool) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if isImport {
		if err := ctx.CheckPermission("write"); err != nil {
			return cliFail(outFmt, err)
		}
	}
	format, err := formatFor(path, importFormat)
	if err != nil {
		return cliFail(outFmt, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cliFail(outFmt, fmt.Errorf("reading %s: %w", path, err))
	}
	raw, err := resources.CompareConfig(c, kindOf(noun), format, string(b))
	if err != nil {
		return cliFail(outFmt, err)
	}
	changes, err := resources.FlattenComparison(raw)
	if err != nil {
		return cliFail(outFmt, err)
	}
	rows := make([]map[string]any, len(changes))
	for i, ch := range changes {
		rows[i] = map[string]any{"path": ch.Path, "action": ch.Action, "fields": ch.Fields}
	}
	cols := []output.Column{{Key: "action", Header: "ACTION"}, {Key: "path", Header: "PATH"}, {Key: "fields", Header: "CHANGES"}}
	if !isImport || !flagApply {
		return output.Print(outFmt, cols, rows)
	}
	// Import regardless of what the comparison listed: importcompare does not
	// always report objects that would be newly created.
	if err := resources.ImportConfig(c, kindOf(noun), format, string(b)); err != nil {
		return cliFail(outFmt, err)
	}
	if len(rows) == 0 {
		rows = append(rows, map[string]any{"action": "imported", "path": path, "fields": []string{}})
	}
	for _, r := range rows {
		r["applied"] = true
	}
	return output.Print(outFmt, append(cols, output.Column{Key: "applied", Header: "APPLIED"}), rows)
}
