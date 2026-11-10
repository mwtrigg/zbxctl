package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Problem represents an active or recently-resolved Zabbix problem.
type Problem struct {
	EventID         string `json:"eventid"`
	ObjectID        string `json:"objectid"`     // triggerid
	Name            string `json:"name"`
	Severity        string `json:"severity"`     // 0–5
	Acknowledged    string `json:"acknowledged"` // "0"|"1"
	Clock           string `json:"clock"`        // Unix timestamp — when problem opened
	RecoveryEventID string `json:"r_eventid"`    // "0" = still open; non-zero = resolved
	RecoveryClock   string `json:"r_clock"`      // Unix timestamp — when resolved; "0" if open
}

// ProblemFilter controls which problems are returned.
type ProblemFilter struct {
	HostIDs      []string
	MinSeverity  int   // 0–5; -1 = no filter
	Since        int64 // Unix timestamp; 0 = no filter
	Acknowledged *bool // nil = all; true = acknowledged only; false = unacknowledged only
	Recent       bool  // if true, pass Zabbix "recent" flag to include recently-resolved problems
	Limit        int
}

// GetProblems returns a list of problems. By default returns only active (open) problems.
// Set filter.Recent = true to also include recently-resolved problems.
func GetProblems(c *api.Client, f ProblemFilter) ([]Problem, error) {
	params := map[string]any{
		"output":    []string{"eventid", "objectid", "name", "severity", "acknowledged", "clock", "r_eventid", "r_clock"},
		"sortfield": "eventid",
		"sortorder": "DESC",
	}
	if f.Recent {
		params["recent"] = true
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if f.MinSeverity >= 0 {
		sev := make([]int, 0, 6-f.MinSeverity)
		for i := f.MinSeverity; i <= 5; i++ {
			sev = append(sev, i)
		}
		params["severities"] = sev
	}
	if f.Since > 0 {
		params["time_from"] = f.Since
	}
	if f.Acknowledged != nil {
		params["acknowledged"] = *f.Acknowledged
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("problem.get", params)
	if err != nil {
		return nil, fmt.Errorf("problem.get: %w", err)
	}
	var problems []Problem
	if err := json.Unmarshal(raw, &problems); err != nil {
		return nil, fmt.Errorf("parsing problems: %w", err)
	}
	return problems, nil
}
