package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// Item represents a Zabbix item.
type Item struct {
	ItemID    string `json:"itemid"`
	HostID    string `json:"hostid"`
	Name      string `json:"name"`
	Key       string `json:"key_"`
	Type      string `json:"type"`
	ValueType string `json:"value_type"`
	Status    string `json:"status"`    // "0"=enabled, "1"=disabled
	LastValue string `json:"lastvalue"`
	LastClock string `json:"lastclock"` // Unix timestamp string; "0" = never collected
	Units     string `json:"units"`
	Delay     string `json:"delay"`
}

// ItemFilter controls which items are returned.
type ItemFilter struct {
	HostIDs []string
	KeySearch string // substring search on key_
	Status    string // "0"=enabled, "1"=disabled, ""=all
	Limit     int
}

// GetItems returns items for the given filter.
func GetItems(c *api.Client, f ItemFilter) ([]Item, error) {
	params := map[string]any{
		"output":    []string{"itemid", "hostid", "name", "key_", "type", "value_type", "status", "lastvalue", "lastclock", "units", "delay"},
		"sortfield": "name",
		"sortorder": "ASC",
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if f.KeySearch != "" {
		params["search"] = map[string]any{"key_": f.KeySearch}
	}
	if f.Status != "" {
		params["filter"] = map[string]any{"status": []string{f.Status}}
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("item.get", params)
	if err != nil {
		return nil, fmt.Errorf("item.get: %w", err)
	}
	var items []Item
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing items: %w", err)
	}
	return items, nil
}

// GetLatestData returns items that have been collected at least once, sorted by
// last collection time descending.
func GetLatestData(c *api.Client, f ItemFilter) ([]Item, error) {
	params := map[string]any{
		"output":    []string{"itemid", "hostid", "name", "key_", "lastvalue", "lastclock", "units", "value_type"},
		"sortfield": "name",
		"sortorder": "ASC",
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if f.KeySearch != "" {
		params["search"] = map[string]any{"key_": f.KeySearch}
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("item.get", params)
	if err != nil {
		return nil, fmt.Errorf("item.get (latestdata): %w", err)
	}
	var items []Item
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing items: %w", err)
	}

	// Filter out items with no collected data.
	var out []Item
	for _, it := range items {
		if it.LastClock != "" && it.LastClock != "0" {
			out = append(out, it)
		}
	}
	return out, nil
}
