package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// TriggerHost is a minimal host reference embedded in trigger results.
type TriggerHost struct {
	HostID string `json:"hostid"`
	Host   string `json:"host"`
}

// Trigger represents a Zabbix trigger.
type Trigger struct {
	TriggerID   string        `json:"triggerid"`
	Description string        `json:"description"`
	Priority    string        `json:"priority"`    // 0–5
	Status      string        `json:"status"`      // "0"=enabled, "1"=disabled
	Value       string        `json:"value"`       // "0"=OK, "1"=PROBLEM
	LastChange  string        `json:"lastchange"`  // Unix timestamp
	Hosts       []TriggerHost `json:"hosts"`
}

// TriggerFilter controls which triggers are returned.
type TriggerFilter struct {
	HostIDs      []string
	MinSeverity  string // "0"–"5", ""=all
	Status       string // "0"=enabled, "1"=disabled, ""=all
	OnlyProblems bool   // if true, only triggers currently in PROBLEM state (value=1)
	Limit        int
	SortField    string // "priority" | "lastchange" | "description"
	SortOrder    string // "ASC" | "DESC"
}

// GetTriggers returns a filtered list of triggers.
func GetTriggers(c *api.Client, f TriggerFilter) ([]Trigger, error) {
	params := map[string]any{
		"output":      []string{"triggerid", "description", "priority", "status", "value", "lastchange"},
		"selectHosts": []string{"hostid", "host"},
		"templated":   false, // exclude template-level triggers; return only host triggers
	}

	// Build filter map (may contain status and/or value).
	filterMap := map[string]any{}
	if f.Status != "" {
		filterMap["status"] = []string{f.Status}
	}
	if f.OnlyProblems {
		filterMap["value"] = []string{"1"}
	}
	if len(filterMap) > 0 {
		params["filter"] = filterMap
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if f.MinSeverity != "" {
		params["min_severity"] = f.MinSeverity
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}
	sf := f.SortField
	if sf == "" {
		sf = "priority"
	}
	so := f.SortOrder
	if so == "" {
		so = "DESC"
	}
	params["sortfield"] = sf
	params["sortorder"] = so

	raw, err := c.Call("trigger.get", params)
	if err != nil {
		return nil, fmt.Errorf("trigger.get: %w", err)
	}
	var triggers []Trigger
	if err := json.Unmarshal(raw, &triggers); err != nil {
		return nil, fmt.Errorf("parsing triggers: %w", err)
	}
	return triggers, nil
}
