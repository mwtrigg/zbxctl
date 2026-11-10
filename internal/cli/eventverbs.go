package cli

import (
	"github.com/spf13/cobra"
)

// The four things you can do to a problem event are separate verbs:
//
//	acknowledge / unacknowledge   mark that someone has seen it (and optionally close it)
//	suppress / unsuppress         hide it from the problem list, for a while or indefinitely
//
// They share one implementation (runAcknowledge); the verb picks the action.
func eventVerb(use, short string, set func(), withUntil bool) *cobra.Command {
	c := &cobra.Command{
		Use: use + " <event-id>...", Short: short, Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			set()
			return runAcknowledge(cmd, args)
		},
	}
	c.Flags().StringVar(&ackMessage, "message", "", "message added to the problem's history")
	if withUntil {
		c.Flags().StringVar(&ackSuppressUntil, "until", "", "suppress until this time: unix, RFC3339, YYYY-MM-DD, or from now like 24h, 7d (default: indefinitely)")
	}
	addBulkFlags(c)
	return c
}

func init() {
	rootCmd.AddCommand(
		eventVerb("unacknowledge", "Remove the acknowledgement of problem events", func() { ackUnack = true }, false),
		eventVerb("suppress", "Suppress problem events: hide them from the problem list until a time, or indefinitely", func() { ackSuppress = true }, true),
		eventVerb("unsuppress", "Lift the suppression of problem events", func() { ackUnsuppress = true }, false),
	)
}
