package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	discHosts  []string
	discGroups []string
	lostClose  bool
)

var getDiscoveryCmd = &cobra.Command{
	Use:   "discovery",
	Short: "List low-level discovery rules with prototype counts and lost objects",
	Args:  cobra.NoArgs,
	RunE:  runGetDiscovery,
}

var getLostCmd = &cobra.Command{Use: "lost", Short: "List discovered objects that discovery no longer finds"}
var deleteLostCmd = &cobra.Command{Use: "lost", Short: "Delete lost discovered objects (only objects that are lost)"}

func lostCmd(kind string, del bool) *cobra.Command {
	plural := kind + "s"
	c := &cobra.Command{Use: plural + " [id...]", Args: cobra.ArbitraryArgs}
	if del {
		c.Short = "Delete lost discovered " + plural + " of the given hosts/groups, or by ID; never touches live ones"
		c.RunE = func(cmd *cobra.Command, args []string) error { return runDeleteLost(cmd, kind, args) }
		addBulkFlags(c)
		if kind == "trigger" {
			c.Flags().BoolVar(&lostClose, "close-problems", false, "close each trigger's open problems first (works where the trigger allows manual close)")
		}
	} else {
		c.Short = "List lost discovered " + plural
		c.Args = cobra.NoArgs
		c.Use = plural
		c.RunE = func(cmd *cobra.Command, args []string) error { return runGetLost(kind) }
	}
	c.Flags().StringArrayVar(&discHosts, "host", nil, "host name or ID (repeatable)")
	c.Flags().StringArrayVar(&discGroups, "group", nil, "host group name or ID (repeatable)")
	return c
}

func init() {
	getDiscoveryCmd.Flags().StringArrayVar(&discHosts, "host", nil, "host name or ID (repeatable)")
	getLostCmd.AddCommand(lostCmd("item", false), lostCmd("trigger", false))
	deleteLostCmd.AddCommand(lostCmd("item", true), lostCmd("trigger", true))
	getCmd.AddCommand(getDiscoveryCmd, getLostCmd)
	deleteCmd.AddCommand(deleteLostCmd)
}

func resolveScope(c *api.Client) (hostIDs, groupIDs []string, err error) {
	for _, h := range discHosts {
		id, err := resources.ResolveHostID(c, h)
		if err != nil {
			return nil, nil, err
		}
		hostIDs = append(hostIDs, id)
	}
	for _, g := range discGroups {
		id, err := resources.ResolveGroupID(c, g)
		if err != nil {
			return nil, nil, err
		}
		groupIDs = append(groupIDs, id)
	}
	return hostIDs, groupIDs, nil
}

func runGetDiscovery(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	hostIDs, _, err := resolveScope(c)
	if err != nil {
		return cliFail(outFmt, err)
	}
	rules, err := resources.GetDiscoveryRules(c, hostIDs)
	if err != nil {
		return cliFail(outFmt, err)
	}
	cols := []output.Column{
		{Key: "host", Header: "HOST"}, {Key: "name", Header: "RULE"}, {Key: "key_", Header: "KEY"}, {Key: "status", Header: "STATUS"},
		{Key: "item_prototypes", Header: "ITEM_PROTOS"}, {Key: "trigger_prototypes", Header: "TRIG_PROTOS"},
		{Key: "lost_items", Header: "LOST_ITEMS"}, {Key: "lost_triggers", Header: "LOST_TRIGS"},
	}
	rows := make([]map[string]any, len(rules))
	for i, r := range rules {
		status := "enabled"
		switch {
		case !r.Enabled:
			status = "disabled"
		case !r.Supported:
			status = "not supported"
		}
		rows[i] = map[string]any{
			"host": r.Host, "ruleid": r.RuleID, "name": r.Name, "key_": r.Key, "status": status, "error": r.Error, "interval": r.Interval,
			"item_prototypes": r.ItemPrototypes, "trigger_prototypes": r.TriggerPrototypes, "lost_items": r.LostItems, "lost_triggers": r.LostTriggers,
		}
	}
	return output.Print(outFmt, cols, rows)
}

func lostRows(objs []resources.LostObject) []map[string]any {
	rows := make([]map[string]any, len(objs))
	for i, o := range objs {
		rows[i] = map[string]any{
			"kind": o.Kind, "id": o.ID, "host": o.Host, "name": o.Name, "key_": o.Key, "ruleid": o.RuleID,
			"deletes_at": fmtUnix(o.DeletesAt),
		}
	}
	return rows
}

var lostCols = []output.Column{
	{Key: "id", Header: "ID"}, {Key: "host", Header: "HOST"}, {Key: "name", Header: "NAME"}, {Key: "deletes_at", Header: "DELETES_AT"},
}

func runGetLost(kind string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	hostIDs, groupIDs, err := resolveScope(c)
	if err != nil {
		return cliFail(outFmt, err)
	}
	objs, err := resources.GetLost(c, kind, hostIDs, groupIDs)
	if err != nil {
		return cliFail(outFmt, err)
	}
	return output.Print(outFmt, lostCols, lostRows(objs))
}

func runDeleteLost(cmd *cobra.Command, kind string, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliFail(outFmt, err)
	}
	if len(args) == 0 && len(discHosts) == 0 && len(discGroups) == 0 {
		return cliPrintErr(outFmt, "say what to clean up: --host or --group, or pass lost object IDs", output.ExitUsage)
	}
	hostIDs, groupIDs, err := resolveScope(c)
	if err != nil {
		return cliFail(outFmt, err)
	}
	lost, err := resources.GetLost(c, kind, hostIDs, groupIDs)
	if err != nil {
		return cliFail(outFmt, err)
	}
	if len(args) > 0 { // explicit IDs may be outside the scope; look them up broadly
		all, err := resources.GetLost(c, kind, nil, nil)
		if err != nil {
			return cliFail(outFmt, err)
		}
		lost = all
	}
	byID := map[string]resources.LostObject{}
	for _, o := range lost {
		byID[o.ID] = o
	}
	targets := args
	if len(targets) == 0 {
		for _, o := range lost {
			targets = append(targets, o.ID)
		}
	}
	if len(targets) == 0 {
		return output.Print(outFmt, lostCols, nil) // nothing is lost: nothing to do
	}
	spec := bulkSpec{
		Name: "delete-lost-" + kind, Tier: "write",
		Cols: []output.Column{{Key: "id", Header: "ID"}, {Key: "host", Header: "HOST"}, {Key: "name", Header: "NAME"}, {Key: "status", Header: "STATUS"}},
		Prepare: func(c *api.Client, id string) (*step, error) {
			o, ok := byID[id]
			if !ok {
				return nil, &usageError{fmt.Sprintf("%s %s is not a lost discovered %s; refusing to delete it", kind, id, kind)}
			}
			desc := fmt.Sprintf("delete lost %s %q on %s (id %s)", kind, o.Name, o.Host, o.ID)
			var events []string
			if kind == "trigger" {
				if events, err = resources.OpenProblemEventIDs(c, id); err != nil {
					return nil, err
				}
				if n := len(events); n > 0 {
					if lostClose {
						desc += fmt.Sprintf(" after closing %d open problem(s)", n)
					} else {
						desc += fmt.Sprintf(" with %d open problem(s) (add --close-problems to close them first)", n)
					}
				}
			}
			return &step{
				Key: o.ID, Desc: desc,
				Before: map[string]any{"kind": kind, "id": o.ID, "host": o.Host, "name": o.Name, "key": o.Key, "deletes_at": fmtUnix(o.DeletesAt), "open_problem_events": events},
				Apply: func() error {
					if kind == "trigger" && lostClose {
						for _, ev := range events {
							// Best effort: only triggers that allow manual close can be closed.
							_ = resources.AcknowledgeEvent(c, resources.AcknowledgeParams{EventID: ev, Action: resources.ActionAcknowledge, Close: true, Severity: -1})
						}
					}
					return resources.DeleteLost(c, kind, id)
				},
				Verify: func() error {
					left, err := resources.GetLost(c, kind, nil, nil)
					if err != nil {
						return err
					}
					for _, l := range left {
						if l.ID == id {
							return errors.New(strings.Title(kind) + " still exists") //nolint:staticcheck
						}
					}
					return nil
				},
				Row: map[string]any{"id": o.ID, "host": o.Host, "name": o.Name, "status": "deleted"},
			}, nil
		},
	}
	return runBulk(cmd, targets, spec)
}
