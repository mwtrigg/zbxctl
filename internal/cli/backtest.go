package cli

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/backtest"
	"github.com/mwtrigg/zbxctl/internal/output"
)

var (
	btItem        string
	btHosts       []string
	btGroups      []string
	btWhen        string
	btSince       string
	btUntil       string
	btSource      string
	btRealProblem string
	btRealEvents  []string
	btLead        string
	btSummary     bool
)

var backtestCmd = &cobra.Command{
	Use:   "backtest",
	Short: "Replay an alert condition over history or trends: when would it have fired, and was it right?",
	Long: `Replays a threshold condition over collected data and lists when it would have fired.

Conditions: last>90, avg(5m)>90, min(10m)>=80, max(1h)<5, nodata(10m).
Use --source trends for long windows (hourly min/avg/max, kept for months).

Give --real-problem <text> (a substring of the problem name) or --real-event <id> to compare
the fires with real problems on the same hosts. A fire is a true alert when it starts within
--lead before a real problem or while it runs; every other fire is a false alarm, and real
problems with no fire are missed. --summary prints one row per host and item instead of every fire.`,
	Args: cobra.NoArgs,
	RunE: runBacktest,
}

func init() {
	f := backtestCmd.Flags()
	f.StringVar(&btItem, "item", "", "item key pattern or numeric item ID (required)")
	f.StringArrayVar(&btHosts, "host", nil, "host name or ID (repeatable)")
	f.StringArrayVar(&btGroups, "group", nil, "host group name or ID (repeatable)")
	f.StringVar(&btWhen, "when", "", "condition to replay, e.g. avg(5m)>90 (required)")
	f.StringVar(&btSince, "since", "7d", "start of the window: unix, RFC3339, YYYY-MM-DD or relative like 30d")
	f.StringVar(&btUntil, "until", "", "end of the window (default now)")
	f.StringVar(&btSource, "source", "history", "history | trends")
	f.StringVar(&btRealProblem, "real-problem", "", "compare with real problems whose name contains this text")
	f.StringArrayVar(&btRealEvents, "real-event", nil, "compare with this real problem event ID (repeatable)")
	f.StringVar(&btLead, "lead", "30m", "how long before a real problem a fire still counts as catching it")
	f.BoolVar(&btSummary, "summary", false, "one row per host and item instead of every fire")
	rootCmd.AddCommand(backtestCmd)
}

func runBacktest(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if btItem == "" || btWhen == "" {
		return cliPrintErr(outFmt, "--item and --when are required", output.ExitUsage)
	}
	if btSource != "history" && btSource != "trends" {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --source %q: use history or trends", btSource), output.ExitUsage)
	}
	numeric := isNumericStr(btItem)
	if !numeric && len(btHosts) == 0 && len(btGroups) == 0 {
		return cliPrintErr(outFmt, "a non-numeric --item needs at least one --host or --group", output.ExitUsage)
	}
	cond, err := backtest.Parse(btWhen)
	if err != nil {
		return cliFail(outFmt, &usageError{err.Error()})
	}
	lead, err := parseWindow(btLead)
	if err != nil || lead < 0 {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --lead %q: use a duration like 30m, 1h", btLead), output.ExitUsage)
	}
	since, err := parseTimestamp(btSince)
	if err != nil {
		return cliFail(outFmt, err)
	}
	var until int64
	if btUntil != "" {
		if until, err = parseTimestamp(btUntil); err != nil {
			return cliFail(outFmt, err)
		}
	}
	var hostIDs, groupIDs []string
	for _, h := range btHosts {
		id, err := resources.ResolveHostID(c, h)
		if err != nil {
			return cliFail(outFmt, err)
		}
		hostIDs = append(hostIDs, id)
	}
	for _, g := range btGroups {
		id, err := resources.ResolveGroupID(c, g)
		if err != nil {
			return cliFail(outFmt, err)
		}
		groupIDs = append(groupIDs, id)
	}

	// Collect samples per item.
	type series struct {
		host, hostID, key string
		samples           []backtest.Sample
	}
	byItem := map[string]*series{}
	var order []string
	add := func(item, host, hostID, key string, s backtest.Sample) {
		sr := byItem[item]
		if sr == nil {
			sr = &series{host: host, hostID: hostID, key: key}
			byItem[item] = sr
			order = append(order, item)
		}
		sr.samples = append(sr.samples, s)
	}
	// Fetch from before the window so window functions have context at its start.
	fetchFrom := since - int64(cond.Window.Seconds())
	if btSource == "trends" {
		q := resources.TrendQuery{HostIDs: hostIDs, GroupIDs: groupIDs, KeySearch: btItem, TimeFrom: fetchFrom, TimeTill: until}
		if numeric {
			q.ItemIDs, q.KeySearch = []string{btItem}, ""
		}
		recs, err := resources.GetTrends(c, q)
		if err != nil {
			return cliFail(outFmt, err)
		}
		for _, r := range recs {
			f := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
			add(r.ItemID, r.Host, r.HostID, r.Key, backtest.Sample{T: r.ClockUnix, V: f(r.Avg), Min: f(r.Min), Max: f(r.Max), Num: f(r.Num), Trend: true})
		}
	} else {
		q := resources.HistoryQuery{HostIDs: hostIDs, GroupIDs: groupIDs, KeySearch: btItem, TimeFrom: fetchFrom, TimeTill: until, All: true}
		if numeric {
			q.ItemIDs, q.KeySearch = []string{btItem}, ""
		}
		recs, err := resources.GetHistory(c, q)
		if err != nil {
			return cliFail(outFmt, err)
		}
		for _, r := range recs {
			v, err := strconv.ParseFloat(r.Value, 64)
			if err != nil {
				return cliPrintErr(outFmt, fmt.Sprintf("item %s has non-numeric values; backtest works on numeric items", r.Key), output.ExitUsage)
			}
			add(r.ItemID, r.Host, r.HostID, r.Key, backtest.Sample{T: r.ClockUnix, V: v})
		}
	}
	if len(order) == 0 {
		return cliFail(outFmt, fmt.Errorf("no data for %q in the window; nothing to replay", btItem))
	}

	// Real problems to judge against (optional).
	judge := btRealProblem != "" || len(btRealEvents) > 0
	realByHost := map[string][]backtest.Period{}
	if judge {
		ids := hostIDs
		if len(ids) == 0 {
			seen := map[string]bool{}
			for _, sr := range byItem {
				if !seen[sr.hostID] {
					seen[sr.hostID] = true
					ids = append(ids, sr.hostID)
				}
			}
		}
		spans, err := resources.GetProblemSpans(c, ids, btRealProblem, btRealEvents, since-int64(lead.Seconds()), until)
		if err != nil {
			return cliFail(outFmt, err)
		}
		end := until
		if end == 0 {
			end = time.Now().Unix()
		}
		for _, sp := range spans {
			e := sp.End
			if e == 0 {
				e = end
			}
			realByHost[sp.HostID] = append(realByHost[sp.HostID], backtest.Period{Start: sp.Start, End: e})
		}
	}

	fmtT := func(t int64) string { return fmtUnix(strconv.FormatInt(t, 10)) }
	var rows []map[string]any
	var sums []map[string]any
	sort.Strings(order)
	for _, id := range order {
		sr := byItem[id]
		fires := backtest.Evaluate(sr.samples, cond, since, until)
		var verdicts []backtest.Verdict
		var missed []int
		real := realByHost[sr.hostID]
		if judge {
			verdicts, missed = backtest.Judge(fires, real, lead)
		}
		for i, f := range fires {
			row := map[string]any{
				"host": sr.host, "key_": sr.key, "from": fmtT(f.Start), "to": fmtT(f.End),
				"minutes": float64(f.End-f.Start) / 60, "peak": f.Peak, "still_firing": f.Open,
			}
			if judge {
				v := verdicts[i]
				row["verdict"] = map[bool]string{true: "true alert", false: "false alarm"}[v.True]
				if v.True {
					row["lead_minutes"] = v.LeadMinutes
					row["real_event_start"] = fmtT(real[v.Problem].Start)
				}
			}
			rows = append(rows, row)
		}
		sum := map[string]any{"host": sr.host, "key_": sr.key, "fires": len(fires)}
		if judge {
			tp, fp := 0, 0
			var leads []float64
			seenProblem := map[int]bool{}
			for _, v := range verdicts {
				if v.True {
					tp++
					if !seenProblem[v.Problem] {
						seenProblem[v.Problem] = true
						leads = append(leads, v.LeadMinutes)
					}
				} else {
					fp++
				}
			}
			sum["true_alerts"], sum["false_alarms"], sum["real_problems"], sum["missed"] = tp, fp, len(real), len(missed)
			if len(leads) > 0 {
				sort.Float64s(leads)
				sum["median_lead_minutes"] = leads[len(leads)/2]
			}
		}
		sums = append(sums, sum)
	}
	if btSummary {
		cols := []output.Column{{Key: "host", Header: "HOST"}, {Key: "key_", Header: "KEY"}, {Key: "fires", Header: "FIRES"}}
		if judge {
			cols = append(cols, output.Column{Key: "true_alerts", Header: "TRUE"}, output.Column{Key: "false_alarms", Header: "FALSE"},
				output.Column{Key: "real_problems", Header: "REAL"}, output.Column{Key: "missed", Header: "MISSED"}, output.Column{Key: "median_lead_minutes", Header: "LEAD_MIN"})
		}
		return output.Print(outFmt, cols, sums)
	}
	cols := []output.Column{{Key: "host", Header: "HOST"}, {Key: "key_", Header: "KEY"}, {Key: "from", Header: "FROM"}, {Key: "to", Header: "TO"}, {Key: "minutes", Header: "MINUTES"}, {Key: "peak", Header: "PEAK"}}
	if judge {
		cols = append(cols, output.Column{Key: "verdict", Header: "VERDICT"}, output.Column{Key: "lead_minutes", Header: "LEAD_MIN"})
	}
	return output.Print(outFmt, cols, rows)
}
