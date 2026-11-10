package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var diagSince string

var diagnoseHostCmd = &cobra.Command{
	Use:   "host <name|id>",
	Short: "Single-call composite snapshot for agent-driven triage",
	Long: `Returns a composite snapshot of host state: open problems, recent events,
latest metric values, broken items, and interface availability.

Output is always JSON or JSONL (table falls back to JSON).`,
	Args: cobra.ExactArgs(1),
	RunE: runDiagnoseHost,
}

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose",
	Short: "Composite diagnostic commands",
}

func init() {
	diagnoseHostCmd.Flags().StringVar(&diagSince, "since", "24h", "event window before now (e.g. 1h, 6h, 24h)")
	diagnoseCmd.AddCommand(diagnoseHostCmd)
}

func runDiagnoseHost(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}

	// Table is not usable for nested composite output.
	if outFmt == output.FormatTable {
		outFmt = output.FormatJSON
	}

	target := args[0]

	hostID, err := resources.ResolveHostID(c, target)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliFail(outFmt, err)
	}

	sinceDur, err := parseWindow(diagSince)
	if err != nil {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --since %q: use a duration like 30m, 24h, 7d, 2w", diagSince), output.ExitUsage)
	}
	sinceTS := time.Now().Add(-sinceDur).Unix()

	var (
		hostObj     *resources.Host
		ifaces      []resources.Interface
		problems    []resources.Problem
		evts        []resources.Event
		latestItems []resources.Item
		brokenItems []resources.Item
	)

	g, _ := errgroup.WithContext(cmd.Context())

	g.Go(func() error {
		h, err := resources.GetHost(c, hostID)
		if err != nil {
			return err
		}
		hostObj = h
		return nil
	})
	g.Go(func() error {
		ii, err := resources.GetInterfaces(c, hostID)
		if err != nil {
			return nil // non-fatal
		}
		ifaces = ii
		return nil
	})
	g.Go(func() error {
		ps, err := resources.GetProblems(c, resources.ProblemFilter{
			HostIDs:     []string{hostID},
			MinSeverity: -1,
		})
		if err != nil {
			return err
		}
		problems = ps
		return nil
	})
	g.Go(func() error {
		evs, err := resources.GetEvents(c, resources.EventFilter{
			HostIDs: []string{hostID},
			Since:   sinceTS,
		})
		if err != nil {
			return err
		}
		evts = evs
		return nil
	})
	g.Go(func() error {
		items, err := resources.GetLatestData(c, resources.ItemFilter{
			HostIDs: []string{hostID},
		})
		if err != nil {
			return err
		}
		latestItems = items
		return nil
	})
	g.Go(func() error {
		items, err := resources.GetItems(c, resources.ItemFilter{
			HostIDs: []string{hostID},
			State:   "1",
		})
		if err != nil {
			return err
		}
		brokenItems = items
		return nil
	})

	if err := g.Wait(); err != nil {
		return cliFail(outFmt, err)
	}

	// Build interface availability for host section.
	ifaceAvail := "unknown"
	for _, iface := range ifaces {
		if iface.Main == "1" && iface.TypeRaw == "1" {
			ifaceAvail = iface.Available
			break
		}
	}

	hostSection := map[string]any{
		"hostid":          hostObj.HostID,
		"host":            hostObj.Host,
		"name":            hostObj.Name,
		"status":          hostStatusLabel(hostObj.Status),
		"available":       availableLabel(hostObj.AvailableState()),
		"iface_available": ifaceAvail,
	}

	openProblems := make([]map[string]any, len(problems))
	for i, p := range problems {
		openProblems[i] = map[string]any{
			"eventid":      p.EventID,
			"name":         p.Name,
			"severity":     severityLabel(p.Severity),
			"severity_raw": p.Severity,
			"clock":        fmtUnix(p.Clock),
			"clock_unix":   p.Clock,
			"acknowledged": acknowledgedLabel(p.Acknowledged),
		}
	}

	recentEvents := make([]map[string]any, len(evts))
	for i, ev := range evts {
		hostName := ""
		if len(ev.Hosts) > 0 {
			hostName = ev.Hosts[0].Host
		}
		recentEvents[i] = map[string]any{
			"eventid":    ev.EventID,
			"host":       hostName,
			"name":       ev.Name,
			"value":      triggerValueLabel(ev.Value),
			"clock":      fmtUnix(ev.Clock),
			"clock_unix": ev.Clock,
		}
	}

	latestData := make([]map[string]any, len(latestItems))
	for i, it := range latestItems {
		latestData[i] = map[string]any{
			"itemid":         it.ItemID,
			"key_":           it.Key,
			"name":           it.Name,
			"lastvalue":      it.LastValue,
			"lastclock":      fmtUnix(it.LastClock),
			"lastclock_unix": it.LastClock,
			"units":          it.Units,
		}
	}

	brokenSection := make([]map[string]any, len(brokenItems))
	for i, it := range brokenItems {
		brokenSection[i] = map[string]any{
			"itemid": it.ItemID,
			"key_":   it.Key,
			"name":   it.Name,
			"error":  it.Error,
		}
	}

	metaSection := map[string]any{
		"host":         hostObj.Host,
		"since":        diagSince,
		"since_unix":   sinceTS,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}

	type section struct {
		name string
		data any
	}
	sections := []section{
		{"host", hostSection},
		{"open_problems", openProblems},
		{"recent_events", recentEvents},
		{"latestdata", latestData},
		{"broken_items", brokenSection},
		{"meta", metaSection},
	}

	enc := json.NewEncoder(os.Stdout)

	if outFmt == output.FormatJSONL {
		for _, s := range sections {
			if err := enc.Encode(map[string]any{"section": s.name, "data": s.data}); err != nil {
				return fmt.Errorf("encoding %s: %w", s.name, err)
			}
		}
		return nil
	}

	snap := map[string]any{}
	for _, s := range sections {
		snap[s.name] = s.data
	}
	enc.SetIndent("", "  ")
	return enc.Encode(snap)
}

// parseWindow parses a look-back window such as 30m, 24h, 7d or 2w (also any
// Go duration like 1h30m).
func parseWindow(s string) (time.Duration, error) {
	if m := relTimeRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return time.Duration(n) * unit, nil
	}
	return time.ParseDuration(s)
}

var diagTriggerEvent bool

var diagnoseTriggerCmd = &cobra.Command{
	Use:   "trigger <trigger-id | event-id>",
	Short: "Explain why a problem is open and whether it can recover",
	Long: `Looks at the trigger behind a problem and reports plain-language reasons: disabled
trigger, host or items, unsupported items, suppression or maintenance, manual close off.
Pass a trigger ID, or an event ID with --event.

Output is always JSON or JSONL (table falls back to JSON).`,
	Args: cobra.ExactArgs(1),
	RunE: runDiagnoseTrigger,
}

func init() {
	diagnoseTriggerCmd.Flags().BoolVar(&diagTriggerEvent, "event", false, "the argument is an event ID; diagnose that event's trigger")
	diagnoseCmd.AddCommand(diagnoseTriggerCmd)
}

func runDiagnoseTrigger(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if outFmt == output.FormatTable {
		outFmt = output.FormatJSON
	}
	triggerID := args[0]
	if diagTriggerEvent {
		st, err := resources.GetEventAckState(c, args[0])
		if err != nil {
			return cliFail(outFmt, err)
		}
		triggerID = st.TriggerID
	}
	d, err := resources.DiagnoseTrigger(c, triggerID)
	if err != nil {
		return cliFail(outFmt, err)
	}
	problems := make([]map[string]any, len(d.OpenProblems))
	for i, p := range d.OpenProblems {
		m := map[string]any{"eventid": p.EventID, "since": fmtUnix(p.Since), "acknowledged": p.Acknowledged, "suppressed": p.Suppressed}
		if p.Suppressed {
			m["suppressed_until"] = suppressedUntilText(p.Until)
			if p.Maintenance != "" {
				m["maintenance"] = p.Maintenance
			}
		}
		problems[i] = m
	}
	items := make([]map[string]any, len(d.Items))
	for i, it := range d.Items {
		items[i] = map[string]any{"itemid": it.ItemID, "key": it.Key, "enabled": it.Enabled, "supported": it.State != "1", "error": it.Error}
	}
	doc := map[string]any{
		"trigger": map[string]any{
			"triggerid": d.TriggerID, "description": d.Description, "severity": severityLabel(d.Severity), "hosts": d.Hosts,
			"enabled": d.Enabled, "state": map[bool]string{true: "problem", false: "ok"}[d.InProblem], "manual_close": d.ManualClose,
		},
		"open_problems": problems,
		"items":         items,
		"reasons":       d.Reasons,
		"recoverable":   d.Recoverable,
		"meta":          map[string]any{"generated_at": time.Now().UTC().Format(time.RFC3339)},
	}
	if outFmt == output.FormatJSONL {
		b, _ := json.Marshal(doc)
		fmt.Println(string(b))
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"data": doc})
}

func suppressedUntilText(until string) string {
	if until == "" || until == "0" {
		return "indefinitely"
	}
	return fmtUnix(until)
}
