package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/demo"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Serve a made-up Zabbix API on localhost to try zbxctl without a server",
	Long: `Serves a small made-up Zabbix API on this machine and prints the variables that
point zbxctl at it. Everything in it is invented (hosts under example.com): six
hosts and seven open problems, one of which can never recover.

  zbxctl demo &
  export ZBXCTL_URL=http://127.0.0.1:8044 ZBXCTL_API_TOKEN=demo ZBXCTL_PERMISSIONS=write
  zbxctl get problems --group-by name -o table
  zbxctl diagnose trigger 4741          # a problem that can never recover, and why
  zbxctl disable host web-03            # a dry run
  zbxctl disable host web-03 --apply

Changes are kept in memory until it stops (Ctrl-C). It answers the calls these
commands make, not the whole API: a method it does not know gets an empty list,
and --verbose prints which methods were asked for.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, _ := cmd.Flags().GetString("addr")
		verbose, _ := cmd.Flags().GetBool("verbose")
		out := cmd.ErrOrStderr() // stdout stays free: nothing here is a result
		var log io.Writer
		if verbose {
			log = out
		}
		url, stop, err := demo.Start(addr, log)
		if err != nil {
			return cliPrintErr(output.Format(flagOutput), "cannot start the demo: "+err.Error(), output.ExitUsage)
		}
		defer stop()
		fmt.Fprintf(out, "demo Zabbix API on %s; made-up data, kept in memory\n\n", url)
		fmt.Fprintf(out, "  export ZBXCTL_URL=%s ZBXCTL_API_TOKEN=%s ZBXCTL_PERMISSIONS=write\n\n", url, demo.Token)
		fmt.Fprintln(out, "then, in the same shell or another one:")
		fmt.Fprintln(out, "  zbxctl get problems --group-by name -o table")
		fmt.Fprintln(out, "  zbxctl diagnose trigger 4741")
		fmt.Fprintln(out, "  zbxctl disable host web-03")
		fmt.Fprintln(out, "\nCtrl-C stops it.")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sig)
		select {
		case <-sig:
		case <-cmd.Context().Done():
		}
		return nil
	},
}

func init() {
	demoCmd.Flags().String("addr", "127.0.0.1:8044", "address to listen on (this machine only by default)")
	demoCmd.Flags().Bool("verbose", false, "print every API method that is called")
	rootCmd.AddCommand(demoCmd)
}
