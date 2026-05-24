package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
	"codeberg.org/mwtrigg/zbxctl/internal/api/resources"
	"codeberg.org/mwtrigg/zbxctl/internal/config"
	"codeberg.org/mwtrigg/zbxctl/internal/output"
)

// confirm prompts the user for a yes/no confirmation unless --yes was passed.
// Returns true if the operation should proceed.
func confirm(prompt string) bool {
	if flagYes {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		resp := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return resp == "y" || resp == "yes"
	}
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
	Args:  cobra.ExactArgs(1),
	RunE:  runEnableHost,
}

var disableHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Disable a host (set status to unmonitored)",
	Args:  cobra.ExactArgs(1),
	RunE:  runDisableHost,
}

func runEnableHost(cmd *cobra.Command, args []string) error {
	return runSetHostStatus(args[0], true)
}

func runDisableHost(cmd *cobra.Command, args []string) error {
	return runSetHostStatus(args[0], false)
}

func runSetHostStatus(idOrName string, enable bool) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("operator"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	h, err := resources.GetHost(c, idOrName)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	action := "disable"
	newStatus := "unmonitored"
	if enable {
		action = "enable"
		newStatus = "monitored"
	}

	if !confirm(fmt.Sprintf("%s host %q (%s)?", action, h.Name, h.Host)) {
		fmt.Fprintln(os.Stderr, "aborted")
		return nil
	}

	if enable {
		err = resources.EnableHost(c, h.HostID)
	} else {
		err = resources.DisableHost(c, h.HostID)
	}
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	return output.Print(outFmt, []output.Column{
		{Key: "hostid", Header: "HOST_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "name", Header: "NAME"},
		{Key: "status", Header: "STATUS"},
	}, []map[string]any{{
		"hostid": h.HostID,
		"host":   h.Host,
		"name":   h.Name,
		"status": newStatus,
	}})
}

// ── enable/disable trigger ────────────────────────────────────────────────────

var enableTriggerCmd = &cobra.Command{
	Use:   "trigger <id>",
	Short: "Enable a trigger",
	Args:  cobra.ExactArgs(1),
	RunE:  runEnableTrigger,
}

var disableTriggerCmd = &cobra.Command{
	Use:   "trigger <id>",
	Short: "Disable a trigger",
	Args:  cobra.ExactArgs(1),
	RunE:  runDisableTrigger,
}

func runEnableTrigger(cmd *cobra.Command, args []string) error {
	return runSetTriggerStatus(args[0], true)
}

func runDisableTrigger(cmd *cobra.Command, args []string) error {
	return runSetTriggerStatus(args[0], false)
}

func runSetTriggerStatus(id string, enable bool) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("operator"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	t, err := resources.GetTriggerByID(c, id)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	action := "disable"
	newStatus := "disabled"
	if enable {
		action = "enable"
		newStatus = "enabled"
	}

	hostName := ""
	if len(t.Hosts) > 0 {
		hostName = t.Hosts[0].Host
	}

	if !confirm(fmt.Sprintf("%s trigger %s %q (host: %s)?", action, t.TriggerID, t.Description, hostName)) {
		fmt.Fprintln(os.Stderr, "aborted")
		return nil
	}

	if enable {
		err = resources.EnableTrigger(c, t.TriggerID)
	} else {
		err = resources.DisableTrigger(c, t.TriggerID)
	}
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	return output.Print(outFmt, []output.Column{
		{Key: "triggerid", Header: "TRIGGER_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "description", Header: "DESCRIPTION"},
		{Key: "status", Header: "STATUS"},
	}, []map[string]any{{
		"triggerid":   t.TriggerID,
		"host":        hostName,
		"description": t.Description,
		"status":      newStatus,
	}})
}

// ── acknowledge ───────────────────────────────────────────────────────────────

var (
	ackMessage string
	ackClose   bool
)

var acknowledgeCmd = &cobra.Command{
	Use:   "acknowledge <event-id>",
	Short: "Acknowledge an event",
	Args:  cobra.ExactArgs(1),
	RunE:  runAcknowledge,
}

func init() {
	acknowledgeCmd.Flags().StringVar(&ackMessage, "message", "", "acknowledgement message")
	acknowledgeCmd.Flags().BoolVar(&ackClose, "close", false, "also close the problem")
}

func runAcknowledge(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("operator"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	eventID := args[0]

	prompt := fmt.Sprintf("acknowledge event %s?", eventID)
	if ackClose {
		prompt = fmt.Sprintf("acknowledge and close event %s?", eventID)
	}
	if !confirm(prompt) {
		fmt.Fprintln(os.Stderr, "aborted")
		return nil
	}

	err = resources.AcknowledgeEvent(c, resources.AcknowledgeParams{
		EventID: eventID,
		Message: ackMessage,
		Close:   ackClose,
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	row := map[string]any{
		"eventid":      eventID,
		"acknowledged": "yes",
		"closed":       fmt.Sprintf("%v", ackClose),
	}
	if ackMessage != "" {
		row["message"] = ackMessage
	}

	return output.Print(outFmt, []output.Column{
		{Key: "eventid", Header: "EVENT_ID"},
		{Key: "acknowledged", Header: "ACKNOWLEDGED"},
		{Key: "closed", Header: "CLOSED"},
	}, []map[string]any{row})
}

// ── maintenance create ────────────────────────────────────────────────────────

var (
	maintCreateHost     string
	maintCreateGroup    string
	maintCreateName     string
	maintCreateStart    string
	maintCreateDuration int
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
	f.StringVar(&maintCreateStart, "start", "", "start time: unix timestamp, RFC3339, or YYYY-MM-DD (required)")
	f.IntVar(&maintCreateDuration, "duration", 0, "duration in minutes (required)")
	f.StringVar(&maintCreateDesc, "description", "", "optional description")
	_ = maintenanceCreateCmd.MarkFlagRequired("name")
	_ = maintenanceCreateCmd.MarkFlagRequired("start")
	_ = maintenanceCreateCmd.MarkFlagRequired("duration")
}

func runMaintenanceCreate(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("operator"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	if maintCreateHost == "" && maintCreateGroup == "" {
		return cliPrintErr(outFmt, "one of --host or --group is required", output.ExitError)
	}

	startTS, err := parseTimestamp(maintCreateStart)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if startTS == 0 {
		return cliPrintErr(outFmt, "--start is required", output.ExitError)
	}
	if maintCreateDuration <= 0 {
		return cliPrintErr(outFmt, "--duration must be a positive number of minutes", output.ExitError)
	}
	endTS := startTS + int64(maintCreateDuration)*60

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
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
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
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		p.GroupIDs = []string{g.GroupID}
	}

	prompt := fmt.Sprintf("create maintenance %q from %s for %d min?",
		maintCreateName,
		time.Unix(startTS, 0).UTC().Format("2006-01-02 15:04:05"),
		maintCreateDuration)
	if !confirm(prompt) {
		fmt.Fprintln(os.Stderr, "aborted")
		return nil
	}

	id, err := resources.CreateMaintenance(c, p)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	return output.Print(outFmt, []output.Column{
		{Key: "maintenanceid", Header: "MAINTENANCE_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "active_since", Header: "START"},
		{Key: "active_till", Header: "END"},
	}, []map[string]any{{
		"maintenanceid": id,
		"name":          maintCreateName,
		"active_since":  time.Unix(startTS, 0).UTC().Format("2006-01-02 15:04:05"),
		"active_till":   time.Unix(endTS, 0).UTC().Format("2006-01-02 15:04:05"),
		"active_since_unix": startTS,
		"active_till_unix":  endTS,
	}})
}

// ── maintenance delete ────────────────────────────────────────────────────────

var maintenanceDeleteCmd = &cobra.Command{
	Use:   "delete <id|name>",
	Short: "Delete a maintenance window",
	Args:  cobra.ExactArgs(1),
	RunE:  runMaintenanceDelete,
}

func runMaintenanceDelete(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("operator"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	m, err := resources.ResolveMaintenance(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	if !confirm(fmt.Sprintf("delete maintenance %q (%s)?", m.Name, m.MaintenanceID)) {
		fmt.Fprintln(os.Stderr, "aborted")
		return nil
	}

	if err := resources.DeleteMaintenance(c, m.MaintenanceID); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	return output.Print(outFmt, []output.Column{
		{Key: "maintenanceid", Header: "MAINTENANCE_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "status", Header: "STATUS"},
	}, []map[string]any{{
		"maintenanceid": m.MaintenanceID,
		"name":          m.Name,
		"status":        "deleted",
	}})
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
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	maintenances, err := resources.GetMaintenances(c, resources.MaintenanceFilter{
		Limit: effectiveLimit(maintenanceListLimit, ctx),
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "maintenanceid", Header: "MAINTENANCE_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "active_since", Header: "START"},
		{Key: "active_till", Header: "END"},
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
)

var createCmd = &cobra.Command{Use: "create", Short: "Create resources"}
var createHostCmd = &cobra.Command{
	Use:   "host",
	Short: "Create a new Zabbix host",
	RunE:  runCreateHost,
}

var updateCmd = &cobra.Command{Use: "update", Short: "Update resources"}
var updateHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Update a Zabbix host",
	Args:  cobra.ExactArgs(1),
	RunE:  runUpdateHost,
}

var deleteCmd = &cobra.Command{Use: "delete", Short: "Delete resources"}
var deleteHostCmd = &cobra.Command{
	Use:   "host <id|name>",
	Short: "Delete a Zabbix host",
	Args:  cobra.ExactArgs(1),
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
	g.StringArrayVar(&flagUpdateHostRemoveTemplates, "remove-template", nil, "template to unlink (repeatable)")
	updateCmd.AddCommand(updateHostCmd)

	deleteCmd.AddCommand(deleteHostCmd)
}

func runCreateHost(_ *cobra.Command, _ []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
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
		return cliPrintErr(outFmt, "invalid --interface-type; use agent|snmp|ipmi|jmx", output.ExitError)
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
		fmt.Fprintln(os.Stderr, "cancelled")
		return nil
	}

	host, err := resources.CreateHost(c, in)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
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

func runUpdateHost(_ *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	if flagUpdateHostVisibleName == "" && flagUpdateHostDescription == "" &&
		len(flagUpdateHostAddGroups) == 0 && len(flagUpdateHostRemoveGroups) == 0 &&
		len(flagUpdateHostAddTemplates) == 0 && len(flagUpdateHostRemoveTemplates) == 0 {
		return cliPrintErr(outFmt, "no update flags provided; nothing to do", output.ExitError)
	}

	hostID, err := resources.ResolveHostID(c, args[0])
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
	}

	in := resources.UpdateHostInput{
		HostID:      hostID,
		VisibleName: flagUpdateHostVisibleName,
		Description: flagUpdateHostDescription,
	}

	for _, grp := range flagUpdateHostAddGroups {
		id, err := resources.ResolveGroupID(c, grp)
		if err != nil {
			return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", grp, err), output.ExitNotFound)
		}
		in.AddGroupIDs = append(in.AddGroupIDs, id)
	}
	for _, grp := range flagUpdateHostRemoveGroups {
		id, err := resources.ResolveGroupID(c, grp)
		if err != nil {
			return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", grp, err), output.ExitNotFound)
		}
		in.RemoveGroupIDs = append(in.RemoveGroupIDs, id)
	}
	for _, t := range flagUpdateHostAddTemplates {
		id, err := resources.ResolveTemplateID(c, t)
		if err != nil {
			return cliPrintErr(outFmt, fmt.Sprintf("template %q: %s", t, err), output.ExitNotFound)
		}
		in.AddTemplateIDs = append(in.AddTemplateIDs, id)
	}
	for _, t := range flagUpdateHostRemoveTemplates {
		id, err := resources.ResolveTemplateID(c, t)
		if err != nil {
			return cliPrintErr(outFmt, fmt.Sprintf("template %q: %s", t, err), output.ExitNotFound)
		}
		in.RemoveTemplateIDs = append(in.RemoveTemplateIDs, id)
	}

	if !confirm(fmt.Sprintf("update host %q?", args[0])) {
		fmt.Fprintln(os.Stderr, "cancelled")
		return nil
	}

	host, err := resources.UpdateHost(c, in)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	if outFmt == output.FormatTable {
		fmt.Printf("updated host %s (id: %s)\n", host.Host, host.HostID)
		return nil
	}
	return printWriteResult(outFmt, map[string]any{
		"hostid": host.HostID,
		"host":   host.Host,
		"name":   host.Name,
		"status": hostStatusLabel(host.Status),
	})
}

func runDeleteHost(_ *cobra.Command, args []string) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("write"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	host, err := resources.GetHost(c, args[0])
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
	}

	if !confirm(fmt.Sprintf("delete host %q (id: %s)?", host.Host, host.HostID)) {
		fmt.Fprintln(os.Stderr, "cancelled")
		return nil
	}

	_, err = resources.DeleteHost(c, host.HostID)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	if outFmt == output.FormatTable {
		fmt.Printf("deleted host %s (id: %s)\n", host.Host, host.HostID)
		return nil
	}
	return printWriteResult(outFmt, map[string]any{
		"hostid": host.HostID,
		"host":   host.Host,
		"name":   host.Name,
		"status": hostStatusLabel(host.Status),
	})
}
