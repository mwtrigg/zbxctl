package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/config"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage configuration contexts",
}

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available contexts",
	RunE:  runContextList,
}

var contextUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the active context",
	Args:  cobra.ExactArgs(1),
	RunE:  runContextUse,
}

func init() {
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextUseCmd)
}

func runContextList(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfigFile()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "name", Header: "NAME"},
		{Key: "url", Header: "URL"},
		{Key: "current", Header: "CURRENT"},
	}

	var rows []map[string]any
	for name, ctx := range cfg.Contexts {
		current := ""
		if name == cfg.CurrentContext {
			current = "*"
		}
		rows = append(rows, map[string]any{
			"name":    name,
			"url":     ctx.URL,
			"current": current,
		})
	}

	return output.Print(output.Format(flagOutput), cols, rows)
}

func runContextUse(cmd *cobra.Command, args []string) error {
	name := args[0]

	if err := config.WriteCurrentContext(name); err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}

	fmt.Fprintf(os.Stdout, "switched to context %q\n", name)
	return nil
}
