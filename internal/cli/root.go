package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	flagOutput           string
	flagContext          string
	flagYes              bool
	flagApply            bool
	flagFilter           []string
	flagField            string
	flagInsecure         bool
	flagRaw              bool
	flagTimeout          time.Duration
	flagRetries          int
	flagTZ               string
	flagAllowUnsupported bool
	flagShowSecrets      bool
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
	Long: `zbxctl is a CLI for managing Zabbix 7.4 and newer without the web UI.

Single static binary. Machine-readable output. Structured errors.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := enforceMCPPolicy(cmd, args); err != nil {
			return err
		}
		output.Raw = flagRaw
		api.Timeout, api.Retries = flagTimeout, flagRetries
		if err := setDisplayZone(flagTZ); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
		output.Field = flagField
		if flagYes {
			flagApply = true // --yes is an alias for --apply
		}
		output.Command = strings.TrimPrefix(cmd.CommandPath(), "zbxctl ")
		return nil
	},
}

// enforceMCPPolicy makes a process started by the MCP server refuse the flags and
// values the server never offers, whatever way they arrived.
func enforceMCPPolicy(cmd *cobra.Command, args []string) error {
	if os.Getenv("ZBXCTL_FROM_MCP") == "" {
		return nil
	}
	var bad []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if mcpChildDenied(f.Name) {
			bad = append(bad, "--"+f.Name)
		}
	})
	if len(bad) > 0 {
		return &usageError{msg: "not allowed over MCP: " + strings.Join(bad, ", ")}
	}
	if cmd.CommandPath() == "zbxctl state show" {
		for _, a := range args {
			if strings.ContainsAny(a, `/\`) {
				return &usageError{msg: "state show takes a file name, not a path, over MCP"}
			}
		}
	}
	return nil
}

// usageError marks a bad flag or argument (exit code 5).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// classify maps an error to an exit code.
func classify(err error) int {
	var nfe *resources.NotFoundError
	var ue *usageError
	var rpc *api.RPCError
	var fe *output.FieldError
	var uv *api.UnsupportedVersionError
	var uo *outcomeUnknownError
	switch {
	case errors.As(err, &uo):
		return output.ExitUnknown
	case errors.As(err, &uv):
		return output.ExitVersion
	case errors.As(err, &fe):
		return output.ExitUsage
	case isCobraUsage(err):
		return output.ExitUsage
	case errors.As(err, &ue):
		return output.ExitUsage
	case errors.As(err, &nfe):
		return output.ExitNotFound
	case errors.Is(err, api.ErrConnect):
		return output.ExitConnect
	case errors.Is(err, api.ErrAuth):
		return output.ExitAuthError
	case errors.As(err, &rpc) && rpc.IsAuth():
		return output.ExitAuthError
	}
	return output.ExitError
}

// isCobraUsage recognises cobra's own flag/argument/command errors.
func isCobraUsage(err error) bool {
	m := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "accepts ", "requires at least", "required flag", "invalid argument", "flag needs an argument"} {
		if strings.HasPrefix(m, p) || strings.Contains(m, p) && strings.HasPrefix(m, "unknown") {
			return true
		}
	}
	return false
}

// cliFail prints err as a structured error with the exit code it classifies to.
func cliFail(outFmt output.Format, err error) error {
	return cliPrintErr(outFmt, err.Error(), classify(err))
}

// Execute runs the root command and returns an exit code.
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		var ce *cliErr
		if errors.As(err, &ce) {
			return ce.code
		}
		code := classify(err)
		output.PrintError(output.Format(flagOutput), err.Error(), code)
		return code
	}
	return output.ExitSuccess
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o",
		string(output.DefaultFormat()), "output format: json|jsonl|table|csv")
	rootCmd.PersistentFlags().StringVarP(&flagContext, "context", "c",
		"", "override current context")
	rootCmd.PersistentFlags().BoolVar(&flagApply, "apply", false,
		"execute state-changing commands (default is a dry run that changes nothing)")
	rootCmd.PersistentFlags().BoolVarP(&flagYes, "yes", "y",
		false, "alias for --apply; also skips the upgrade prompt")
	rootCmd.PersistentFlags().StringArrayVar(&flagFilter, "filter", nil,
		"exact-match API filter KEY=VALUE for list commands (repeatable; a repeated KEY ORs its values)")
	rootCmd.PersistentFlags().StringVar(&flagField, "field", "",
		"print only this field of each result, one value per line (lists print one element per line)")
	rootCmd.PersistentFlags().DurationVar(&flagTimeout, "timeout", 180*time.Second, "timeout of each API request")
	rootCmd.PersistentFlags().IntVar(&flagRetries, "retries", 2, "retries of read-only calls after a connection error or 502/503/504 (writes are never retried)")
	rootCmd.PersistentFlags().StringVar(&flagTZ, "tz", os.Getenv("ZBXCTL_TZ"), "time zone for displayed times: an IANA name like America/New_York, or local (default UTC)")
	rootCmd.PersistentFlags().BoolVar(&flagAllowUnsupported, "allow-unsupported", os.Getenv("ZBXCTL_ALLOW_UNSUPPORTED") != "", "try a Zabbix server older than zbxctl supports (7.4)")
	rootCmd.PersistentFlags().BoolVar(&flagRaw, "raw", false,
		"include raw API values (*_raw, *_unix) next to the readable ones")
	rootCmd.PersistentFlags().BoolVar(&flagShowSecrets, "show-secrets", false,
		"show secret values (SNMP communities, passwords in raw API results) instead of ******")
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
