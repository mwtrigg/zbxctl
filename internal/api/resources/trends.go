package resources

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

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
