package resources

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// TrendRecord is one hourly trend bucket of a numeric item.
type TrendRecord struct {
	ItemID    string
	HostID    string
	Host      string
	Key       string
	Clock     string // Unix, start of the hour
	ClockUnix int64
	Num       string // samples in the hour
	Min       string
	Avg       string
	Max       string
}

// TrendQuery selects items like HistoryQuery does.
type TrendQuery struct {
	ItemIDs   []string
	HostIDs   []string
	GroupIDs  []string
	KeySearch string
	TimeFrom  int64
	TimeTill  int64
	Limit     int
}

// GetTrends returns hourly min/avg/max per item, oldest first. Trends exist
// only for numeric items (float and unsigned).
func GetTrends(c *api.Client, q TrendQuery) ([]TrendRecord, error) {
	refs, err := resolveItemRefs(c, q.ItemIDs, q.HostIDs, q.GroupIDs, q.KeySearch)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		query := q.KeySearch
		if query == "" && len(q.ItemIDs) > 0 {
			query = q.ItemIDs[0]
		}
		return nil, &NotFoundError{Resource: "item", Query: query}
	}
	byID := map[string]itemRef{}
	var ids []string
	for _, r := range refs {
		if r.ValueType == "0" || r.ValueType == "3" {
			byID[r.ItemID] = r
			ids = append(ids, r.ItemID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("trends exist only for numeric items; the matching items are text, log or character items")
	}
	params := map[string]any{"itemids": ids, "output": []string{"itemid", "clock", "num", "value_min", "value_avg", "value_max"}}
	if q.TimeFrom > 0 {
		params["time_from"] = q.TimeFrom
	}
	if q.TimeTill > 0 {
		params["time_till"] = q.TimeTill
	}
	if q.Limit > 0 {
		params["limit"] = q.Limit
	}
	raw, err := c.Call("trend.get", params)
	if err != nil {
		return nil, fmt.Errorf("trend.get: %w", err)
	}
	var rs []struct {
		ItemID string `json:"itemid"`
		Clock  string `json:"clock"`
		Num    string `json:"num"`
		Min    string `json:"value_min"`
		Avg    string `json:"value_avg"`
		Max    string `json:"value_max"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing trends: %w", err)
	}
	out := make([]TrendRecord, 0, len(rs))
	for _, r := range rs {
		ref := byID[r.ItemID]
		ts, _ := strconv.ParseInt(r.Clock, 10, 64)
		out = append(out, TrendRecord{ItemID: r.ItemID, HostID: ref.HostID, Host: ref.Host, Key: ref.Key,
			Clock: r.Clock, ClockUnix: ts, Num: r.Num, Min: r.Min, Avg: r.Avg, Max: r.Max})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ClockUnix < out[j].ClockUnix })
	return out, nil
}

// RollupTrends folds hourly trend buckets into days, weeks or months. A bucket
// starts at midnight in loc (weeks on Monday), so a day that has 23 or 25 hours
// because of daylight saving still rolls up as one day. Min is the lowest
// hourly minimum, Max the highest hourly maximum, Avg is weighted by how many
// samples each hour held, and Num is the total sample count.
func RollupTrends(recs []TrendRecord, period string, loc *time.Location) ([]TrendRecord, error) {
	start := func(t time.Time) time.Time {
		y, m, d := t.In(loc).Date()
		switch period {
		case "day":
			return time.Date(y, m, d, 0, 0, 0, 0, loc)
		case "week":
			day := time.Date(y, m, d, 0, 0, 0, 0, loc)
			back := (int(day.Weekday()) + 6) % 7 // Monday = 0
			return time.Date(y, m, d-back, 0, 0, 0, 0, loc)
		case "month":
			return time.Date(y, m, 1, 0, 0, 0, 0, loc)
		}
		return time.Time{}
	}
	if period != "day" && period != "week" && period != "month" {
		return nil, fmt.Errorf("invalid rollup %q: use day, week or month", period)
	}
	type acc struct {
		rec          TrendRecord
		min, max     float64
		sum, samples float64
		started      bool
	}
	byKey := map[string]*acc{}
	var order []string
	for _, r := range recs {
		b := start(time.Unix(r.ClockUnix, 0))
		key := r.ItemID + "/" + strconv.FormatInt(b.Unix(), 10)
		a := byKey[key]
		min, _ := strconv.ParseFloat(r.Min, 64)
		max, _ := strconv.ParseFloat(r.Max, 64)
		avg, _ := strconv.ParseFloat(r.Avg, 64)
		num, _ := strconv.ParseFloat(r.Num, 64)
		if a == nil {
			a = &acc{rec: TrendRecord{ItemID: r.ItemID, HostID: r.HostID, Host: r.Host, Key: r.Key, ClockUnix: b.Unix(), Clock: strconv.FormatInt(b.Unix(), 10)}}
			byKey[key] = a
			order = append(order, key)
		}
		if !a.started || min < a.min {
			a.min = min
		}
		if !a.started || max > a.max {
			a.max = max
		}
		a.started = true
		a.sum += avg * num
		a.samples += num
	}
	out := make([]TrendRecord, 0, len(order))
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	for _, k := range order {
		a := byKey[k]
		a.rec.Min, a.rec.Max, a.rec.Num = f(a.min), f(a.max), f(a.samples)
		if a.samples > 0 {
			a.rec.Avg = strconv.FormatFloat(a.sum/a.samples, 'f', 4, 64)
			a.rec.Avg = strings.TrimRight(strings.TrimRight(a.rec.Avg, "0"), ".")
		} else {
			a.rec.Avg = "0"
		}
		out = append(out, a.rec)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ClockUnix != out[j].ClockUnix {
			return out[i].ClockUnix < out[j].ClockUnix
		}
		return out[i].ItemID < out[j].ItemID
	})
	return out, nil
}
