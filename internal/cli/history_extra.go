package cli

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/api/resources"
	"github.com/mwtrigg/zbxctl/internal/output"
)

// downsampleHistory keeps the last sample of each step-sized bucket per item.
func downsampleHistory(recs []resources.HistoryRecord, step time.Duration) []resources.HistoryRecord {
	secs := int64(step.Seconds())
	type key struct {
		item   string
		bucket int64
	}
	last := map[key]int{}
	for i, r := range recs { // recs are sorted by clock ascending
		last[key{r.ItemID, r.ClockUnix / secs}] = i
	}
	keep := make([]int, 0, len(last))
	for _, i := range last {
		keep = append(keep, i)
	}
	sort.Ints(keep)
	out := make([]resources.HistoryRecord, len(keep))
	for i, idx := range keep {
		out[i] = recs[idx]
	}
	return out
}

type historyGap struct {
	itemID, host, key string
	from, to          int64
	edge              string // "", "start" or "end" when the gap touches the window edge
}

// findHistoryGaps returns, per item, the spans between consecutive samples that
// exceed over. With a known window, silence at its start or end counts too.
func findHistoryGaps(recs []resources.HistoryRecord, over time.Duration, since, until int64) []historyGap {
	limit := int64(over.Seconds())
	byItem := map[string][]resources.HistoryRecord{}
	var order []string
	for _, r := range recs {
		if _, ok := byItem[r.ItemID]; !ok {
			order = append(order, r.ItemID)
		}
		byItem[r.ItemID] = append(byItem[r.ItemID], r)
	}
	var gaps []historyGap
	for _, id := range order {
		rs := byItem[id]
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].ClockUnix < rs[j].ClockUnix })
		first, last := rs[0], rs[len(rs)-1]
		if since > 0 && first.ClockUnix-since > limit {
			gaps = append(gaps, historyGap{id, first.Host, first.Key, since, first.ClockUnix, "start"})
		}
		for i := 1; i < len(rs); i++ {
			if rs[i].ClockUnix-rs[i-1].ClockUnix > limit {
				gaps = append(gaps, historyGap{id, rs[i].Host, rs[i].Key, rs[i-1].ClockUnix, rs[i].ClockUnix, ""})
			}
		}
		if until > 0 && until-last.ClockUnix > limit {
			gaps = append(gaps, historyGap{id, last.Host, last.Key, last.ClockUnix, until, "end"})
		}
	}
	return gaps
}

func printHistoryGaps(outFmt output.Format, recs []resources.HistoryRecord, over time.Duration, since, until int64) error {
	gaps := findHistoryGaps(recs, over, since, until)
	cols := []output.Column{{Key: "host", Header: "HOST"}, {Key: "key_", Header: "KEY"}, {Key: "from", Header: "FROM"}, {Key: "to", Header: "TO"}, {Key: "seconds", Header: "SECONDS"}}
	rows := make([]map[string]any, len(gaps))
	for i, g := range gaps {
		rows[i] = map[string]any{
			"host": g.host, "itemid": g.itemID, "key_": g.key,
			"from": fmtUnix(strconv.FormatInt(g.from, 10)), "to": fmtUnix(strconv.FormatInt(g.to, 10)),
			"seconds": g.to - g.from, "edge": g.edge,
		}
	}
	return output.Print(outFmt, cols, rows)
}

// ── get trends ────────────────────────────────────────────────────────────────

var (
	trendItem   string
	trendHosts  []string
	trendGroups []string
	trendSince  string
	trendUntil  string
	trendLimit  int
	trendRollup string
)

var getTrendsCmd = &cobra.Command{
	Use:   "trends",
	Short: "Hourly min/avg/max of numeric items (kept far longer than history)",
	Args:  cobra.NoArgs,
	RunE:  runGetTrends,
}

func init() {
	f := getTrendsCmd.Flags()
	f.StringVar(&trendItem, "item", "", "item key pattern or numeric item ID (required)")
	f.StringArrayVar(&trendHosts, "host", nil, "host name or ID (repeatable)")
	f.StringArrayVar(&trendGroups, "group", nil, "host group name or ID (repeatable)")
	f.StringVar(&trendSince, "since", "", "start: unix, RFC3339, YYYY-MM-DD, or 30d")
	f.StringVar(&trendUntil, "until", "", "end: unix, RFC3339, YYYY-MM-DD, or 1d")
	f.IntVar(&trendLimit, "limit", 0, "max rows (default: all in the window)")
	f.StringVar(&trendRollup, "rollup", "", "fold the hourly buckets into day, week (Monday start) or month, in the --tz zone")
	getCmd.AddCommand(getTrendsCmd)
}

func runGetTrends(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliFail(outFmt, err)
	}
	if trendItem == "" {
		return cliPrintErr(outFmt, "--item is required", output.ExitUsage)
	}
	numeric := isNumericStr(trendItem)
	if !numeric && len(trendHosts) == 0 && len(trendGroups) == 0 {
		return cliPrintErr(outFmt, "a non-numeric --item needs at least one --host or --group", output.ExitUsage)
	}
	q := resources.TrendQuery{KeySearch: trendItem, Limit: trendLimit}
	if numeric {
		q.ItemIDs, q.KeySearch = []string{trendItem}, ""
	}
	for _, h := range trendHosts {
		id, err := resources.ResolveHostID(c, h)
		if err != nil {
			return cliFail(outFmt, err)
		}
		q.HostIDs = append(q.HostIDs, id)
	}
	for _, g := range trendGroups {
		id, err := resources.ResolveGroupID(c, g)
		if err != nil {
			return cliFail(outFmt, err)
		}
		q.GroupIDs = append(q.GroupIDs, id)
	}
	if trendSince != "" {
		if q.TimeFrom, err = parseTimestamp(trendSince); err != nil {
			return cliFail(outFmt, err)
		}
	}
	if trendUntil != "" {
		if q.TimeTill, err = parseTimestamp(trendUntil); err != nil {
			return cliFail(outFmt, err)
		}
	}
	recs, err := resources.GetTrends(c, q)
	if err != nil {
		return cliFail(outFmt, err)
	}
	if trendRollup != "" {
		if recs, err = resources.RollupTrends(recs, trendRollup, displayLoc); err != nil {
			return cliFail(outFmt, &usageError{err.Error()})
		}
	}
	periodHeader := "HOUR"
	if trendRollup != "" {
		periodHeader = strings.ToUpper(trendRollup)
	}
	cols := []output.Column{
		{Key: "host", Header: "HOST"}, {Key: "key_", Header: "KEY"}, {Key: "clock", Header: periodHeader},
		{Key: "num", Header: "SAMPLES"}, {Key: "min", Header: "MIN"}, {Key: "avg", Header: "AVG"}, {Key: "max", Header: "MAX"},
	}
	rows := make([]map[string]any, len(recs))
	for i, r := range recs {
		rows[i] = map[string]any{"host": r.Host, "hostid": r.HostID, "itemid": r.ItemID, "key_": r.Key,
			"clock": fmtUnix(r.Clock), "clock_unix": r.ClockUnix, "num": r.Num, "min": r.Min, "avg": r.Avg, "max": r.Max}
	}
	return output.Print(outFmt, cols, rows)
}
