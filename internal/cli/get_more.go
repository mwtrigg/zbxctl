package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"sort"
	"strings"

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

var getProxiesCmd = &cobra.Command{
	Use:   "proxies",
	Short: "List proxies: mode, last seen, version, compatibility and how many hosts each monitors",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, outFmt, _, err := newClient()
		if err != nil {
			return cliFail(outFmt, err)
		}
		ps, err := resources.GetProxies(c)
		if err != nil {
			return cliFail(outFmt, err)
		}
		mode := map[string]string{"0": "active", "1": "passive"}
		compat := map[string]string{"0": "undefined", "1": "current", "2": "outdated", "3": "unsupported"}
		state := map[string]string{"0": "unknown", "1": "offline", "2": "online"}
		cols := []output.Column{{Key: "proxyid", Header: "PROXY_ID"}, {Key: "name", Header: "NAME"}, {Key: "mode", Header: "MODE"}, {Key: "state", Header: "STATE"}, {Key: "last_seen", Header: "LAST_SEEN"}, {Key: "version", Header: "VERSION"}, {Key: "compatibility", Header: "COMPAT"}, {Key: "hosts", Header: "HOSTS"}}
		rows := make([]map[string]any, len(ps))
		for i, p := range ps {
			rows[i] = map[string]any{"proxyid": p.ID, "name": p.Name, "mode": mode[p.Mode], "address": p.Address, "state": state[p.State], "last_seen": fmtUnix(p.LastAccess),
				"version": resources.VersionLabel(p.Version), "compatibility": compat[p.Compatibility], "hosts": p.Hosts}
		}
		return output.Print(outFmt, cols, rows)
	},
}

var getProxyGroupsCmd = &cobra.Command{
	Use:   "proxy-groups",
	Short: "List proxy groups with their state and proxy count",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, outFmt, _, err := newClient()
		if err != nil {
			return cliFail(outFmt, err)
		}
		gs, err := resources.GetProxyGroups(c)
		if err != nil {
			return cliFail(outFmt, err)
		}
		state := map[string]string{"0": "unknown", "1": "offline", "2": "recovering", "3": "online", "4": "degrading"}
		cols := []output.Column{{Key: "proxy_groupid", Header: "GROUP_ID"}, {Key: "name", Header: "NAME"}, {Key: "state", Header: "STATE"}, {Key: "proxies", Header: "PROXIES"}, {Key: "min_online", Header: "MIN_ONLINE"}, {Key: "failover_delay", Header: "FAILOVER"}}
		rows := make([]map[string]any, len(gs))
		for i, g := range gs {
			rows[i] = map[string]any{"proxy_groupid": g.ID, "name": g.Name, "state": state[g.State], "proxies": g.Proxies, "min_online": g.MinOnline, "failover_delay": g.FailoverDelay}
		}
		return output.Print(outFmt, cols, rows)
	},
}

func init() { getCmd.AddCommand(getProxiesCmd, getProxyGroupsCmd) }

var (
	alUsers   []string
	alActions []string
	alRes     []string
	alSince   string
	alUntil   string
	alSearch  string
	alLimit   int
	alDetails bool
)

func codesFor(names []string, table map[string]string, what string) ([]string, error) {
	var out []string
	for _, n := range names {
		found := false
		for code, label := range table {
			if label == n || code == n {
				out = append(out, code)
				found = true
			}
		}
		if !found {
			var ks []string
			for _, l := range table {
				ks = append(ks, l)
			}
			sort.Strings(ks)
			return nil, &usageError{fmt.Sprintf("unknown %s %q: use %s, or a numeric code", what, n, strings.Join(ks, ", "))}
		}
	}
	return out, nil
}

var getAuditLogCmd = &cobra.Command{
	Use:   "auditlog",
	Short: "Who changed what, when: the Zabbix audit log, newest first",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, outFmt, ctx, err := newClient()
		if err != nil {
			return cliFail(outFmt, err)
		}
		q := resources.AuditQuery{Users: alUsers, Search: alSearch, Limit: effectiveLimit(alLimit, ctx)}
		if q.ActionCodes, err = codesFor(alActions, resources.AuditActions, "action"); err != nil {
			return cliFail(outFmt, err)
		}
		if q.ResourceCodes, err = codesFor(alRes, resources.AuditResources, "resource"); err != nil {
			return cliFail(outFmt, err)
		}
		if alSince != "" {
			if q.From, err = parseTimestamp(alSince); err != nil {
				return cliFail(outFmt, err)
			}
		}
		if alUntil != "" {
			if q.Till, err = parseTimestamp(alUntil); err != nil {
				return cliFail(outFmt, err)
			}
		}
		es, err := resources.GetAuditLog(c, q)
		if err != nil {
			return cliFail(outFmt, err)
		}
		cols := []output.Column{{Key: "time", Header: "TIME"}, {Key: "user", Header: "USER"}, {Key: "action", Header: "ACTION"}, {Key: "resource", Header: "RESOURCE"}, {Key: "name", Header: "NAME"}, {Key: "ip", Header: "IP"}}
		rows := make([]map[string]any, len(es))
		for i, e := range es {
			act, ok := resources.AuditActions[e.Action]
			if !ok {
				act = "action " + e.Action
			}
			res, ok := resources.AuditResources[e.ResourceType]
			if !ok {
				res = "resource " + e.ResourceType
			}
			row := map[string]any{"time": fmtUnix(e.Clock), "user": e.User, "ip": e.IP, "action": act, "resource": res, "name": e.ResourceName, "resourceid": e.ResourceID}
			if alDetails {
				row["details"] = e.Details
			}
			rows[i] = row
		}
		if alDetails {
			cols = append(cols, output.Column{Key: "details", Header: "DETAILS"})
		}
		return output.Print(outFmt, cols, rows)
	},
}

func init() {
	f := getAuditLogCmd.Flags()
	f.StringArrayVar(&alUsers, "user", nil, "only this username (repeatable)")
	f.StringArrayVar(&alActions, "action", nil, "add|update|delete|login|logout|execute|push, or a numeric code (repeatable)")
	f.StringArrayVar(&alRes, "resource", nil, "user|usergroup|host|hostgroup|template|item|trigger|macro|maintenance, or a numeric code (repeatable)")
	f.StringVar(&alSince, "since", "", "unix, RFC3339, YYYY-MM-DD, or 24h/7d")
	f.StringVar(&alUntil, "until", "", "end of the window")
	f.StringVar(&alSearch, "search", "", "substring of the resource name")
	f.IntVar(&alLimit, "limit", 0, "max results")
	f.BoolVar(&alDetails, "details", false, "include what changed (Zabbix's change details, secrets are already masked by Zabbix)")
	getCmd.AddCommand(getAuditLogCmd)
}
