package resources

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Maintenance represents a Zabbix maintenance window.
type Maintenance struct {
	MaintenanceID   string `json:"maintenanceid"`
	Name            string `json:"name"`
	ActiveSince     string `json:"active_since"` // Unix timestamp
	ActiveTill      string `json:"active_till"`  // Unix timestamp
	Description     string `json:"description"`
	MaintenanceType string `json:"maintenance_type"` // "0"=with data collection, "1"=without

	Hosts []struct {
		Host string `json:"host"`
	} `json:"hosts"`
	HostGroups  []NamedRef   `json:"hostgroups"`
	TimePeriods []TimePeriod `json:"timeperiods"`
}

// TimePeriod is one period of a maintenance window.
type TimePeriod struct {
	Type      string `json:"timeperiod_type"` // "0" one-time
	StartDate string `json:"start_date"`      // Unix, one-time periods
	Period    string `json:"period"`          // seconds
}

// HostNames returns the names of the hosts in the window.
func (m Maintenance) HostNames() []string {
	out := make([]string, len(m.Hosts))
	for i, h := range m.Hosts {
		out[i] = h.Host
	}
	return out
}

// GroupNames returns the names of the host groups in the window.
func (m Maintenance) GroupNames() []string { return names(m.HostGroups) }

// maintSelects adds the related objects every maintenance read needs.
func maintSelects(params map[string]any) {
	params["selectHosts"] = []string{"host"}
	params["selectHostGroups"] = []string{"name"}
	params["selectTimeperiods"] = []string{"timeperiod_type", "start_date", "period"}
}

// MaintenanceFilter controls which maintenances are returned.
type MaintenanceFilter struct {
	Limit int
}

// GetMaintenances returns a list of maintenance windows.
func GetMaintenances(c *api.Client, f MaintenanceFilter) ([]Maintenance, error) {
	params := map[string]any{
		"output":    []string{"maintenanceid", "name", "active_since", "active_till", "description", "maintenance_type"},
		"sortfield": "name",
		"sortorder": "ASC",
	}
	maintSelects(params)
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}
	raw, err := c.CallList("maintenance.get", params)
	if err != nil {
		return nil, fmt.Errorf("maintenance.get: %w", err)
	}
	var result []Maintenance
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing maintenances: %w", err)
	}
	return result, nil
}

// GetMaintenanceByName returns a single maintenance by exact name.
func GetMaintenanceByName(c *api.Client, name string) (*Maintenance, error) {
	params := map[string]any{
		"output": []string{"maintenanceid", "name", "active_since", "active_till", "description", "maintenance_type"},
		"filter": map[string]any{"name": []string{name}},
	}
	maintSelects(params)
	raw, err := c.Call("maintenance.get", params)
	if err != nil {
		return nil, fmt.Errorf("maintenance.get: %w", err)
	}
	var result []Maintenance
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing maintenance: %w", err)
	}
	if len(result) == 0 {
		return nil, &NotFoundError{Resource: "maintenance", Query: name}
	}
	return &result[0], nil
}

// CreateMaintenanceParams holds parameters for creating a maintenance window.
type CreateMaintenanceParams struct {
	Name        string
	HostIDs     []string
	GroupIDs    []string
	ActiveSince int64 // Unix timestamp — start
	ActiveTill  int64 // Unix timestamp — end (ActiveSince + duration)
	Description string
}

// CreateMaintenance creates a new maintenance window and returns the new ID.
func CreateMaintenance(c *api.Client, p CreateMaintenanceParams) (string, error) {
	// Build a single "one-time" time period covering the entire window.
	timePeriod := map[string]any{
		"timeperiod_type": 0, // one-time
		"start_date":      p.ActiveSince,
		"period":          p.ActiveTill - p.ActiveSince,
	}

	params := map[string]any{
		"name":             p.Name,
		"active_since":     p.ActiveSince,
		"active_till":      p.ActiveTill,
		"timeperiods":      []any{timePeriod},
		"maintenance_type": 0, // with data collection
	}
	if p.Description != "" {
		params["description"] = p.Description
	}
	if len(p.HostIDs) > 0 {
		// maintenance.create takes host objects; the old "hostids" parameter
		// was removed from the API.
		hosts := make([]map[string]string, len(p.HostIDs))
		for i, id := range p.HostIDs {
			hosts[i] = map[string]string{"hostid": id}
		}
		params["hosts"] = hosts
	}
	if len(p.GroupIDs) > 0 {
		groups := make([]map[string]string, len(p.GroupIDs))
		for i, id := range p.GroupIDs {
			groups[i] = map[string]string{"groupid": id}
		}
		params["groups"] = groups
	}

	raw, err := c.Call("maintenance.create", params)
	if err != nil {
		return "", fmt.Errorf("maintenance.create: %w", err)
	}
	var result struct {
		MaintenanceIDs []string `json:"maintenanceids"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parsing maintenance.create response: %w", err)
	}
	if len(result.MaintenanceIDs) == 0 {
		return "", fmt.Errorf("maintenance.create returned empty maintenanceids")
	}
	return result.MaintenanceIDs[0], nil
}

// DeleteMaintenance deletes a maintenance window by ID.
func DeleteMaintenance(c *api.Client, maintenanceID string) error {
	raw, err := c.Call("maintenance.delete", []string{maintenanceID})
	if err != nil {
		return fmt.Errorf("maintenance.delete: %w", err)
	}
	var result struct {
		MaintenanceIDs []string `json:"maintenanceids"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("parsing maintenance.delete response: %w", err)
	}
	return nil
}

// ResolveMaintenance returns the maintenanceid for a given numeric ID or name.
func ResolveMaintenance(c *api.Client, idOrName string) (*Maintenance, error) {
	if isNumeric(idOrName) {
		params := map[string]any{
			"output":         []string{"maintenanceid", "name", "active_since", "active_till", "description", "maintenance_type"},
			"maintenanceids": []string{idOrName},
		}
		maintSelects(params)
		raw, err := c.Call("maintenance.get", params)
		if err != nil {
			return nil, fmt.Errorf("maintenance.get: %w", err)
		}
		var result []Maintenance
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("parsing maintenance: %w", err)
		}
		if len(result) == 0 {
			return nil, &NotFoundError{Resource: "maintenance", Query: idOrName}
		}
		return &result[0], nil
	}
	return GetMaintenanceByName(c, idOrName)
}

// SetMaintenanceEnd moves the end of a one-time maintenance window to newTill
// (Unix), changing active_till and the period length together so the window
// stays consistent. Windows with recurring periods are refused.
func SetMaintenanceEnd(c *api.Client, m *Maintenance, newTill int64) error {
	if err := CheckMaintenanceEnd(m, newTill); err != nil {
		return err
	}
	start, _ := strconv.ParseInt(m.TimePeriods[0].StartDate, 10, 64)
	_, err := c.Call("maintenance.update", map[string]any{
		"maintenanceid": m.MaintenanceID,
		"active_till":   newTill,
		"timeperiods":   []map[string]any{{"timeperiod_type": 0, "start_date": start, "period": newTill - start}},
	})
	if err != nil {
		return fmt.Errorf("maintenance.update: %w", err)
	}
	return nil
}

// CheckMaintenanceEnd says whether the end of m can move to newTill: the window
// must be a single one-time period and the new end must come after its start.
func CheckMaintenanceEnd(m *Maintenance, newTill int64) error {
	if len(m.TimePeriods) != 1 || m.TimePeriods[0].Type != "0" {
		return fmt.Errorf("maintenance %q is not a single one-time window; change it in Zabbix", m.Name)
	}
	start, err := strconv.ParseInt(m.TimePeriods[0].StartDate, 10, 64)
	if err != nil {
		return fmt.Errorf("parsing start_date %q: %w", m.TimePeriods[0].StartDate, err)
	}
	if newTill <= start {
		return fmt.Errorf("the new end %d is not after the window start: maintenance %q starts at %d", newTill, m.Name, start)
	}
	return nil
}
