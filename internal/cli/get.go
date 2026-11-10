package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/config"
	"github.com/mwtrigg/zbxctl/internal/output"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// newClient loads config and creates an authenticated API client, applying any
// CLI overrides (--insecure, --context).
func newClient() (*api.Client, output.Format, *config.Context, error) {
	outFmt := output.Format(flagOutput)
	_, ctx, err := config.LoadConfig(flagContext)
	if err != nil {
		return nil, outFmt, nil, err
	}
	if flagInsecure {
		f := false
		ctx.TLSVerify = &f
	}
	c, err := api.NewClient(context.Background(), ctx)
	if err != nil {
		return nil, outFmt, nil, err
	}
	return c, outFmt, ctx, nil
}

// effectiveLimit returns the user-supplied limit or the context default.
func effectiveLimit(flagLimit int, ctx *config.Context) int {
	if flagLimit > 0 {
		return flagLimit
	}
	return ctx.DefaultLimit
}

// parseTimestamp parses a Unix timestamp (integer string) or an RFC3339 /
// YYYY-MM-DD date string into a Unix timestamp. Returns 0 on empty input.
func parseTimestamp(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	// Try plain integer first.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	// Try RFC3339.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), nil
	}
	// Try date-only.
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("cannot parse timestamp %q (accepted: unix integer, RFC3339, YYYY-MM-DD)", s)
}

// fmtUnix converts a Unix timestamp string to a short human-readable format.
// Returns an empty string for "0" or "".
func fmtUnix(s string) string {
	if s == "" || s == "0" {
		return ""
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s
	}
	return time.Unix(n, 0).UTC().Format("2006-01-02 15:04:05")
}

// hostStatusLabel converts a Zabbix status string to a human-readable label.
func hostStatusLabel(s string) string {
	switch s {
	case "0":
		return "monitored"
	case "1":
		return "unmonitored"
	default:
		return s
	}
}

// availableLabel converts a Zabbix available string to a human-readable label.
func availableLabel(s string) string {
	switch s {
	case "0":
		return "unknown"
	case "1":
		return "available"
	case "2":
		return "unavailable"
	default:
		return s
	}
}

// severityLabel converts a Zabbix severity/priority integer string.
func severityLabel(s string) string {
	switch s {
	case "0":
		return "not_classified"
	case "1":
		return "information"
	case "2":
		return "warning"
	case "3":
		return "average"
	case "4":
		return "high"
	case "5":
		return "disaster"
	default:
		return s
	}
}

// triggerValueLabel converts a Zabbix trigger value string.
func triggerValueLabel(s string) string {
	switch s {
	case "0":
		return "OK"
	case "1":
		return "PROBLEM"
	default:
		return s
	}
}

// enabledLabel converts a "0"/"1" status to enabled/disabled.
func enabledLabel(s string) string {
	switch s {
	case "0":
		return "enabled"
	case "1":
		return "disabled"
	default:
		return s
	}
}

// guiAccessLabel converts usergroup gui_access codes.
func guiAccessLabel(s string) string {
	switch s {
	case "0":
		return "default"
	case "1":
		return "internal"
	case "2":
		return "LDAP"
	case "3":
		return "disabled"
	default:
		return s
	}
}

// userTypeLabel converts a Zabbix user type code.
func userTypeLabel(s string) string {
	switch s {
	case "1":
		return "user"
	case "2":
		return "admin"
	case "3":
		return "super_admin"
	default:
		return s
	}
}

// acknowledgedLabel converts "0"/"1" acknowledged field.
func acknowledgedLabel(s string) string {
	switch s {
	case "0":
		return "no"
	case "1":
		return "yes"
	default:
		return s
	}
}

// ── parent get command ────────────────────────────────────────────────────────

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Read Zabbix resources",
}

// ── get hosts ────────────────────────────────────────────────────────────────

var (
	getHostsGroup  string
	getHostsStatus string
	getHostsSearch string
	getHostsLimit  int
	getHostsSortBy string
)

var getHostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "List hosts",
	RunE:  runGetHosts,
}

func init() {
	f := getHostsCmd.Flags()
	f.StringVar(&getHostsGroup, "group", "", "filter by host group name")
	f.StringVar(&getHostsStatus, "status", "", "filter by status: monitored|unmonitored")
	f.StringVar(&getHostsSearch, "search", "", "substring search on host/name")
	f.IntVar(&getHostsLimit, "limit", 0, "max results (default: context default_limit)")
	f.StringVar(&getHostsSortBy, "sort-by", "host", "sort field: host|name|hostid")
	getCmd.AddCommand(getHostsCmd)
}

func runGetHosts(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	filter := resources.HostFilter{
		Search:    getHostsSearch,
		SortField: getHostsSortBy,
		Limit:     effectiveLimit(getHostsLimit, ctx),
	}

	// Translate human status to Zabbix code.
	switch getHostsStatus {
	case "monitored":
		filter.Status = "0"
	case "unmonitored":
		filter.Status = "1"
	case "":
		// no filter
	default:
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use monitored or unmonitored", getHostsStatus), output.ExitError)
	}

	// Resolve group name → ID if provided.
	if getHostsGroup != "" {
		g, err := resources.GetGroupByName(c, getHostsGroup)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		filter.GroupIDs = []string{g.GroupID}
	}

	hosts, err := resources.GetHosts(c, filter)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "hostid", Header: "HOST_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "name", Header: "NAME"},
		{Key: "status", Header: "STATUS"},
		{Key: "available", Header: "AVAILABLE"},
	}

	rows := make([]map[string]any, len(hosts))
	for i, h := range hosts {
		rows[i] = map[string]any{
			"hostid":      h.HostID,
			"host":        h.Host,
			"name":        h.Name,
			"status":      hostStatusLabel(h.Status),
			"status_raw":  h.Status,
			"available":   availableLabel(h.Available),
			"available_raw": h.Available,
			"description": h.Description,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get host <id|name> ────────────────────────────────────────────────────────

var getHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Show detail for a single host",
	Args:  cobra.ExactArgs(1),
	RunE:  runGetHost,
}

func init() { getCmd.AddCommand(getHostCmd) }

func runGetHost(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	h, err := resources.GetHost(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "hostid", Header: "HOST_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "name", Header: "NAME"},
		{Key: "status", Header: "STATUS"},
		{Key: "available", Header: "AVAILABLE"},
		{Key: "description", Header: "DESCRIPTION"},
	}
	row := map[string]any{
		"hostid":        h.HostID,
		"host":          h.Host,
		"name":          h.Name,
		"status":        hostStatusLabel(h.Status),
		"status_raw":    h.Status,
		"available":     availableLabel(h.Available),
		"available_raw": h.Available,
		"description":   h.Description,
	}
	return output.Print(outFmt, cols, []map[string]any{row})
}

// ── get items <host> ──────────────────────────────────────────────────────────

var (
	getItemsKey    string
	getItemsStatus string
	getItemsState  string
	getItemsLimit  int
)

var getItemsCmd = &cobra.Command{
	Use:   "items <host>",
	Short: "List items for a host",
	Args:  cobra.ExactArgs(1),
	RunE:  runGetItems,
}

func init() {
	f := getItemsCmd.Flags()
	f.StringVar(&getItemsKey, "key", "", "substring search on item key")
	f.StringVar(&getItemsStatus, "status", "", "filter by status: enabled|disabled")
	f.StringVar(&getItemsState, "state", "", "filter by collection state: normal|unsupported")
	f.IntVar(&getItemsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getItemsCmd)
}

func runGetItems(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	hostID, err := resources.ResolveHostID(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	filter := resources.ItemFilter{
		HostIDs:   []string{hostID},
		KeySearch: getItemsKey,
		Limit:     effectiveLimit(getItemsLimit, ctx),
	}
	switch getItemsStatus {
	case "enabled":
		filter.Status = "0"
	case "disabled":
		filter.Status = "1"
	case "":
		// no filter
	default:
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use enabled or disabled", getItemsStatus), output.ExitError)
	}
	switch getItemsState {
	case "normal":
		filter.State = "0"
	case "unsupported":
		filter.State = "1"
	case "":
		// no filter
	default:
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --state %q: use normal or unsupported", getItemsState), output.ExitError)
	}

	items, err := resources.GetItems(c, filter)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "itemid", Header: "ITEM_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "key_", Header: "KEY"},
		{Key: "status", Header: "STATUS"},
		{Key: "state", Header: "STATE"},
		{Key: "units", Header: "UNITS"},
		{Key: "delay", Header: "DELAY"},
	}
	rows := make([]map[string]any, len(items))
	for i, it := range items {
		stateLabel := "normal"
		if it.State == "1" {
			stateLabel = "unsupported"
		}
		rows[i] = map[string]any{
			"itemid":         it.ItemID,
			"hostid":         it.HostID,
			"name":           it.Name,
			"key_":           it.Key,
			"type":           it.Type,
			"value_type":     it.ValueType,
			"status":         enabledLabel(it.Status),
			"status_raw":     it.Status,
			"state":          stateLabel,
			"state_raw":      it.State,
			"error":          it.Error,
			"lastvalue":      it.LastValue,
			"lastclock":      fmtUnix(it.LastClock),
			"lastclock_unix": it.LastClock,
			"units":          it.Units,
			"delay":          it.Delay,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get latestdata [<host>] ───────────────────────────────────────────────────

var (
	getLatestDataLimit    int
	flagLatestdataHosts   []string
	flagLatestdataGroups  []string
	flagLatestdataItem    string
)

var getLatestDataCmd = &cobra.Command{
	Use:   "latestdata [<host>]",
	Short: "Show latest collected metric values for a host",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runGetLatestData,
}

func init() {
	f := getLatestDataCmd.Flags()
	f.IntVar(&getLatestDataLimit, "limit", 0, "max results")
	f.StringArrayVar(&flagLatestdataHosts, "host", nil, "host name or ID (repeatable)")
	f.StringArrayVar(&flagLatestdataGroups, "group", nil, "host group name or ID (repeatable)")
	f.StringVar(&flagLatestdataItem, "item", "", "filter by item key substring")
	getCmd.AddCommand(getLatestDataCmd)
}

func runGetLatestData(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}

	// Collect all host targets: positional arg + --host flags.
	hostTargets := append([]string{}, flagLatestdataHosts...)
	if len(args) > 0 {
		hostTargets = append(hostTargets, args[0])
	}

	if len(hostTargets) == 0 && len(flagLatestdataGroups) == 0 {
		return cliPrintErr(outFmt, "provide at least one host (positional or --host) or --group", output.ExitError)
	}

	var hostIDs []string
	for _, h := range hostTargets {
		id, err := resources.ResolveHostID(c, h)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, fmt.Sprintf("host %q: %s", h, err), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, fmt.Sprintf("host %q: %s", h, err), output.ExitError)
		}
		hostIDs = append(hostIDs, id)
	}

	var groupIDs []string
	for _, g := range flagLatestdataGroups {
		id, err := resources.ResolveGroupID(c, g)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitError)
		}
		groupIDs = append(groupIDs, id)
	}

	items, err := resources.GetLatestData(c, resources.ItemFilter{
		HostIDs:   hostIDs,
		GroupIDs:  groupIDs,
		KeySearch: flagLatestdataItem,
		Limit:     effectiveLimit(getLatestDataLimit, ctx),
		WithHost:  true,
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "host", Header: "HOST"},
		{Key: "hostid", Header: "HOSTID"},
		{Key: "itemid", Header: "ITEMID"},
		{Key: "key_", Header: "KEY"},
		{Key: "name", Header: "NAME"},
		{Key: "value_type", Header: "VALUE_TYPE"},
		{Key: "lastvalue", Header: "LAST_VALUE"},
		{Key: "lastclock", Header: "LAST_SEEN"},
		{Key: "lastclock_unix", Header: "LAST_SEEN_UNIX"},
		{Key: "units", Header: "UNITS"},
	}
	rows := make([]map[string]any, len(items))
	for i, it := range items {
		rows[i] = map[string]any{
			"host":           it.Host,
			"hostid":         it.HostID,
			"itemid":         it.ItemID,
			"key_":           it.Key,
			"name":           it.Name,
			"value_type":     it.ValueType,
			"lastvalue":      it.LastValue,
			"lastclock":      fmtUnix(it.LastClock),
			"lastclock_unix": it.LastClock,
			"units":          it.Units,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get triggers ──────────────────────────────────────────────────────────────

var (
	getTriggersHost     string
	getTriggersSeverity string
	getTriggersStatus   string
	getTriggersProblem  bool
	getTriggersLimit    int
	getTriggersSortBy   string
)

var getTriggersCmd = &cobra.Command{
	Use:   "triggers",
	Short: "List triggers",
	RunE:  runGetTriggers,
}

func init() {
	f := getTriggersCmd.Flags()
	f.StringVar(&getTriggersHost, "host", "", "filter by host name or ID")
	f.StringVar(&getTriggersSeverity, "severity", "", "minimum severity: 0-5 or not_classified|information|warning|average|high|disaster")
	f.StringVar(&getTriggersStatus, "status", "", "filter by status: enabled|disabled")
	f.BoolVar(&getTriggersProblem, "problem", false, "only show triggers currently in PROBLEM state")
	f.IntVar(&getTriggersLimit, "limit", 0, "max results")
	f.StringVar(&getTriggersSortBy, "sort-by", "priority", "sort field: priority|lastchange|description")
	getCmd.AddCommand(getTriggersCmd)
}

func runGetTriggers(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	filter := resources.TriggerFilter{
		SortField:    getTriggersSortBy,
		Limit:        effectiveLimit(getTriggersLimit, ctx),
		OnlyProblems: getTriggersProblem,
	}

	if getTriggersHost != "" {
		id, err := resources.ResolveHostID(c, getTriggersHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		filter.HostIDs = []string{id}
	}

	if getTriggersSeverity != "" {
		sev, err := parseSeverity(getTriggersSeverity)
		if err != nil {
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		filter.MinSeverity = sev
	}

	switch getTriggersStatus {
	case "enabled":
		filter.Status = "0"
	case "disabled":
		filter.Status = "1"
	case "":
	default:
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use enabled or disabled", getTriggersStatus), output.ExitError)
	}

	triggers, err := resources.GetTriggers(c, filter)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "triggerid", Header: "TRIGGER_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "description", Header: "DESCRIPTION"},
		{Key: "priority", Header: "SEVERITY"},
		{Key: "value", Header: "STATE"},
		{Key: "status", Header: "STATUS"},
	}
	rows := make([]map[string]any, len(triggers))
	for i, t := range triggers {
		hostName := ""
		if len(t.Hosts) > 0 {
			hostName = t.Hosts[0].Host
		}
		rows[i] = map[string]any{
			"triggerid":       t.TriggerID,
			"host":            hostName,
			"description":     t.Description,
			"priority":        severityLabel(t.Priority),
			"priority_raw":    t.Priority,
			"value":           triggerValueLabel(t.Value),
			"value_raw":       t.Value,
			"status":          enabledLabel(t.Status),
			"status_raw":      t.Status,
			"lastchange":      fmtUnix(t.LastChange),
			"lastchange_unix": t.LastChange,
			"hosts":           t.Hosts,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get problems ──────────────────────────────────────────────────────────────

var (
	getProblemsHost         string
	getProblemsSeverity     string
	getProblemsSince        string
	getProblemsAcknowledged string
	getProblemsResolved     bool
	getProblemsLimit        int
)

var getProblemsCmd = &cobra.Command{
	Use:   "problems",
	Short: "List active problems",
	RunE:  runGetProblems,
}

func init() {
	f := getProblemsCmd.Flags()
	f.StringVar(&getProblemsHost, "host", "", "filter by host name or ID")
	f.StringVar(&getProblemsSeverity, "severity", "", "minimum severity: 0-5 or not_classified|information|warning|average|high|disaster")
	f.StringVar(&getProblemsSince, "since", "", "only problems after this time (unix timestamp, RFC3339, or YYYY-MM-DD)")
	f.StringVar(&getProblemsAcknowledged, "acknowledged", "", "filter: true=acknowledged only, false=unacknowledged only")
	f.BoolVar(&getProblemsResolved, "resolved", false, "include recently-resolved problems (default: open only)")
	f.IntVar(&getProblemsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getProblemsCmd)
}

func runGetProblems(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	filter := resources.ProblemFilter{
		MinSeverity: -1,
		Limit:       effectiveLimit(getProblemsLimit, ctx),
		Recent:      getProblemsResolved,
	}

	if getProblemsHost != "" {
		id, err := resources.ResolveHostID(c, getProblemsHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		filter.HostIDs = []string{id}
	}

	if getProblemsSeverity != "" {
		sev, err := parseSeverity(getProblemsSeverity)
		if err != nil {
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		n, _ := strconv.Atoi(sev)
		filter.MinSeverity = n
	}

	if getProblemsSince != "" {
		ts, err := parseTimestamp(getProblemsSince)
		if err != nil {
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		filter.Since = ts
	}

	if getProblemsAcknowledged != "" {
		b, err := strconv.ParseBool(getProblemsAcknowledged)
		if err != nil {
			return cliPrintErr(outFmt, fmt.Sprintf("invalid --acknowledged %q: use true or false", getProblemsAcknowledged), output.ExitError)
		}
		filter.Acknowledged = &b
	}

	problems, err := resources.GetProblems(c, filter)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "eventid", Header: "EVENT_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "severity", Header: "SEVERITY"},
		{Key: "status", Header: "STATUS"},
		{Key: "acknowledged", Header: "ACK"},
		{Key: "clock", Header: "OPENED"},
		{Key: "r_clock", Header: "RESOLVED"},
	}
	rows := make([]map[string]any, len(problems))
	for i, p := range problems {
		resolved := p.RecoveryEventID != "" && p.RecoveryEventID != "0"
		statusLabel := "open"
		if resolved {
			statusLabel = "resolved"
		}
		rows[i] = map[string]any{
			"eventid":          p.EventID,
			"objectid":         p.ObjectID,
			"name":             p.Name,
			"severity":         severityLabel(p.Severity),
			"severity_raw":     p.Severity,
			"status":           statusLabel,
			"acknowledged":     acknowledgedLabel(p.Acknowledged),
			"acknowledged_raw": p.Acknowledged,
			"clock":            fmtUnix(p.Clock),
			"clock_unix":       p.Clock,
			"r_eventid":        p.RecoveryEventID,
			"r_clock":          fmtUnix(p.RecoveryClock),
			"r_clock_unix":     p.RecoveryClock,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get events ────────────────────────────────────────────────────────────────

var (
	getEventsHost  string
	getEventsSince string
	getEventsUntil string
	getEventsLimit int
)

var getEventsCmd = &cobra.Command{
	Use:   "events",
	Short: "List events",
	RunE:  runGetEvents,
}

func init() {
	f := getEventsCmd.Flags()
	f.StringVar(&getEventsHost, "host", "", "filter by host name or ID")
	f.StringVar(&getEventsSince, "since", "", "events after this time (unix timestamp, RFC3339, or YYYY-MM-DD)")
	f.StringVar(&getEventsUntil, "until", "", "events before this time (unix timestamp, RFC3339, or YYYY-MM-DD)")
	f.IntVar(&getEventsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getEventsCmd)
}

func runGetEvents(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	filter := resources.EventFilter{
		Limit: effectiveLimit(getEventsLimit, ctx),
	}

	if getEventsHost != "" {
		id, err := resources.ResolveHostID(c, getEventsHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		filter.HostIDs = []string{id}
	}

	if getEventsSince != "" {
		ts, err := parseTimestamp(getEventsSince)
		if err != nil {
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		filter.Since = ts
	}
	if getEventsUntil != "" {
		ts, err := parseTimestamp(getEventsUntil)
		if err != nil {
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		filter.Until = ts
	}

	events, err := resources.GetEvents(c, filter)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "eventid", Header: "EVENT_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "name", Header: "NAME"},
		{Key: "value", Header: "STATE"},
		{Key: "acknowledged", Header: "ACK"},
		{Key: "clock", Header: "TIME"},
	}
	rows := make([]map[string]any, len(events))
	for i, ev := range events {
		hostName := ""
		if len(ev.Hosts) > 0 {
			hostName = ev.Hosts[0].Host
		}
		rows[i] = map[string]any{
			"eventid":      ev.EventID,
			"objectid":     ev.ObjectID,
			"host":         hostName,
			"name":         ev.Name,
			"value":        triggerValueLabel(ev.Value),
			"value_raw":    ev.Value,
			"acknowledged": acknowledgedLabel(ev.Acknowledged),
			"acknowledged_raw": ev.Acknowledged,
			"clock":        fmtUnix(ev.Clock),
			"clock_unix":   ev.Clock,
			"hosts":        ev.Hosts,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get templates ─────────────────────────────────────────────────────────────

var (
	getTemplatesSearch string
	getTemplatesLimit  int
)

var getTemplatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List templates",
	RunE:  runGetTemplates,
}

func init() {
	f := getTemplatesCmd.Flags()
	f.StringVar(&getTemplatesSearch, "search", "", "substring search on template name")
	f.IntVar(&getTemplatesLimit, "limit", 0, "max results")
	getCmd.AddCommand(getTemplatesCmd)
}

func runGetTemplates(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	templates, err := resources.GetTemplates(c, resources.TemplateFilter{
		Search: getTemplatesSearch,
		Limit:  effectiveLimit(getTemplatesLimit, ctx),
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "templateid", Header: "TEMPLATE_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "name", Header: "NAME"},
		{Key: "items", Header: "ITEMS"},
		{Key: "triggers", Header: "TRIGGERS"},
		{Key: "graphs", Header: "GRAPHS"},
	}
	rows := make([]map[string]any, len(templates))
	for i, t := range templates {
		rows[i] = map[string]any{
			"templateid":  t.TemplateID,
			"host":        t.Host,
			"name":        t.Name,
			"description": t.Description,
			"items":       t.Items,
			"triggers":    t.Triggers,
			"graphs":      t.Graphs,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get template <id|name> ────────────────────────────────────────────────────

var getTemplateCmd = &cobra.Command{
	Use:   "template <id|name>",
	Short: "Show detail for a single template",
	Args:  cobra.ExactArgs(1),
	RunE:  runGetTemplate,
}

func init() { getCmd.AddCommand(getTemplateCmd) }

func runGetTemplate(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	t, err := resources.GetTemplate(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "templateid", Header: "TEMPLATE_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "name", Header: "NAME"},
		{Key: "items", Header: "ITEMS"},
		{Key: "triggers", Header: "TRIGGERS"},
		{Key: "graphs", Header: "GRAPHS"},
		{Key: "description", Header: "DESCRIPTION"},
	}
	row := map[string]any{
		"templateid":  t.TemplateID,
		"host":        t.Host,
		"name":        t.Name,
		"description": t.Description,
		"items":       t.Items,
		"triggers":    t.Triggers,
		"graphs":      t.Graphs,
	}
	return output.Print(outFmt, cols, []map[string]any{row})
}

// ── get groups ────────────────────────────────────────────────────────────────

var getGroupsLimit int

var getGroupsCmd = &cobra.Command{
	Use:   "groups",
	Short: "List host groups",
	RunE:  runGetGroups,
}

func init() {
	getGroupsCmd.Flags().IntVar(&getGroupsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getGroupsCmd)
}

func runGetGroups(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	groups, err := resources.GetGroups(c, resources.GroupFilter{
		Limit: effectiveLimit(getGroupsLimit, ctx),
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "groupid", Header: "GROUP_ID"},
		{Key: "name", Header: "NAME"},
	}
	rows := make([]map[string]any, len(groups))
	for i, g := range groups {
		rows[i] = map[string]any{
			"groupid": g.GroupID,
			"name":    g.Name,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get users ─────────────────────────────────────────────────────────────────

var getUsersLimit int

var getUsersCmd = &cobra.Command{
	Use:   "users",
	Short: "List users",
	RunE:  runGetUsers,
}

func init() {
	getUsersCmd.Flags().IntVar(&getUsersLimit, "limit", 0, "max results")
	getCmd.AddCommand(getUsersCmd)
}

func runGetUsers(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	users, err := resources.GetUsers(c, resources.UserFilter{
		Limit: effectiveLimit(getUsersLimit, ctx),
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "userid", Header: "USER_ID"},
		{Key: "username", Header: "USERNAME"},
		{Key: "name", Header: "NAME"},
		{Key: "surname", Header: "SURNAME"},
		{Key: "type", Header: "TYPE"},
	}
	rows := make([]map[string]any, len(users))
	for i, u := range users {
		rows[i] = map[string]any{
			"userid":   u.UserID,
			"username": u.Username,
			"name":     u.Name,
			"surname":  u.Surname,
			"roleid":   u.RoleID,
			"type":     userTypeLabel(u.Type),
			"type_raw": u.Type,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get user <id|name> ────────────────────────────────────────────────────────

var getUserCmd = &cobra.Command{
	Use:   "user <id|name>",
	Short: "Show detail for a single user",
	Args:  cobra.ExactArgs(1),
	RunE:  runGetUser,
}

func init() { getCmd.AddCommand(getUserCmd) }

func runGetUser(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	u, err := resources.GetUser(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "userid", Header: "USER_ID"},
		{Key: "username", Header: "USERNAME"},
		{Key: "name", Header: "NAME"},
		{Key: "surname", Header: "SURNAME"},
		{Key: "type", Header: "TYPE"},
		{Key: "roleid", Header: "ROLE_ID"},
	}
	row := map[string]any{
		"userid":   u.UserID,
		"username": u.Username,
		"name":     u.Name,
		"surname":  u.Surname,
		"roleid":   u.RoleID,
		"type":     userTypeLabel(u.Type),
		"type_raw": u.Type,
	}
	return output.Print(outFmt, cols, []map[string]any{row})
}

// ── get usergroups ────────────────────────────────────────────────────────────

var getUserGroupsLimit int

var getUserGroupsCmd = &cobra.Command{
	Use:   "usergroups",
	Short: "List user groups",
	RunE:  runGetUserGroups,
}

func init() {
	getUserGroupsCmd.Flags().IntVar(&getUserGroupsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getUserGroupsCmd)
}

func runGetUserGroups(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	groups, err := resources.GetUserGroups(c, resources.UserGroupFilter{
		Limit: effectiveLimit(getUserGroupsLimit, ctx),
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "usrgrpid", Header: "GROUP_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "gui_access", Header: "GUI_ACCESS"},
		{Key: "users_status", Header: "STATUS"},
	}
	rows := make([]map[string]any, len(groups))
	for i, g := range groups {
		rows[i] = map[string]any{
			"usrgrpid":         g.UserGroupID,
			"name":             g.Name,
			"gui_access":       guiAccessLabel(g.GUIAccess),
			"gui_access_raw":   g.GUIAccess,
			"users_status":     enabledLabel(g.UsersStatus),
			"users_status_raw": g.UsersStatus,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get history ───────────────────────────────────────────────────────────────

var (
	flagHistoryItem        string
	flagHistoryHosts       []string
	flagHistoryGroups      []string
	flagHistorySince       string
	flagHistoryUntil       string
	flagHistoryLimit       int
	flagHistoryAroundEvent string
	flagHistoryWindow      string
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Fetch time-series metric history",
	RunE:  runGetHistory,
}

func init() {
	f := historyCmd.Flags()
	f.StringVar(&flagHistoryItem, "item", "", "item key pattern or numeric item ID (required)")
	f.StringArrayVar(&flagHistoryHosts, "host", nil, "filter by host name or ID (repeatable)")
	f.StringArrayVar(&flagHistoryGroups, "group", nil, "filter by host group name or ID (repeatable)")
	f.StringVar(&flagHistorySince, "since", "", "start time: unix timestamp, RFC3339, or YYYY-MM-DD")
	f.StringVar(&flagHistoryUntil, "until", "", "end time: unix timestamp, RFC3339, or YYYY-MM-DD")
	f.IntVar(&flagHistoryLimit, "limit", 0, "max results")
	f.StringVar(&flagHistoryAroundEvent, "around-event", "", "anchor query to this event ID (resolves host, centers window on event clock)")
	f.StringVar(&flagHistoryWindow, "window", "1h", "time window for --around-event: 30m|1h|2h|6h")
	getCmd.AddCommand(historyCmd)
}

// isNumericStr reports whether s is a non-empty string of ASCII digits only.
func isNumericStr(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func runGetHistory(_ *cobra.Command, _ []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}

	if flagHistoryAroundEvent != "" {
		return runGetHistoryAroundEvent(c, outFmt, ctx)
	}

	if flagHistoryItem == "" {
		return cliPrintErr(outFmt, "--item is required (or use --around-event)", output.ExitError)
	}

	// Non-numeric item key requires at least one host or group for resolution.
	if !isNumericStr(flagHistoryItem) && len(flagHistoryHosts) == 0 && len(flagHistoryGroups) == 0 {
		return cliPrintErr(outFmt, "non-numeric --item requires at least one --host or --group for resolution", output.ExitError)
	}

	// Resolve host IDs.
	var hostIDs []string
	for _, h := range flagHistoryHosts {
		id, err := resources.ResolveHostID(c, h)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, fmt.Sprintf("host %q: %s", h, err), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, fmt.Sprintf("host %q: %s", h, err), output.ExitError)
		}
		hostIDs = append(hostIDs, id)
	}

	// Resolve group IDs.
	var groupIDs []string
	for _, g := range flagHistoryGroups {
		id, err := resources.ResolveGroupID(c, g)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitError)
		}
		groupIDs = append(groupIDs, id)
	}

	var since, until int64
	if flagHistorySince != "" {
		ts, err := parseTimestamp(flagHistorySince)
		if err != nil {
			return cliPrintErr(outFmt, "invalid --since: "+err.Error(), output.ExitError)
		}
		since = ts
	}
	if flagHistoryUntil != "" {
		ts, err := parseTimestamp(flagHistoryUntil)
		if err != nil {
			return cliPrintErr(outFmt, "invalid --until: "+err.Error(), output.ExitError)
		}
		until = ts
	}

	q := resources.HistoryQuery{
		HostIDs:   hostIDs,
		GroupIDs:  groupIDs,
		KeySearch: flagHistoryItem,
		TimeFrom:  since,
		TimeTill:  until,
		Limit:     effectiveLimit(flagHistoryLimit, ctx),
	}
	if isNumericStr(flagHistoryItem) {
		q.ItemIDs = []string{flagHistoryItem}
		q.KeySearch = ""
	}

	records, err := resources.GetHistory(c, q)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "host", Header: "HOST"},
		{Key: "hostid", Header: "HOSTID"},
		{Key: "itemid", Header: "ITEMID"},
		{Key: "key_", Header: "KEY"},
		{Key: "clock", Header: "TIME"},
		{Key: "clock_unix", Header: "CLOCK_UNIX"},
		{Key: "value", Header: "VALUE"},
	}
	rows := make([]map[string]any, len(records))
	for i, r := range records {
		rows[i] = map[string]any{
			"host":       r.Host,
			"hostid":     r.HostID,
			"itemid":     r.ItemID,
			"key_":       r.Key,
			"clock":      fmtUnix(r.Clock),
			"clock_unix": r.ClockUnix,
			"value":      r.Value,
		}
	}
	return output.Print(outFmt, cols, rows)
}

func runGetHistoryAroundEvent(c *api.Client, outFmt output.Format, ctx *config.Context) error {
	windowDur, err := time.ParseDuration(flagHistoryWindow)
	if err != nil {
		return cliPrintErr(outFmt,
			fmt.Sprintf("invalid --window %q: use 30m, 1h, 2h, or 6h", flagHistoryWindow),
			output.ExitError)
	}
	if windowDur <= 0 {
		return cliPrintErr(outFmt,
			fmt.Sprintf("--window must be a positive duration, got %q", flagHistoryWindow),
			output.ExitError)
	}
	half := windowDur / 2

	info, err := resources.GetEventTriggerInfo(c, flagHistoryAroundEvent)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if info.HostID == "" {
		return cliPrintErr(outFmt,
			fmt.Sprintf("could not resolve host for event %s", flagHistoryAroundEvent),
			output.ExitError)
	}

	timeFrom := info.Clock - int64(half.Seconds())
	timeTill := info.Clock + int64(half.Seconds())

	records, err := resources.GetHistory(c, resources.HistoryQuery{
		HostIDs:   []string{info.HostID},
		KeySearch: flagHistoryItem,
		TimeFrom:  timeFrom,
		TimeTill:  timeTill,
		Limit:     effectiveLimit(flagHistoryLimit, ctx),
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "host", Header: "HOST"},
		{Key: "key_", Header: "KEY"},
		{Key: "clock", Header: "TIME"},
		{Key: "value", Header: "VALUE"},
	}
	rows := make([]map[string]any, len(records))
	for i, r := range records {
		rows[i] = map[string]any{
			"host":       r.Host,
			"hostid":     r.HostID,
			"itemid":     r.ItemID,
			"key_":       r.Key,
			"clock":      fmtUnix(r.Clock),
			"clock_unix": r.ClockUnix,
			"value":      r.Value,
		}
	}

	if outFmt == output.FormatJSONL {
		meta := map[string]any{
			"event_id":    info.EventID,
			"event_clock": fmtUnix(fmt.Sprintf("%d", info.Clock)),
			"event_name":  info.EventName,
			"window_from": fmtUnix(fmt.Sprintf("%d", timeFrom)),
			"window_till": fmtUnix(fmt.Sprintf("%d", timeTill)),
		}
		rows = append(rows, map[string]any{"_meta": meta})
	}

	return output.Print(outFmt, cols, rows)
}

// ── severity parsing ──────────────────────────────────────────────────────────

// parseSeverity converts a severity name or integer string to a Zabbix severity
// integer string ("0"–"5").
func parseSeverity(s string) (string, error) {
	switch s {
	case "not_classified", "not classified":
		return "0", nil
	case "information":
		return "1", nil
	case "warning":
		return "2", nil
	case "average":
		return "3", nil
	case "high":
		return "4", nil
	case "disaster":
		return "5", nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 5 {
		return "", fmt.Errorf("invalid severity %q: use 0-5 or not_classified|information|warning|average|high|disaster", s)
	}
	return s, nil
}

// ── get scripts ───────────────────────────────────────────────────────────────

var getScriptsHost string

var getScriptsCmd = &cobra.Command{
	Use:   "scripts",
	Short: "List Zabbix scripts",
	RunE:  runGetScripts,
}

func init() {
	getScriptsCmd.Flags().StringVar(&getScriptsHost, "host", "", "filter to scripts applicable to this host (name or ID)")
	getCmd.AddCommand(getScriptsCmd)
}

func runGetScripts(_ *cobra.Command, _ []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}

	f := resources.ScriptFilter{}
	if getScriptsHost != "" {
		id, err := resources.ResolveHostID(c, getScriptsHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		f.HostIDs = []string{id}
	}

	scripts, err := resources.GetScripts(c, f)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "scriptid", Header: "SCRIPT_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "type", Header: "TYPE"},
		{Key: "execute_on", Header: "EXECUTE_ON"},
		{Key: "description", Header: "DESCRIPTION"},
	}
	rows := make([]map[string]any, len(scripts))
	for i, s := range scripts {
		rows[i] = map[string]any{
			"scriptid":       s.ScriptID,
			"name":           s.Name,
			"type":           resources.ScriptTypeLabel(s.Type),
			"type_raw":       s.Type,
			"execute_on":     resources.ExecuteOnLabel(s.ExecuteOn),
			"execute_on_raw": s.ExecuteOn,
			"description":    s.Description,
			"command":        s.Command,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get alerts ────────────────────────────────────────────────────────────────

var (
	getAlertsHost   string
	getAlertsEvent  string
	getAlertsSince  string
	getAlertsStatus string
	getAlertsLimit  int
)

var getAlertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "List notification alerts (delivery status)",
	RunE:  runGetAlerts,
}

func init() {
	f := getAlertsCmd.Flags()
	f.StringVar(&getAlertsHost, "host", "", "filter by host name or ID")
	f.StringVar(&getAlertsEvent, "event", "", "filter by event ID")
	f.StringVar(&getAlertsSince, "since", "", "alerts after this time: unix timestamp, RFC3339, or YYYY-MM-DD")
	f.StringVar(&getAlertsStatus, "status", "", "filter by delivery status: sent|failed")
	f.IntVar(&getAlertsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getAlertsCmd)
}

func runGetAlerts(_ *cobra.Command, _ []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}

	switch getAlertsStatus {
	case "sent", "failed", "":
		// valid
	default:
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use sent or failed", getAlertsStatus), output.ExitError)
	}

	f := resources.AlertFilter{
		Limit:  effectiveLimit(getAlertsLimit, ctx),
		Status: getAlertsStatus,
	}

	if getAlertsHost != "" {
		id, err := resources.ResolveHostID(c, getAlertsHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		f.HostIDs = []string{id}
	}
	if getAlertsEvent != "" {
		f.EventIDs = []string{getAlertsEvent}
	}
	if getAlertsSince != "" {
		ts, err := parseTimestamp(getAlertsSince)
		if err != nil {
			return cliPrintErr(outFmt, "invalid --since: "+err.Error(), output.ExitError)
		}
		f.Since = ts
	}

	alerts, err := resources.GetAlerts(c, f)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "alertid", Header: "ALERT_ID"},
		{Key: "eventid", Header: "EVENT_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "sendto", Header: "SEND_TO"},
		{Key: "status", Header: "STATUS"},
		{Key: "clock", Header: "TIME"},
	}
	rows := make([]map[string]any, len(alerts))
	for i, a := range alerts {
		rows[i] = map[string]any{
			"alertid":     a.AlertID,
			"eventid":     a.EventID,
			"hostid":      a.HostID,
			"host":        a.Host,
			"clock":       fmtUnix(a.Clock),
			"clock_unix":  a.Clock,
			"sendto":      a.SendTo,
			"subject":     a.Subject,
			"status":      resources.AlertStatusLabel(a.Status),
			"status_raw":  a.Status,
			"error":       a.Error,
			"userid":      a.UserID,
			"mediatypeid": a.MediaTypeID,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get interfaces ────────────────────────────────────────────────────────────

var getInterfacesCmd = &cobra.Command{
	Use:   "interfaces <host>",
	Short: "Show network interfaces for a host",
	Args:  cobra.ExactArgs(1),
	RunE:  runGetInterfaces,
}

func init() { getCmd.AddCommand(getInterfacesCmd) }

func runGetInterfaces(_ *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}

	hostID, err := resources.ResolveHostID(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	ifaces, err := resources.GetInterfaces(c, hostID)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "interfaceid", Header: "IFACE_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "type", Header: "TYPE"},
		{Key: "ip", Header: "IP"},
		{Key: "dns", Header: "DNS"},
		{Key: "port", Header: "PORT"},
		{Key: "available", Header: "AVAILABLE"},
		{Key: "error", Header: "ERROR"},
	}
	rows := make([]map[string]any, len(ifaces))
	for i, iface := range ifaces {
		rows[i] = map[string]any{
			"interfaceid":       iface.InterfaceID,
			"hostid":            iface.HostID,
			"host":              iface.Host,
			"type":              iface.Type,
			"type_raw":          iface.TypeRaw,
			"main":              iface.Main,
			"useip":             iface.UseIP,
			"ip":                iface.IP,
			"dns":               iface.DNS,
			"port":              iface.Port,
			"available":         iface.Available,
			"available_raw":     iface.AvailableRaw,
			"error":             iface.Error,
			"snmp_version":      iface.SNMPVersion,
			"snmp_community":    iface.SNMPCommunity,
			"snmp_securityname": iface.SNMPSecurityName,
		}
	}
	return output.Print(outFmt, cols, rows)
}
