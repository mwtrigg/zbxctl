package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"codeberg.org/mwtrigg/zbxctl/internal/output"
)

var (
	flagOutput   string
	flagContext  string
	flagYes      bool
	flagInsecure bool
)

// cliErr is a sentinel error that carries a desired process exit code.
// Cobra won't print it (SilenceErrors is set); Execute() reads the code.
type cliErr struct{ code int }

func (e *cliErr) Error() string { return "" }

// cliPrintErr prints a structured error to stderr and returns a cliErr so the
// process exits with the correct code.
func cliPrintErr(outFmt output.Format, msg string, code int) error {
	output.PrintError(outFmt, msg, code)
	return &cliErr{code: code}
}

// rootCmd is the base command.
var rootCmd = &cobra.Command{
	Use:   "zbxctl",
	Short: "zbxctl — Zabbix CLI for agents and operators",
	Long: `zbxctl is a CLI for managing Zabbix 7.0+ without the web UI.

Single static binary. Machine-readable output. Structured errors.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command and returns an exit code.
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		var ce *cliErr
		if errors.As(err, &ce) {
			return ce.code
		}
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return output.ExitError
	}
	return output.ExitSuccess
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o",
		string(output.DefaultFormat()), "output format: table|json|jsonl")
	rootCmd.PersistentFlags().StringVarP(&flagContext, "context", "c",
		"", "override current context")
	rootCmd.PersistentFlags().BoolVarP(&flagYes, "yes", "y",
		false, "skip confirmation prompts")
	rootCmd.PersistentFlags().BoolVar(&flagInsecure, "insecure",
		false, "disable TLS verification")

	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(contextCmd)
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(enableCmd)
	rootCmd.AddCommand(disableCmd)
	rootCmd.AddCommand(acknowledgeCmd)
	rootCmd.AddCommand(maintenanceCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(diagnoseCmd)
	rootCmd.AddCommand(llmCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(versionCmd)
}
