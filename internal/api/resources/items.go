package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Item represents a Zabbix item.
type Item struct {
	ItemID    string `json:"itemid"`
	HostID    string `json:"hostid"`
	Host      string // enriched from selectHosts; empty if not requested
	Name      string `json:"name"`
	Key       string `json:"key_"`
	Type      string `json:"type"`
	ValueType string `json:"value_type"`
	Status    string `json:"status"` // "0"=enabled, "1"=disabled
	State     string `json:"state"`  // "0"=normal, "1"=not_supported
	Error     string `json:"error"`  // non-empty when state=1
	LastValue string `json:"lastvalue"`
	LastClock string `json:"lastclock"` // Unix timestamp string; "0" = never collected
	Units     string `json:"units"`
	Delay     string `json:"delay"`
}

// itemHostRef is used to unmarshal selectHosts from item.get.
type itemHostRef struct {
	HostID string `json:"hostid"`
	Host   string `json:"host"`
}

// itemWithHosts is the raw shape returned when selectHosts is requested.
type itemWithHosts struct {
	Item
	Hosts []itemHostRef `json:"hosts"`
}

// ItemFilter controls which items are returned.
type ItemFilter struct {
	HostIDs   []string
	GroupIDs  []string // filter by host group
	KeySearch string   // substring search on key_
	Status    string   // "0"=enabled, "1"=disabled, ""=all
	State     string   // "0"=normal, "1"=not_supported, ""=all
	Limit     int
	WithHost  bool // if true, include host name in output via selectHosts
}

// GetItems returns items for the given filter.
func GetItems(c *api.Client, f ItemFilter) ([]Item, error) {
	params := map[string]any{
		"output":    []string{"itemid", "hostid", "name", "key_", "type", "value_type", "status", "state", "error", "lastvalue", "lastclock", "units", "delay"},
		"sortfield": "name",
		"sortorder": "ASC",
	}
	if f.WithHost {
		params["selectHosts"] = []string{"hostid", "host"}
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if len(f.GroupIDs) > 0 {
		params["groupids"] = f.GroupIDs
	}
	if f.KeySearch != "" {
		params["search"] = map[string]any{"key_": f.KeySearch}
	}
	if f.Status != "" {
		params["filter"] = map[string]any{"status": []string{f.Status}}
	}
	if f.State != "" {
		existing, _ := params["filter"].(map[string]any)
		if existing == nil {
			existing = make(map[string]any)
		}
		existing["state"] = []string{f.State}
		params["filter"] = existing
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("item.get", params)
	if err != nil {
		return nil, fmt.Errorf("item.get: %w", err)
	}

	if !f.WithHost {
		var items []Item
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("parsing items: %w", err)
		}
		return items, nil
	}

	var withHosts []itemWithHosts
	if err := json.Unmarshal(raw, &withHosts); err != nil {
		return nil, fmt.Errorf("parsing items: %w", err)
	}
	items := make([]Item, len(withHosts))
	for i, wh := range withHosts {
		items[i] = wh.Item
		if len(wh.Hosts) > 0 {
			items[i].Host = wh.Hosts[0].Host
		}
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
	if f.WithHost {
		params["selectHosts"] = []string{"hostid", "host"}
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if len(f.GroupIDs) > 0 {
		params["groupids"] = f.GroupIDs
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

	var withHosts []itemWithHosts
	if err := json.Unmarshal(raw, &withHosts); err != nil {
		return nil, fmt.Errorf("parsing items: %w", err)
	}

	var out []Item
	for _, wh := range withHosts {
		if wh.Item.LastClock == "" || wh.Item.LastClock == "0" {
			continue
		}
		item := wh.Item
		if len(wh.Hosts) > 0 {
			item.Host = wh.Hosts[0].Host
		}
		out = append(out, item)
	}
	return out, nil
}
