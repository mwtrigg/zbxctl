package cli

import (
	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	macroHosts  []string
	macroGlobal bool
	macroSearch string
	actionLimit int
)

var getMacrosCmd = &cobra.Command{
	Use:   "macros",
	Short: "List user macros of hosts (or global macros); secret values are never shown",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, outFmt, _, err := newClient()
		if err != nil {
			return cliFail(outFmt, err)
		}
		var ids []string
		for _, h := range macroHosts {
			id, err := resources.ResolveHostID(c, h)
			if err != nil {
				return cliFail(outFmt, err)
			}
			ids = append(ids, id)
		}
		ms, err := resources.GetMacros(c, ids, macroGlobal, macroSearch)
		if err != nil {
			return cliFail(outFmt, err)
		}
		cols := []output.Column{{Key: "host", Header: "HOST"}, {Key: "macro", Header: "MACRO"}, {Key: "value", Header: "VALUE"}, {Key: "type", Header: "TYPE"}, {Key: "description", Header: "DESCRIPTION"}}
		rows := make([]map[string]any, len(ms))
		for i, m := range ms {
			val := m.Value
			if m.Type == "1" {
				val = "******" // the API returns no value for secrets; never imply one
			}
			scope := m.Host
			if macroGlobal {
				scope = "(global)"
			}
			rows[i] = map[string]any{"host": scope, "macro": m.Macro, "value": val, "type": m.TypeLabel(), "description": m.Description}
		}
		return output.Print(outFmt, cols, rows)
	},
}

var getActionsCmd = &cobra.Command{
	Use:   "actions",
	Short: "List actions (trigger, discovery, autoregistration, internal, service)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, outFmt, ctx, err := newClient()
		if err != nil {
			return cliFail(outFmt, err)
		}
		as, err := resources.GetActions(c, effectiveLimit(actionLimit, ctx))
		if err != nil {
			return cliFail(outFmt, err)
		}
		src := map[string]string{"0": "trigger", "1": "discovery", "2": "autoregistration", "3": "internal", "4": "service"}
		cols := []output.Column{{Key: "actionid", Header: "ACTION_ID"}, {Key: "name", Header: "NAME"}, {Key: "event_source", Header: "SOURCE"}, {Key: "enabled", Header: "ENABLED"}, {Key: "conditions", Header: "CONDITIONS"}}
		rows := make([]map[string]any, len(as))
		for i, a := range as {
			rows[i] = map[string]any{"actionid": a.ActionID, "name": a.Name, "event_source": src[a.EventSource], "enabled": a.Enabled, "conditions": a.Conditions}
		}
		return output.Print(outFmt, cols, rows)
	},
}

func init() {
	f := getMacrosCmd.Flags()
	f.StringArrayVar(&macroHosts, "host", nil, "host name or ID (repeatable)")
	f.BoolVar(&macroGlobal, "global", false, "list global macros instead of host macros")
	f.StringVar(&macroSearch, "search", "", "substring match on the macro name")
	getActionsCmd.Flags().IntVar(&actionLimit, "limit", 0, "max results")
	getCmd.AddCommand(getMacrosCmd, getActionsCmd)
}
