package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// Maintenance represents a Zabbix maintenance window.
type Maintenance struct {
	MaintenanceID string `json:"maintenanceid"`
	Name          string `json:"name"`
	ActiveSince   string `json:"active_since"`  // Unix timestamp
	ActiveTill    string `json:"active_till"`   // Unix timestamp
	Description   string `json:"description"`
	MaintenanceType string `json:"maintenance_type"` // "0"=with data collection, "1"=without
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
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}
	raw, err := c.Call("maintenance.get", params)
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
	Name      string
	HostIDs   []string
	GroupIDs  []string
	ActiveSince int64  // Unix timestamp — start
	ActiveTill  int64  // Unix timestamp — end (ActiveSince + duration)
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
		params["hostids"] = p.HostIDs
	}
	if len(p.GroupIDs) > 0 {
		params["groupids"] = p.GroupIDs
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
			"output":          []string{"maintenanceid", "name", "active_since", "active_till", "description", "maintenance_type"},
			"maintenanceids":  []string{idOrName},
		}
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
