package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// ProblemHost is a minimal host reference embedded in problem results.
type ProblemHost struct {
	HostID string `json:"hostid"`
	Host   string `json:"host"`
}

// Problem represents an active Zabbix problem.
type Problem struct {
	EventID      string `json:"eventid"`
	ObjectID     string `json:"objectid"` // triggerid
	Name         string `json:"name"`
	Severity     string `json:"severity"`     // 0–5
	Acknowledged string `json:"acknowledged"` // "0"|"1"
	Clock        string `json:"clock"`        // Unix timestamp
}

// ProblemFilter controls which problems are returned.
type ProblemFilter struct {
	HostIDs      []string
	MinSeverity  int    // 0–5; -1 = no filter
	Since        int64  // Unix timestamp; 0 = no filter
	Acknowledged *bool  // nil = all; true = acknowledged only; false = unacknowledged only
	Limit        int
}

// GetProblems returns a list of active problems.
func GetProblems(c *api.Client, f ProblemFilter) ([]Problem, error) {
	params := map[string]any{
		"output":    []string{"eventid", "objectid", "name", "severity", "acknowledged", "clock"},
		"sortfield": "eventid",
		"sortorder": "DESC",
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if f.MinSeverity >= 0 {
		// Build severities array: all values from min to 5.
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
		if *f.Acknowledged {
			params["acknowledged"] = true
		} else {
			params["acknowledged"] = false
		}
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
