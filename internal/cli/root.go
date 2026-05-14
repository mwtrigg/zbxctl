package cli

import (
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
}
