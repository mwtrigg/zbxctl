package resources

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// HistoryRecord is a single history data point enriched with host and item metadata.
type HistoryRecord struct {
	ItemID    string
	HostID    string
	Host      string
	Key       string
	Clock     string // Unix timestamp string from Zabbix
	ClockUnix int64
	Value     string // all Zabbix history values are strings
}

// HistoryQuery describes a history fetch request.
// Set ItemIDs for direct lookup. Set HostIDs/GroupIDs + KeySearch for resolution.
// If neither is set and ItemIDs is empty, returns an error.
type HistoryQuery struct {
	ItemIDs   []string
	HostIDs   []string
	GroupIDs  []string
	KeySearch string
	TimeFrom  int64
	TimeTill  int64
	Limit     int
}

// itemRef is a lightweight struct used during item resolution.
type itemRef struct {
	ItemID    string
	HostID    string
	Host      string
	Key       string
	ValueType string // "0"=float, "1"=string, "2"=log, "3"=uint, "4"=text
}

// rawHistoryRecord matches the Zabbix history.get response shape.
type rawHistoryRecord struct {
	ItemID string `json:"itemid"`
	Clock  string `json:"clock"`
	NS     string `json:"ns"`
	Value  string `json:"value"`
}

// GetHistory resolves items and fetches history, returning enriched records
// sorted by clock ascending.
func GetHistory(c *api.Client, q HistoryQuery) ([]HistoryRecord, error) {
	var refs []itemRef

	if len(q.ItemIDs) > 0 {
		// Direct item ID path — skip resolution, no host/key enrichment.
		resolved, err := resolveItemRefs(c, q.ItemIDs, nil, nil, "")
		if err != nil {
			return nil, err
		}
		refs = resolved
	} else {
		var err error
		refs, err = resolveItemRefs(c, nil, q.HostIDs, q.GroupIDs, q.KeySearch)
		if err != nil {
			return nil, err
		}
	}

	if len(refs) == 0 {
		return nil, &NotFoundError{Resource: "item", Query: q.KeySearch}
	}

	groups := groupByValueType(refs)
	var all []HistoryRecord

	for vt, group := range groups {
		ids := make([]string, len(group))
		for i, r := range group {
			ids[i] = r.ItemID
		}
		raw, err := fetchHistoryRaw(c, ids, vt, q.TimeFrom, q.TimeTill, q.Limit)
		if err != nil {
			return nil, err
		}
		// Build lookup map for enrichment.
		refMap := make(map[string]itemRef, len(group))
		for _, r := range group {
			refMap[r.ItemID] = r
		}
		for _, r := range raw {
			ref := refMap[r.ItemID]
			ts, _ := strconv.ParseInt(r.Clock, 10, 64)
			all = append(all, HistoryRecord{
				ItemID:    r.ItemID,
				HostID:    ref.HostID,
				Host:      ref.Host,
				Key:       ref.Key,
				Clock:     r.Clock,
				ClockUnix: ts,
				Value:     r.Value,
			})
		}
	}

	return mergeAndSort(all), nil
}

// resolveItemRefs calls item.get to get itemids, hostids, host names, keys, and value_types.
func resolveItemRefs(c *api.Client, itemIDs, hostIDs, groupIDs []string, keySearch string) ([]itemRef, error) {
	params := map[string]any{
		"output":      []string{"itemid", "hostid", "key_", "value_type"},
		"selectHosts": []string{"hostid", "host"},
	}
	if len(itemIDs) > 0 {
		params["itemids"] = itemIDs
	}
	if len(hostIDs) > 0 {
		params["hostids"] = hostIDs
	}
	if len(groupIDs) > 0 {
		params["groupids"] = groupIDs
	}
	if keySearch != "" {
		params["search"] = map[string]any{"key_": keySearch}
	}

	raw, err := c.Call("item.get", params)
	if err != nil {
		return nil, fmt.Errorf("item.get: %w", err)
	}

	var items []struct {
		ItemID    string `json:"itemid"`
		HostID    string `json:"hostid"`
		Key       string `json:"key_"`
		ValueType string `json:"value_type"`
		Hosts     []struct {
			HostID string `json:"hostid"`
			Host   string `json:"host"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing items: %w", err)
	}

	refs := make([]itemRef, len(items))
	for i, it := range items {
		hostname := ""
		if len(it.Hosts) > 0 {
			hostname = it.Hosts[0].Host
		}
		refs[i] = itemRef{
			ItemID:    it.ItemID,
			HostID:    it.HostID,
			Host:      hostname,
			Key:       it.Key,
			ValueType: it.ValueType,
		}
	}
	return refs, nil
}

// fetchHistoryRaw calls history.get for a single value_type.
func fetchHistoryRaw(c *api.Client, itemIDs []string, valueType int, from, till int64, limit int) ([]rawHistoryRecord, error) {
	params := map[string]any{
		"output":    "extend",
		"itemids":   itemIDs,
		"history":   valueType,
		"sortfield": "clock",
		"sortorder": "ASC",
	}
	if from > 0 {
		params["time_from"] = from
	}
	if till > 0 {
		params["time_till"] = till
	}
	if limit > 0 {
		params["limit"] = limit
	}

	raw, err := c.Call("history.get", params)
	if err != nil {
		return nil, fmt.Errorf("history.get (type %d): %w", valueType, err)
	}
	var records []rawHistoryRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("parsing history: %w", err)
	}
	return records, nil
}

// groupByValueType groups itemRefs by their value_type integer.
func groupByValueType(refs []itemRef) map[int][]itemRef {
	groups := make(map[int][]itemRef)
	for _, r := range refs {
		vt, _ := strconv.Atoi(r.ValueType)
		groups[vt] = append(groups[vt], r)
	}
	return groups
}

// mergeAndSort merges multiple slices of HistoryRecord and sorts by ClockUnix ascending.
func mergeAndSort(slices ...[]HistoryRecord) []HistoryRecord {
	var total int
	for _, s := range slices {
		total += len(s)
	}
	out := make([]HistoryRecord, 0, total)
	for _, s := range slices {
		out = append(out, s...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ClockUnix < out[j].ClockUnix
	})
	return out
}
