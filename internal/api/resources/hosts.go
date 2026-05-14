package resources

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// Host represents a Zabbix host object.
type Host struct {
	HostID      string `json:"hostid"`
	Host        string `json:"host"`        // technical name
	Name        string `json:"name"`        // visible name
	Status      string `json:"status"`      // "0"=monitored, "1"=unmonitored
	Available   string `json:"available"`   // "0"=unknown, "1"=available, "2"=unavailable
	Description string `json:"description"`
}

// HostFilter controls which hosts are returned by GetHosts.
type HostFilter struct {
	GroupIDs  []string
	Status    string // "0"=monitored, "1"=unmonitored, ""=all
	Search    string // substring search on host/name fields
	Limit     int
	SortField string // "host" | "name" | "hostid"
	SortOrder string // "ASC" | "DESC"
}

// GetHosts returns a filtered list of hosts.
func GetHosts(c *api.Client, f HostFilter) ([]Host, error) {
	params := map[string]any{
		"output": []string{"hostid", "host", "name", "status", "available", "description"},
	}
	if len(f.GroupIDs) > 0 {
		params["groupids"] = f.GroupIDs
	}
	if f.Status != "" {
		params["filter"] = map[string]any{"status": []string{f.Status}}
	}
	if f.Search != "" {
		params["search"] = map[string]any{
			"host": f.Search,
			"name": f.Search,
		}
		params["searchByAny"] = true
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}
	sf := f.SortField
	if sf == "" {
		sf = "host"
	}
	params["sortfield"] = sf
	so := f.SortOrder
	if so == "" {
		so = "ASC"
	}
	params["sortorder"] = so

	raw, err := c.Call("host.get", params)
	if err != nil {
		return nil, fmt.Errorf("host.get: %w", err)
	}
	var hosts []Host
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, fmt.Errorf("parsing hosts: %w", err)
	}
	return hosts, nil
}

// GetHost returns a single host by numeric ID, technical name, or visible name.
// Lookup order: numeric ID → technical name (host field) → visible name (name field).
func GetHost(c *api.Client, idOrName string) (*Host, error) {
	outputFields := []string{"hostid", "host", "name", "status", "available", "description"}

	if isNumeric(idOrName) {
		return hostGetOne(c, outputFields, map[string]any{
			"hostids": []string{idOrName},
		}, idOrName)
	}

	// Try technical name.
	h, err := hostGetOne(c, outputFields, map[string]any{
		"filter": map[string]any{"host": []string{idOrName}},
	}, "")
	if err == nil {
		return h, nil
	}
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		return nil, err
	}

	// Fall back to visible name.
	return hostGetOne(c, outputFields, map[string]any{
		"filter": map[string]any{"name": []string{idOrName}},
	}, idOrName)
}

func hostGetOne(c *api.Client, outputFields []string, extraParams map[string]any, query string) (*Host, error) {
	params := map[string]any{"output": outputFields}
	for k, v := range extraParams {
		params[k] = v
	}
	raw, err := c.Call("host.get", params)
	if err != nil {
		return nil, fmt.Errorf("host.get: %w", err)
	}
	var hosts []Host
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, fmt.Errorf("parsing host: %w", err)
	}
	if len(hosts) == 0 {
		if query == "" {
			return nil, &NotFoundError{Resource: "host", Query: "unknown"}
		}
		return nil, &NotFoundError{Resource: "host", Query: query}
	}
	return &hosts[0], nil
}

// ResolveHostID returns the hostid for a given numeric ID or technical name.
func ResolveHostID(c *api.Client, idOrName string) (string, error) {
	if isNumeric(idOrName) {
		return idOrName, nil
	}
	h, err := GetHost(c, idOrName)
	if err != nil {
		return "", err
	}
	return h.HostID, nil
}

// isNumeric returns true if s consists entirely of decimal digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
