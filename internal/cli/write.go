package cli

import (
	"bufio"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api"
	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/config"
	"github.com/mwtrigg/zbxctl/internal/output"
)

// confirm gates every state-changing command. Without --apply (or its alias
// --yes) it prints what would happen, changes nothing, and returns false.
func confirm(prompt string) bool {
	if flagApply {
		return true
	}
	what := strings.TrimSuffix(prompt, "?")
	_ = output.Print(output.Format(flagOutput),
		[]output.Column{{Key: "would", Header: "WOULD"}, {Key: "applied", Header: "APPLIED"}},
		[]map[string]any{{"would": what, "applied": false, "dry_run": true}})
	return false
}

// writeClient is like newClient but skips the config context return value.
func writeClient() (*api.Client, output.Format, *config.Context, error) {
	return newClient()
}

// printWriteResult emits a single-row result for a write operation.
func printWriteResult(outFmt output.Format, fields map[string]any) error {
	cols := make([]output.Column, 0, len(fields))
	for k := range fields {
		cols = append(cols, output.Column{Key: k, Header: strings.ToUpper(k)})
	}
	return output.Print(outFmt, cols, []map[string]any{fields})
}

// ── enable/disable host ───────────────────────────────────────────────────────

var enableHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Enable a host (set status to monitored)",
	Args:  cobra.ArbitraryArgs,
	RunE:  runEnableHost,
}

var disableHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Disable a host (set status to unmonitored)",
	Args:  cobra.ArbitraryArgs,
	RunE:  runDisableHost,
}

func runEnableHost(cmd *cobra.Command, args []string) error {
	return runBulk(cmd, args, hostStatusSpec(true))
}

func runDisableHost(cmd *cobra.Command, args []string) error {
	return runBulk(cmd, args, hostStatusSpec(false))
}

// ── enable/disable trigger ────────────────────────────────────────────────────

var enableTriggerCmd = &cobra.Command{
	Use:   "trigger <id>",
	Short: "Enable a trigger",
	Args:  cobra.ArbitraryArgs,
	RunE:  runEnableTrigger,
}

var disableTriggerCmd = &cobra.Command{
	Use:   "trigger <id>",
	Short: "Disable a trigger",
	Args:  cobra.ArbitraryArgs,
	RunE:  runDisableTrigger,
}

func runEnableTrigger(cmd *cobra.Command, args []string) error {
	return runBulk(cmd, args, triggerStatusSpec(true))
}

func runDisableTrigger(cmd *cobra.Command, args []string) error {
	return runBulk(cmd, args, triggerStatusSpec(false))
}

// ── acknowledge ───────────────────────────────────────────────────────────────

var (
	ackMessage       string
	ackClose         bool
	ackUnack         bool
	ackSuppress      bool
	ackUnsuppress    bool
	ackSuppressUntil string
	ackSeverity      string
)

var acknowledgeCmd = &cobra.Command{
	Use:   "acknowledge <event-id>",
	Short: "Acknowledge an event (and optionally close it or change its severity); see also unacknowledge, suppress, unsuppress",
	Args:  cobra.ArbitraryArgs,
	RunE:  runAcknowledge,
}

func init() {
	acknowledgeCmd.Flags().StringVar(&ackMessage, "message", "", "acknowledgement message")
	acknowledgeCmd.Flags().BoolVar(&ackClose, "close", false, "also close the problem")
	acknowledgeCmd.Flags().BoolVar(&ackUnack, "unacknowledge", false, "remove the acknowledgement instead")
	acknowledgeCmd.Flags().BoolVar(&ackSuppress, "suppress", false, "suppress the problem (hide it from the problem list)")
	acknowledgeCmd.Flags().StringVar(&ackSuppressUntil, "until", "", "with --suppress: suppress until this time (unix, RFC3339, YYYY-MM-DD, or from now like 24h, 7d); default indefinitely")
	acknowledgeCmd.Flags().BoolVar(&ackUnsuppress, "unsuppress", false, "lift a suppression")
	for flag, verb := range map[string]string{"unacknowledge": "unacknowledge", "suppress": "suppress", "unsuppress": "unsuppress", "until": "suppress --until"} {
		_ = acknowledgeCmd.Flags().MarkDeprecated(flag, "use 'zbxctl "+verb+"' instead")
	}
	acknowledgeCmd.Flags().StringVar(&ackSeverity, "severity", "", "also change the problem severity (0-5 or name)")
}

// parseSeverityFlag accepts 0-5 or a severity name.
func parseSeverityFlag(s string) (int, bool) {
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 5 {
		return n, true
	}
	for i, name := range []string{"not_classified", "information", "warning", "average", "high", "disaster"} {
		if strings.EqualFold(s, name) {
			return i, true
		}
	}
	return 0, false
}

// ── maintenance create ────────────────────────────────────────────────────────

var (
	maintCreateHost     string
	maintCreateGroup    string
	maintCreateName     string
	maintCreateStart    string
	maintCreateDuration string
	maintCreateDesc     string
)

var maintenanceCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a maintenance window",
	RunE:  runMaintenanceCreate,
}

func init() {
	f := maintenanceCreateCmd.Flags()
	f.StringVar(&maintCreateHost, "host", "", "host name or ID to put in maintenance")
	f.StringVar(&maintCreateGroup, "group", "", "host group name or ID to put in maintenance")
	f.StringVar(&maintCreateName, "name", "", "maintenance window name (required)")
	f.StringVar(&maintCreateStart, "start", "now", "start time: now, unix, RFC3339, YYYY-MM-DD")
	f.StringVar(&maintCreateDuration, "duration", "", "how long: 90 (minutes), 30m, 2h, 1d (required)")
	f.StringVar(&maintCreateDesc, "description", "", "optional description")
	_ = maintenanceCreateCmd.MarkFlagRequired("name")
	_ = maintenanceCreateCmd.MarkFlagRequired("duration")
}

func runMaintenanceCreate(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if err := ctx.CheckPermission("operator"); err != nil {
		return cliFail(outFmt, err)
	}

	if maintCreateHost == "" && maintCreateGroup == "" {
		return cliPrintErr(outFmt, "one of --host or --group is required", output.ExitUsage)
	}

	startTS, err := parseTimestamp(maintCreateStart)
	if err != nil {
		return cliFail(outFmt, err)
	}
	dur, err := parseDurationFlexible(maintCreateDuration)
	if err != nil || dur <= 0 {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --duration %q: use minutes (90) or 30m, 2h, 1d", maintCreateDuration), output.ExitUsage)
	}
	endTS := startTS + int64(dur.Seconds())

	p := resources.CreateMaintenanceParams{
		Name:        maintCreateName,
		ActiveSince: startTS,
		ActiveTill:  endTS,
		Description: maintCreateDesc,
	}

	if maintCreateHost != "" {
		hostID, err := resources.ResolveHostID(c, maintCreateHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliFail(outFmt, err)
		}
		p.HostIDs = []string{hostID}
	}

	if maintCreateGroup != "" {
		g, err := resources.GetGroupByName(c, maintCreateGroup)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliFail(outFmt, err)
		}
		p.GroupIDs = []string{g.GroupID}
	}

	prompt := fmt.Sprintf("create maintenance %q from %s to %s?",
		maintCreateName,
		fmtUnix(strconv.FormatInt(startTS, 10)),
		fmtUnix(strconv.FormatInt(endTS, 10)))
	if !confirm(prompt) {
		return nil
	}

	id, err := resources.CreateMaintenance(c, p)
	if err != nil {
		return cliFail(outFmt, err)
	}

	return output.Print(outFmt, []output.Column{
		{Key: "maintenanceid", Header: "MAINTENANCE_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "active_since", Header: "START"},
		{Key: "active_till", Header: "END"},
	}, []map[string]any{{
		"maintenanceid":     id,
		"name":              maintCreateName,
		"active_since":      time.Unix(startTS, 0).UTC().Format(time.RFC3339),
		"active_till":       time.Unix(endTS, 0).UTC().Format(time.RFC3339),
		"active_since_unix": startTS,
		"active_till_unix":  endTS,
	}})
}

// ── maintenance delete ────────────────────────────────────────────────────────

var maintenanceDeleteCmd = &cobra.Command{
	Use:   "delete <id|name>",
	Short: "Delete a maintenance window",
	Args:  cobra.ArbitraryArgs,
	RunE:  runMaintenanceDelete,
}

// ── maintenance get ───────────────────────────────────────────────────────────

var maintenanceListLimit int

var maintenanceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List maintenance windows",
	RunE:  runMaintenanceList,
}

func init() {
	maintenanceListCmd.Flags().IntVar(&maintenanceListLimit, "limit", 0, "max results")
}

func runMaintenanceList(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(outFmt, err)
	}

	maintenances, err := resources.GetMaintenances(c, resources.MaintenanceFilter{
		Limit: effectiveLimit(maintenanceListLimit, ctx),
	})
	if err != nil {
		return cliFail(outFmt, err)
	}

	cols := []output.Column{
		{Key: "maintenanceid", Header: "MAINTENANCE_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "active_since", Header: "START"},
		{Key: "active_till", Header: "END"},
		{Key: "state", Header: "STATE"},
		{Key: "hosts", Header: "HOSTS"},
		{Key: "groups", Header: "GROUPS"},
	}
	rows := make([]map[string]any, len(maintenances))
	for i, m := range maintenances {
		rows[i] = map[string]any{
			"maintenanceid":     m.MaintenanceID,
			"name":              m.Name,
			"active_since":      fmtUnix(m.ActiveSince),
			"active_since_unix": m.ActiveSince,
			"active_till":       fmtUnix(m.ActiveTill),
			"active_till_unix":  m.ActiveTill,
			"description":       m.Description,
			"maintenance_type":  m.MaintenanceType,
			"state":             maintenanceState(m),
			"hosts":             m.HostNames(),
			"groups":            m.GroupNames(),
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── parent commands + registration ───────────────────────────────────────────

var enableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable a Zabbix object",
}

var disableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable a Zabbix object",
}

var maintenanceCmd = &cobra.Command{
	Use:   "maintenance",
	Short: "Manage maintenance windows",
}

func init() {
	enableCmd.AddCommand(enableHostCmd)
	enableCmd.AddCommand(enableTriggerCmd)

	disableCmd.AddCommand(disableHostCmd)
	disableCmd.AddCommand(disableTriggerCmd)

	maintenanceCmd.AddCommand(maintenanceCreateCmd)
	maintenanceCmd.AddCommand(maintenanceDeleteCmd)
	maintenanceCmd.AddCommand(maintenanceListCmd)
}

// ── create host ───────────────────────────────────────────────────────────────

var (
	// create host flags
	flagCreateHostName          string
	flagCreateHostGroups        []string
	flagCreateHostIP            string
	flagCreateHostDNS           string
	flagCreateHostPort          string
	flagCreateHostIfaceType     string
	flagCreateHostVisibleName   string
	flagCreateHostDescription   string
	flagCreateHostSNMPVersion   string
	flagCreateHostSNMPCommunity string

	// update host flags
	flagUpdateHostVisibleName     string
	flagUpdateHostDescription     string
	flagUpdateHostAddGroups       []string
	flagUpdateHostRemoveGroups    []string
	flagUpdateHostAddTemplates    []string
	flagUpdateHostRemoveTemplates []string
	flagUpdateHostClearTemplates  []string
	flagUpdateHostProxy           string
	flagUpdateHostProxyGroup      string
)

var createCmd = &cobra.Command{Use: "create", Short: "Create resources"}
var createHostCmd = &cobra.Command{
	Use:   "host",
	Short: "Create a new Zabbix host",
	RunE:  runCreateHost,
}

var updateCmd = &cobra.Command{Use: "update", Short: "Update resources"}
var updateHostCmd = &cobra.Command{
	Use:   "host <id|name>...",
	Short: "Update one or more Zabbix hosts",
	Args:  cobra.ArbitraryArgs,
	RunE:  runUpdateHost,
}

var deleteCmd = &cobra.Command{Use: "delete", Short: "Delete resources"}
var deleteHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Delete a Zabbix host",
	Args:  cobra.ArbitraryArgs,
	RunE:  runDeleteHost,
}

func init() {
	f := createHostCmd.Flags()
	f.StringVar(&flagCreateHostName, "name", "", "technical host name (required)")
	f.StringArrayVar(&flagCreateHostGroups, "group", nil, "host group name or ID (required, repeatable)")
	f.StringVar(&flagCreateHostIP, "ip", "", "interface IP address")
	f.StringVar(&flagCreateHostDNS, "dns", "", "interface DNS name")
	f.StringVar(&flagCreateHostPort, "port", "10050", "interface port")
	f.StringVar(&flagCreateHostIfaceType, "interface-type", "agent", "interface type: agent|snmp|ipmi|jmx")
	f.StringVar(&flagCreateHostVisibleName, "visible-name", "", "visible name (defaults to --name)")
	f.StringVar(&flagCreateHostDescription, "description", "", "host description")
	f.StringVar(&flagCreateHostSNMPVersion, "snmp-version", "2", "SNMP version: 1|2|3 (SNMP only)")
	f.StringVar(&flagCreateHostSNMPCommunity, "snmp-community", "public", "SNMP community (v1/v2c only)")
	_ = createHostCmd.MarkFlagRequired("name")
	_ = createHostCmd.MarkFlagRequired("group")
	createCmd.AddCommand(createHostCmd)

	g := updateHostCmd.Flags()
	g.StringVar(&flagUpdateHostVisibleName, "visible-name", "", "new visible name")
	g.StringVar(&flagUpdateHostDescription, "description", "", "new description")
	g.StringArrayVar(&flagUpdateHostAddGroups, "add-group", nil, "host group to add (repeatable)")
	g.StringArrayVar(&flagUpdateHostRemoveGroups, "remove-group", nil, "host group to remove (repeatable)")
	g.StringArrayVar(&flagUpdateHostAddTemplates, "add-template", nil, "template to link (repeatable)")
	g.StringArrayVar(&flagUpdateHostRemoveTemplates, "remove-template", nil, "template to unlink; the items, triggers and graphs it gave the host stay (repeatable)")
	g.StringArrayVar(&flagUpdateHostClearTemplates, "clear-template", nil, "template to unlink AND clear: deletes the items, triggers, graphs and history it gave the host, which cannot be undone (repeatable)")
	g.StringVar(&flagUpdateHostProxy, "proxy", "", "monitor the host through this proxy (name or ID), or none to monitor it from the server")
	g.StringVar(&flagUpdateHostProxyGroup, "proxy-group", "", "monitor the host through this proxy group (name or ID)")
	addBulkFlags(updateHostCmd)
	updateCmd.AddCommand(updateHostCmd)

	deleteCmd.AddCommand(deleteHostCmd)
}

func runCreateHost(_ *cobra.Command, _ []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(output.Format(flagOutput), err)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliFail(outFmt, err)
	}

	if flagCreateHostIP == "" && flagCreateHostDNS == "" {
		return cliPrintErr(outFmt, "provide --ip or --dns", output.ExitError)
	}

	groupIDs := make([]string, 0, len(flagCreateHostGroups))
	for _, grp := range flagCreateHostGroups {
		id, err := resources.ResolveGroupID(c, grp)
		if err != nil {
			return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", grp, err), output.ExitNotFound)
		}
		groupIDs = append(groupIDs, id)
	}

	ifaceTypeMap := map[string]int{"agent": 1, "snmp": 2, "ipmi": 3, "jmx": 4}
	ifaceType, ok := ifaceTypeMap[flagCreateHostIfaceType]
	if !ok {
		return cliPrintErr(outFmt, "invalid --interface-type; use agent|snmp|ipmi|jmx", output.ExitUsage)
	}
	useIP := 0
	if flagCreateHostIP != "" {
		useIP = 1
	}

	in := resources.CreateHostInput{
		Host:        flagCreateHostName,
		VisibleName: flagCreateHostVisibleName,
		Description: flagCreateHostDescription,
		GroupIDs:    groupIDs,
		Interface: resources.InterfaceInput{
			Type:          ifaceType,
			UseIP:         useIP,
			IP:            flagCreateHostIP,
			DNS:           flagCreateHostDNS,
			Port:          flagCreateHostPort,
			SNMPVersion:   flagCreateHostSNMPVersion,
			SNMPCommunity: flagCreateHostSNMPCommunity,
		},
	}

	if !confirm(fmt.Sprintf("create host %q?", flagCreateHostName)) {
		return nil
	}

	host, err := resources.CreateHost(c, in)
	if err != nil {
		return cliFail(outFmt, err)
	}

	if outFmt == output.FormatTable {
		fmt.Printf("created host %s (id: %s)\n", host.Host, host.HostID)
		return nil
	}
	return printWriteResult(outFmt, map[string]any{
		"hostid": host.HostID,
		"host":   host.Host,
		"name":   host.Name,
		"status": hostStatusLabel(host.Status),
	})
}

// generateToken returns a 6-character random alphanumeric string for run script confirmation.
func generateToken() (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", fmt.Errorf("reading from CSPRNG: %w", err)
		}
		b[i] = chars[n.Int64()]
	}
	return string(b), nil
}

// ── run script ────────────────────────────────────────────────────────────────

var runScriptHost string

var runScriptCmd = &cobra.Command{
	Use:   "script <name-or-id>",
	Short: "Execute a Zabbix script on a host",
	Long: `Execute a script from the Zabbix script library on a monitored host.

SECURITY: This command executes code on a monitored host. It requires:
  - allowed_scripts in the active context config (run script is blocked otherwise)
  - Manual token confirmation (--yes has no effect on this command)

Recommended: use a read-only API token for agent contexts. Enable run script
only for pre-reviewed diagnostic scripts (ping, traceroute, read-only checks).`,
	Args: cobra.ExactArgs(1),
	RunE: runRunScript,
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute remote operations",
}

func init() {
	runScriptCmd.Flags().StringVar(&runScriptHost, "host", "", "host name or ID (required)")
	_ = runScriptCmd.MarkFlagRequired("host")
	runCmd.AddCommand(runScriptCmd)
}

func runRunScript(_ *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliFail(output.Format(flagOutput), err)
	}

	// allowed_scripts check — independent of permission tier.
	if len(ctx.AllowedScripts) == 0 {
		return cliPrintErr(outFmt,
			"run script is blocked: context has no allowed_scripts configured\n"+
				"Add allowed_scripts to this context in ~/.config/zbxctl/config.yaml",
			output.ExitError)
	}

	nameOrID := args[0]

	// Resolve host.
	hostID, err := resources.ResolveHostID(c, runScriptHost)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
	}

	// Resolve script.
	script, err := resources.GetScriptByNameOrID(c, nameOrID, hostID)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
	}

	// Check against allowed_scripts allowlist (match on name or ID).
	allowed := false
	for _, s := range ctx.AllowedScripts {
		if s == script.Name || s == script.ScriptID {
			allowed = true
			break
		}
	}
	if !allowed {
		fmt.Fprintf(os.Stderr, "error: script %q is not in allowed_scripts for this context\n", script.Name)
		return &cliErr{code: output.ExitError}
	}

	// Always print script details — no flag can suppress this output.
	fmt.Fprintf(os.Stderr, "\n  Script execution details:\n")
	fmt.Fprintf(os.Stderr, "   Script:     %s (id: %s)\n", script.Name, script.ScriptID)
	fmt.Fprintf(os.Stderr, "   Type:       %s\n", resources.ScriptTypeLabel(script.Type))
	fmt.Fprintf(os.Stderr, "   Execute on: %s\n", resources.ExecuteOnLabel(script.ExecuteOn))
	fmt.Fprintf(os.Stderr, "   Host:       %s (id: %s)\n", runScriptHost, hostID)
	if script.Command != "" {
		fmt.Fprintf(os.Stderr, "   Command:\n")
		for _, line := range strings.Split(script.Command, "\n") {
			fmt.Fprintf(os.Stderr, "     %s\n", line)
		}
	}
	fmt.Fprintln(os.Stderr)

	// Token confirmation — --yes has no effect.
	token, err := generateToken()
	if err != nil {
		return fmt.Errorf("generating confirmation token: %w", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	fmt.Fprintf(os.Stderr, "Confirmation token (expires in 60s): %s\n", token)
	fmt.Fprintf(os.Stderr, "Type the token above to execute: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		fmt.Fprintln(os.Stderr, "\naborted")
		return &cliErr{code: output.ExitError}
	}
	input := strings.TrimSpace(scanner.Text())
	if time.Now().After(deadline) {
		return cliPrintErr(outFmt, "token expired (60s TTL exceeded)", output.ExitError)
	}
	if input != token {
		return cliPrintErr(outFmt, "token mismatch: aborted", output.ExitError)
	}

	// Execute.
	result, err := resources.RunScript(c, script.ScriptID, hostID)
	if err != nil {
		return cliFail(outFmt, err)
	}

	// Populate readable host/script name on result.
	result.ScriptName = script.Name
	result.Host = runScriptHost

	return output.Print(outFmt, []output.Column{
		{Key: "scriptid", Header: "SCRIPT_ID"},
		{Key: "script_name", Header: "SCRIPT"},
		{Key: "hostid", Header: "HOST_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "response", Header: "RESPONSE"},
		{Key: "value", Header: "OUTPUT"},
	}, []map[string]any{{
		"scriptid":    result.ScriptID,
		"script_name": result.ScriptName,
		"hostid":      result.HostID,
		"host":        result.Host,
		"response":    result.Response,
		"value":       result.Value,
	}})
}

func runMaintenanceDelete(cmd *cobra.Command, args []string) error {
	return runBulk(cmd, args, maintenanceDeleteSpec())
}

func runDeleteHost(cmd *cobra.Command, args []string) error {
	return runBulk(cmd, args, deleteHostSpec())
}

func runAcknowledge(cmd *cobra.Command, args []string) error {
	outFmt := output.Format(flagOutput)
	p := resources.AcknowledgeParams{Message: ackMessage, Close: ackClose, Severity: -1, Action: resources.ActionAcknowledge}
	exclusive := 0
	for _, on := range []bool{ackUnack, ackSuppress, ackUnsuppress} {
		if on {
			exclusive++
		}
	}
	if exclusive > 1 {
		return cliPrintErr(outFmt, "--unacknowledge, --suppress and --unsuppress are mutually exclusive", output.ExitUsage)
	}
	if exclusive == 1 && ackClose {
		return cliPrintErr(outFmt, "--close only applies when acknowledging", output.ExitUsage)
	}
	if ackSuppressUntil != "" && !ackSuppress {
		return cliPrintErr(outFmt, "--until only applies with --suppress", output.ExitUsage)
	}
	verb := "acknowledge"
	switch {
	case ackUnack:
		p.Action, verb = resources.ActionUnacknowledge, "unacknowledge"
	case ackSuppress:
		p.Action, verb = resources.ActionSuppress, "suppress"
		if ackSuppressUntil != "" {
			ts, err := parseFutureTime(ackSuppressUntil)
			if err != nil {
				return cliFail(outFmt, err)
			}
			p.SuppressUntil = ts
		}
	case ackUnsuppress:
		p.Action, verb = resources.ActionUnsuppress, "unsuppress"
	case ackClose:
		verb = "acknowledge and close"
	}
	if ackSeverity != "" {
		sev, ok := parseSeverityFlag(ackSeverity)
		if !ok {
			return cliPrintErr(outFmt, fmt.Sprintf("invalid --severity %q: use 0-5 or a severity name", ackSeverity), output.ExitUsage)
		}
		p.Severity = sev
	}
	return runBulk(cmd, args, ackSpec(p, verb))
}

// maintenanceState is upcoming, active or expired relative to now.
func maintenanceState(m resources.Maintenance) string {
	since, _ := strconv.ParseInt(m.ActiveSince, 10, 64)
	till, _ := strconv.ParseInt(m.ActiveTill, 10, 64)
	now := time.Now().Unix()
	switch {
	case now < since:
		return "upcoming"
	case now > till:
		return "expired"
	}
	return "active"
}

var (
	maintBy    string
	maintUntil string
)

func newMaintenanceEndCmd(verb string) *cobra.Command {
	c := &cobra.Command{
		Use:   verb + " <id|name>...",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a one-time maintenance window (moves its end; recurring windows are refused)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			outFmt := output.Format(flagOutput)
			if (maintBy == "") == (maintUntil == "") {
				return cliPrintErr(outFmt, "give exactly one of --by and --until", output.ExitUsage)
			}
			if verb == "shorten" && maintUntil != "" {
				return cliPrintErr(outFmt, "--until is only for extend; shorten takes --by", output.ExitUsage)
			}
			var by time.Duration
			var until int64
			if maintBy != "" {
				d, err := parseWindow(maintBy)
				if err != nil || d <= 0 {
					return cliPrintErr(outFmt, fmt.Sprintf("invalid --by %q: use a duration like 30m, 2h, 1d", maintBy), output.ExitUsage)
				}
				by = d
			} else {
				ts, err := parseFutureTime(maintUntil)
				if err != nil {
					return cliFail(outFmt, err)
				}
				until = ts
			}
			return runBulk(cmd, args, maintenanceEndSpec(verb, by, until))
		},
	}
	c.Flags().StringVar(&maintBy, "by", "", "how much to move the end, e.g. 30m, 2h, 1d")
	if verb == "extend" {
		c.Flags().StringVar(&maintUntil, "until", "", "set the new end: unix, RFC3339, YYYY-MM-DD, or relative to now like 2h")
	}
	addBulkFlags(c)
	return c
}

func init() {
	maintenanceCmd.AddCommand(newMaintenanceEndCmd("extend"), newMaintenanceEndCmd("shorten"))
}

// parseDurationFlexible reads 90 (minutes), 30m, 2h, 1d, 1w or a Go duration.
func parseDurationFlexible(s string) (time.Duration, error) {
	if isNumericStr(s) {
		n, _ := strconv.Atoi(s)
		return time.Duration(n) * time.Minute, nil
	}
	return parseWindow(s)
}
