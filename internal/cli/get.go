package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	for _, kv := range flagFilter {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, outFmt, nil, &usageError{fmt.Sprintf("invalid --filter %q: use KEY=VALUE", kv)}
		}
		if c.Filters == nil {
			c.Filters = map[string][]string{}
		}
		c.Filters[k] = append(c.Filters[k], v)
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
var relTimeRe = regexp.MustCompile(`^([0-9]+)([mhdw])$`)

// parseFutureTime is parseTimestamp for times that lie ahead (suppress until,
// extend until): a relative value like 7d means 7 days from now.
func parseFutureTime(s string) (int64, error) {
	if m := relTimeRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return time.Now().Add(time.Duration(n) * unit).Unix(), nil
	}
	return parseTimestamp(s)
}

func parseTimestamp(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if s == "now" {
		return time.Now().Unix(), nil
	}
	// Relative: 30m, 24h, 7d, 2w mean "that long ago".
	if m := relTimeRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return time.Now().Add(-time.Duration(n) * unit).Unix(), nil
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
	return 0, &usageError{fmt.Sprintf("cannot parse timestamp %q (accepted: unix integer, RFC3339, YYYY-MM-DD, or relative like 30m, 24h, 7d)", s)}
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
	return time.Unix(n, 0).In(displayLoc).Format(time.RFC3339)
}

// displayLoc is the zone times are rendered in. Zabbix stores UTC epochs; the
// offset in the output makes every time unambiguous, DST days included.
var displayLoc = time.UTC

// setDisplayZone applies --tz: "" or UTC, "local", or an IANA zone name.
func setDisplayZone(name string) error {
	switch strings.ToLower(name) {
	case "", "utc":
		displayLoc = time.UTC
	case "local":
		displayLoc = time.Local
	default:
		loc, err := time.LoadLocation(name)
		if err != nil {
			displayLoc = time.UTC
			return fmt.Errorf("unknown time zone %q, showing UTC", name)
		}
		displayLoc = loc
	}
	return nil
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
		return cliFail(outFmt, err)
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
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use monitored or unmonitored", getHostsStatus), output.ExitUsage)
	}

	// Resolve group name → ID if provided.
	if getHostsGroup != "" {
		g, err := resources.GetGroupByName(c, getHostsGroup)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliFail(outFmt, err)
		}
		filter.GroupIDs = []string{g.GroupID}
	}

	hosts, err := resources.GetHosts(c, filter)
	if err != nil {
		return cliFail(outFmt, err)
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
			"hostid":        h.HostID,
			"host":          h.Host,
			"name":          h.Name,
			"status":        hostStatusLabel(h.Status),
			"status_raw":    h.Status,
			"available":     availableLabel(h.AvailableState()),
			"available_raw": h.AvailableState(),
			"description":   h.Description,
			"groups":        h.GroupNames(),
			"templates":     h.TemplateNames(),
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
		return cliFail(outFmt, err)
	}

	h, err := resources.GetHost(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
	}

	cols := []output.Column{
		{Key: "hostid", Header: "HOST_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "name", Header: "NAME"},
		{Key: "status", Header: "STATUS"},
		{Key: "available", Header: "AVAILABLE"},
		{Key: "groups", Header: "GROUPS"},
		{Key: "templates", Header: "TEMPLATES"},
		{Key: "description", Header: "DESCRIPTION"},
	}
	ifaces := make([]string, 0, len(h.Interfaces))
	for _, i := range h.Interfaces {
		addr := i.IP
		if addr == "" {
			addr = i.DNS
		}
		ifaces = append(ifaces, addr+":"+i.Port)
	}
	tags := make([]string, 0, len(h.Tags))
	for _, t := range h.Tags {
		if t.Value != "" {
			tags = append(tags, t.Tag+"="+t.Value)
		} else {
			tags = append(tags, t.Tag)
		}
	}
	macros := make([]string, 0, len(h.Macros))
	for _, m := range h.Macros {
		if m.Type == "1" { // secret: the API returns no value, never show one
			macros = append(macros, m.Macro+"=******")
		} else {
			macros = append(macros, m.Macro+"="+m.Value)
		}
	}
	row := map[string]any{
		"hostid":        h.HostID,
		"host":          h.Host,
		"name":          h.Name,
		"status":        hostStatusLabel(h.Status),
		"status_raw":    h.Status,
		"available":     availableLabel(h.AvailableState()),
		"available_raw": h.AvailableState(),
		"description":   h.Description,
		"groups":        h.GroupNames(),
		"templates":     h.TemplateNames(),
		"interfaces":    ifaces,
		"tags":          tags,
		"macros":        macros,
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
		return cliFail(outFmt, err)
	}

	hostID, err := resources.ResolveHostID(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
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
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use enabled or disabled", getItemsStatus), output.ExitUsage)
	}
	switch getItemsState {
	case "normal":
		filter.State = "0"
	case "unsupported":
		filter.State = "1"
	case "":
		// no filter
	default:
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --state %q: use normal or unsupported", getItemsState), output.ExitUsage)
	}

	items, err := resources.GetItems(c, filter)
	if err != nil {
		return cliFail(outFmt, err)
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
	getLatestDataLimit   int
	flagLatestdataHosts  []string
	flagLatestdataGroups []string
	flagLatestdataItem   string
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
		return cliFail(output.Format(flagOutput), err)
	}

	// Collect all host targets: positional arg + --host flags.
	hostTargets := append([]string{}, flagLatestdataHosts...)
	if len(args) > 0 {
		hostTargets = append(hostTargets, args[0])
	}

	if len(hostTargets) == 0 && len(flagLatestdataGroups) == 0 {
		return cliPrintErr(outFmt, "provide at least one host (positional or --host) or --group", output.ExitUsage)
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
		return cliFail(outFmt, err)
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
	getTriggersSearch   string
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
	f.StringVar(&getTriggersSearch, "search", "", "substring match on the trigger description")
	f.StringVar(&getTriggersSortBy, "sort-by", "priority", "sort field: priority|lastchange|description")
	getCmd.AddCommand(getTriggersCmd)
}

func runGetTriggers(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}

	filter := resources.TriggerFilter{
		SortField:    getTriggersSortBy,
		Limit:        effectiveLimit(getTriggersLimit, ctx),
		Search:       getTriggersSearch,
		OnlyProblems: getTriggersProblem,
	}

	if getTriggersHost != "" {
		id, err := resources.ResolveHostID(c, getTriggersHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliFail(outFmt, err)
		}
		filter.HostIDs = []string{id}
	}

	if getTriggersSeverity != "" {
		sev, err := parseSeverity(getTriggersSeverity)
		if err != nil {
			return cliFail(outFmt, err)
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
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use enabled or disabled", getTriggersStatus), output.ExitUsage)
	}

	triggers, err := resources.GetTriggers(c, filter)
	if err != nil {
		return cliFail(outFmt, err)
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
	getProblemsSuppressed   string
	getProblemsMaintenance  bool
	getProblemsNoOrphans    bool
	getProblemsGroupBy      string
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
	f.StringVar(&getProblemsSince, "since", "", "only problems after this time (unix, RFC3339, YYYY-MM-DD or relative like 7d)")
	f.StringVar(&getProblemsAcknowledged, "acknowledged", "", "filter: true=acknowledged only, false=unacknowledged only")
	f.StringVar(&getProblemsSuppressed, "suppressed", "", "filter: true=suppressed only, false=unsuppressed only (default: both)")
	f.BoolVar(&getProblemsMaintenance, "maintenance", false, "only problems suppressed by a maintenance window")
	f.BoolVar(&getProblemsNoOrphans, "exclude-orphans", false, "drop problems that can never recover (trigger, host or items disabled)")
	f.StringVar(&getProblemsGroupBy, "group-by", "", "group identical alerts across hosts: name")
	f.BoolVar(&getProblemsResolved, "resolved", false, "include recently-resolved problems (default: open only)")
	f.IntVar(&getProblemsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getProblemsCmd)
}

func runGetProblems(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
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
			return cliFail(outFmt, err)
		}
		filter.HostIDs = []string{id}
	}

	if getProblemsSeverity != "" {
		sev, err := parseSeverity(getProblemsSeverity)
		if err != nil {
			return cliFail(outFmt, err)
		}
		n, _ := strconv.Atoi(sev)
		filter.MinSeverity = n
	}

	if getProblemsSince != "" {
		ts, err := parseTimestamp(getProblemsSince)
		if err != nil {
			return cliFail(outFmt, err)
		}
		filter.Since = ts
	}

	if getProblemsAcknowledged != "" {
		b, err := strconv.ParseBool(getProblemsAcknowledged)
		if err != nil {
			return cliPrintErr(outFmt, fmt.Sprintf("invalid --acknowledged %q: use true or false", getProblemsAcknowledged), output.ExitUsage)
		}
		filter.Acknowledged = &b
	}

	if getProblemsSuppressed != "" {
		b, err := strconv.ParseBool(getProblemsSuppressed)
		if err != nil {
			return cliPrintErr(outFmt, fmt.Sprintf("invalid --suppressed %q: use true or false", getProblemsSuppressed), output.ExitUsage)
		}
		filter.Suppressed = &b
	}
	filter.Maintenance = getProblemsMaintenance
	filter.NoOrphans = getProblemsNoOrphans
	if getProblemsGroupBy != "" && getProblemsGroupBy != "name" {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --group-by %q: use name", getProblemsGroupBy), output.ExitUsage)
	}

	problems, err := resources.GetProblems(c, filter)
	if err != nil {
		return cliFail(outFmt, err)
	}
	if getProblemsGroupBy == "name" {
		return printProblemGroups(outFmt, problems)
	}

	cols := []output.Column{
		{Key: "eventid", Header: "EVENT_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "name", Header: "NAME"},
		{Key: "severity", Header: "SEVERITY"},
		{Key: "status", Header: "STATUS"},
		{Key: "acknowledged", Header: "ACK"},
		{Key: "suppressed", Header: "SUPPRESSED"},
		{Key: "orphan", Header: "ORPHAN"},
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
			"host":             p.Host,
			"objectid":         p.ObjectID,
			"name":             p.Name,
			"severity":         severityLabel(p.Severity),
			"severity_raw":     p.Severity,
			"status":           statusLabel,
			"acknowledged":     acknowledgedLabel(p.Acknowledged),
			"acknowledged_raw": p.Acknowledged,
			"suppressed":       p.IsSuppressed(),
			"maintenance":      p.MaintenanceSuppressed(),
			"suppressed_until": suppressedUntilLabel(p),
			"orphan":           p.Orphan,
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
	f.StringVar(&getEventsSince, "since", "", "events after this time (unix, RFC3339, YYYY-MM-DD or relative like 7d)")
	f.StringVar(&getEventsUntil, "until", "", "events before this time (unix, RFC3339, YYYY-MM-DD or relative like 7d)")
	f.IntVar(&getEventsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getEventsCmd)
}

func runGetEvents(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
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
			return cliFail(outFmt, err)
		}
		filter.HostIDs = []string{id}
	}

	if getEventsSince != "" {
		ts, err := parseTimestamp(getEventsSince)
		if err != nil {
			return cliFail(outFmt, err)
		}
		filter.Since = ts
	}
	if getEventsUntil != "" {
		ts, err := parseTimestamp(getEventsUntil)
		if err != nil {
			return cliFail(outFmt, err)
		}
		filter.Until = ts
	}

	events, err := resources.GetEvents(c, filter)
	if err != nil {
		return cliFail(outFmt, err)
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
			"eventid":          ev.EventID,
			"objectid":         ev.ObjectID,
			"host":             hostName,
			"name":             ev.Name,
			"value":            triggerValueLabel(ev.Value),
			"value_raw":        ev.Value,
			"acknowledged":     acknowledgedLabel(ev.Acknowledged),
			"acknowledged_raw": ev.Acknowledged,
			"clock":            fmtUnix(ev.Clock),
			"clock_unix":       ev.Clock,
			"hosts":            ev.Hosts,
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
		return cliFail(outFmt, err)
	}

	templates, err := resources.GetTemplates(c, resources.TemplateFilter{
		Search: getTemplatesSearch,
		Limit:  effectiveLimit(getTemplatesLimit, ctx),
	})
	if err != nil {
		return cliFail(outFmt, err)
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
		return cliFail(outFmt, err)
	}

	t, err := resources.GetTemplate(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
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
		return cliFail(outFmt, err)
	}

	groups, err := resources.GetGroups(c, resources.GroupFilter{
		Limit: effectiveLimit(getGroupsLimit, ctx),
	})
	if err != nil {
		return cliFail(outFmt, err)
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
		return cliFail(outFmt, err)
	}

	users, err := resources.GetUsers(c, resources.UserFilter{
		Limit: effectiveLimit(getUsersLimit, ctx),
	})
	if err != nil {
		return cliFail(outFmt, err)
	}

	cols := []output.Column{
		{Key: "userid", Header: "USER_ID"},
		{Key: "username", Header: "USERNAME"},
		{Key: "name", Header: "NAME"},
		{Key: "surname", Header: "SURNAME"},
		{Key: "role", Header: "ROLE"},
	}
	rows := make([]map[string]any, len(users))
	for i, u := range users {
		rows[i] = map[string]any{
			"userid":   u.UserID,
			"username": u.Username,
			"name":     u.Name,
			"surname":  u.Surname,
			"roleid":   u.RoleID,
			"role":     u.Role.Name,
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
		return cliFail(outFmt, err)
	}

	u, err := resources.GetUser(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
	}

	cols := []output.Column{
		{Key: "userid", Header: "USER_ID"},
		{Key: "username", Header: "USERNAME"},
		{Key: "name", Header: "NAME"},
		{Key: "surname", Header: "SURNAME"},
		{Key: "role", Header: "ROLE"},
		{Key: "roleid", Header: "ROLE_ID"},
	}
	row := map[string]any{
		"userid":   u.UserID,
		"username": u.Username,
		"name":     u.Name,
		"surname":  u.Surname,
		"roleid":   u.RoleID,
		"role":     u.Role.Name,
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
		return cliFail(outFmt, err)
	}

	groups, err := resources.GetUserGroups(c, resources.UserGroupFilter{
		Limit: effectiveLimit(getUserGroupsLimit, ctx),
	})
	if err != nil {
		return cliFail(outFmt, err)
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
	flagHistoryAll         bool
	flagHistoryLatest      bool
	flagHistoryMaxRows     int
	flagHistoryStep        string
	flagHistoryGapsOver    string
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
	f.StringVar(&flagHistorySince, "since", "", "start time: unix, RFC3339, YYYY-MM-DD or relative like 7d")
	f.StringVar(&flagHistoryUntil, "until", "", "end time: unix, RFC3339, YYYY-MM-DD or relative like 7d")
	f.IntVar(&flagHistoryLimit, "limit", 0, "max results")
	f.BoolVar(&flagHistoryLatest, "latest", false, "with --limit: return the newest N rows instead of the oldest (output stays oldest first)")
	f.BoolVar(&flagHistoryAll, "all", false, "page through the whole window instead of stopping at --limit")
	f.IntVar(&flagHistoryMaxRows, "max-rows", 0, "with --all: safety cap on rows (default 200000)")
	f.StringVar(&flagHistoryStep, "step", "", "downsample: keep the last value of each bucket of this size per item (e.g. 5m, 1h)")
	f.StringVar(&flagHistoryGapsOver, "gaps-over", "", "report gaps between consecutive samples longer than this (e.g. 10m) instead of the samples")
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
		return cliFail(output.Format(flagOutput), err)
	}

	if flagHistoryAroundEvent != "" {
		return runGetHistoryAroundEvent(c, outFmt, ctx)
	}

	if flagHistoryItem == "" {
		return cliPrintErr(outFmt, "--item is required (or use --around-event)", output.ExitUsage)
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
	q.All, q.MaxRows, q.Latest = flagHistoryAll, flagHistoryMaxRows, flagHistoryLatest
	if q.All {
		q.Limit = 0
	}
	var step, gapsOver time.Duration
	if flagHistoryStep != "" {
		if step, err = parseWindow(flagHistoryStep); err != nil || step <= 0 {
			return cliPrintErr(outFmt, fmt.Sprintf("invalid --step %q: use a duration like 5m, 1h", flagHistoryStep), output.ExitUsage)
		}
	}
	if flagHistoryGapsOver != "" {
		if gapsOver, err = parseWindow(flagHistoryGapsOver); err != nil || gapsOver <= 0 {
			return cliPrintErr(outFmt, fmt.Sprintf("invalid --gaps-over %q: use a duration like 10m", flagHistoryGapsOver), output.ExitUsage)
		}
		q.All = true // gap detection needs every sample
		q.Limit = 0
	}

	records, err := resources.GetHistory(c, q)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
	}
	if gapsOver > 0 {
		return printHistoryGaps(outFmt, records, gapsOver, since, until)
	}
	if step > 0 {
		records = downsampleHistory(records, step)
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
		return cliFail(outFmt, err)
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
		return cliFail(outFmt, err)
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
		return cliFail(output.Format(flagOutput), err)
	}

	f := resources.ScriptFilter{}
	if getScriptsHost != "" {
		id, err := resources.ResolveHostID(c, getScriptsHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliFail(outFmt, err)
		}
		f.HostIDs = []string{id}
	}

	scripts, err := resources.GetScripts(c, f)
	if err != nil {
		return cliFail(outFmt, err)
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
	f.StringVar(&getAlertsSince, "since", "", "alerts after this time: unix, RFC3339, YYYY-MM-DD or relative like 7d")
	f.StringVar(&getAlertsStatus, "status", "", "filter by delivery status: sent|failed")
	f.IntVar(&getAlertsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getAlertsCmd)
}

func runGetAlerts(_ *cobra.Command, _ []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliFail(output.Format(flagOutput), err)
	}

	switch getAlertsStatus {
	case "sent", "failed", "":
		// valid
	default:
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use sent or failed", getAlertsStatus), output.ExitUsage)
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
			return cliFail(outFmt, err)
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
		return cliFail(outFmt, err)
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
		return cliFail(output.Format(flagOutput), err)
	}

	hostID, err := resources.ResolveHostID(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
	}

	ifaces, err := resources.GetInterfaces(c, hostID)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
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

func suppressedUntilLabel(p resources.Problem) string {
	if !p.IsSuppressed() {
		return ""
	}
	if u := p.SuppressedUntil(); u != "" && u != "0" {
		return fmtUnix(u)
	}
	return "indefinitely"
}

// normalizeProblemName replaces the problem's host names with <host> so the
// same alert on many hosts groups together.
func normalizeProblemName(p resources.Problem) string {
	n := p.Name
	for _, h := range p.Hosts {
		if h != "" {
			n = strings.ReplaceAll(n, h, "<host>")
		}
	}
	return n
}

// printProblemGroups prints one row per (normalized name, severity).
func printProblemGroups(outFmt output.Format, problems []resources.Problem) error {
	type group struct {
		name, severity     string
		count, acked, supp int
		hosts              map[string]bool
		first, last        int64
		eventIDs           []string
		orphans            map[string]bool
	}
	order := []string{}
	groups := map[string]*group{}
	for _, p := range problems {
		name := normalizeProblemName(p)
		key := p.Severity + "\x00" + name
		g := groups[key]
		if g == nil {
			g = &group{name: name, severity: p.Severity, hosts: map[string]bool{}, orphans: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		g.count++
		if p.Acknowledged == "1" {
			g.acked++
		}
		if p.IsSuppressed() {
			g.supp++
		}
		for _, h := range p.Hosts {
			g.hosts[h] = true
		}
		if p.Orphan != "" {
			g.orphans[p.Orphan] = true
		}
		ts, _ := strconv.ParseInt(p.Clock, 10, 64)
		if g.first == 0 || ts < g.first {
			g.first = ts
		}
		if ts > g.last {
			g.last = ts
		}
		g.eventIDs = append(g.eventIDs, p.EventID)
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := groups[order[i]], groups[order[j]]
		if a.severity != b.severity {
			return a.severity > b.severity
		}
		return a.count > b.count
	})
	cols := []output.Column{
		{Key: "count", Header: "COUNT"}, {Key: "severity", Header: "SEVERITY"}, {Key: "name", Header: "NAME"},
		{Key: "hosts", Header: "HOSTS"}, {Key: "acknowledged", Header: "ACKED"}, {Key: "suppressed", Header: "SUPPRESSED"}, {Key: "last", Header: "LAST"},
	}
	rows := make([]map[string]any, 0, len(order))
	for _, k := range order {
		g := groups[k]
		hosts := make([]string, 0, len(g.hosts))
		for h := range g.hosts {
			hosts = append(hosts, h)
		}
		sort.Strings(hosts)
		orphans := make([]string, 0, len(g.orphans))
		for o := range g.orphans {
			orphans = append(orphans, o)
		}
		sort.Strings(orphans)
		rows = append(rows, map[string]any{
			"count": g.count, "severity": severityLabel(g.severity), "name": g.name, "hosts": hosts,
			"acknowledged": g.acked, "suppressed": g.supp, "orphans": orphans,
			"first": fmtUnix(strconv.FormatInt(g.first, 10)), "last": fmtUnix(strconv.FormatInt(g.last, 10)),
			"eventids": g.eventIDs,
		})
	}
	return output.Print(outFmt, cols, rows)
}
