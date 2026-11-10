package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Problem represents an active or recently-resolved Zabbix problem.
type Problem struct {
	EventID         string `json:"eventid"`
	ObjectID        string `json:"objectid"` // triggerid
	Name            string `json:"name"`
	Severity        string `json:"severity"`     // 0–5
	Acknowledged    string `json:"acknowledged"` // "0"|"1"
	Clock           string `json:"clock"`        // Unix timestamp — when problem opened
	RecoveryEventID string `json:"r_eventid"`    // "0" = still open; non-zero = resolved
	RecoveryClock   string `json:"r_clock"`      // Unix timestamp — when resolved; "0" if open

	Suppressed      string `json:"suppressed"` // "0"|"1"
	SuppressionData []struct {
		MaintenanceID string `json:"maintenanceid"`
		SuppressUntil string `json:"suppress_until"`
	} `json:"suppression_data"`

	// Filled in by GetProblems from the problem's trigger.
	Host   string   `json:"-"` // first host
	Hosts  []string `json:"-"`
	Orphan string   `json:"-"` // why the problem can never recover; "" = not an orphan
}

// IsSuppressed reports whether the problem is currently suppressed.
func (p Problem) IsSuppressed() bool { return p.Suppressed == "1" }

// MaintenanceSuppressed reports whether a maintenance window suppresses it.
func (p Problem) MaintenanceSuppressed() bool {
	for _, d := range p.SuppressionData {
		if d.MaintenanceID != "" && d.MaintenanceID != "0" {
			return true
		}
	}
	return false
}

// SuppressedUntil returns the latest suppress_until (Unix, as a string); "0" or
// "" means indefinitely or not suppressed.
func (p Problem) SuppressedUntil() string {
	var latest string
	for _, d := range p.SuppressionData {
		if d.SuppressUntil > latest {
			latest = d.SuppressUntil
		}
	}
	return latest
}

// ProblemFilter controls which problems are returned.
type ProblemFilter struct {
	HostIDs      []string
	MinSeverity  int   // 0–5; -1 = no filter
	Since        int64 // Unix timestamp; 0 = no filter
	Acknowledged *bool // nil = all; true = acknowledged only; false = unacknowledged only
	Recent       bool  // if true, pass Zabbix "recent" flag to include recently-resolved problems
	Suppressed   *bool // nil = both; true = only suppressed; false = only unsuppressed
	Maintenance  bool  // only problems suppressed by a maintenance window
	NoOrphans    bool  // drop problems that can never recover
	Limit        int
}

// GetProblems returns a list of problems. By default returns only active (open) problems.
// Set filter.Recent = true to also include recently-resolved problems.
func GetProblems(c *api.Client, f ProblemFilter) ([]Problem, error) {
	params := map[string]any{
		"output":                []string{"eventid", "objectid", "name", "severity", "acknowledged", "clock", "r_eventid", "r_clock", "suppressed"},
		"selectSuppressionData": []string{"maintenanceid", "suppress_until"},
		"sortfield":             "eventid",
		"sortorder":             "DESC",
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
	if f.Suppressed != nil {
		params["suppressed"] = *f.Suppressed
	}
	// Filters applied after enrichment must not be starved by a server-side
	// limit, so with them the limit is applied at the end.
	clientFilter := f.NoOrphans || f.Maintenance
	if f.Limit > 0 && !clientFilter {
		params["limit"] = f.Limit
	}

	raw, err := c.CallList("problem.get", params)
	if err != nil {
		return nil, fmt.Errorf("problem.get: %w", err)
	}
	var problems []Problem
	if err := json.Unmarshal(raw, &problems); err != nil {
		return nil, fmt.Errorf("parsing problems: %w", err)
	}
	if err := attachProblemTriggers(c, problems); err != nil {
		return nil, err
	}
	if !clientFilter {
		return problems, nil
	}
	out := problems[:0:0]
	for _, p := range problems {
		if f.NoOrphans && p.Orphan != "" {
			continue
		}
		if f.Maintenance && !p.MaintenanceSuppressed() {
			continue
		}
		out = append(out, p)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

// attachProblemTriggers fills Host, Hosts and Orphan: problem.get returns only
// the trigger ID, so one trigger.get for the whole list supplies the hosts and
// the state that decides whether a problem can still recover. A problem is an
// orphan when its trigger is disabled, every host is disabled, or every item
// is disabled; those problems stay open forever.
func attachProblemTriggers(c *api.Client, problems []Problem) error {
	if len(problems) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, p := range problems {
		if !seen[p.ObjectID] {
			seen[p.ObjectID] = true
			ids = append(ids, p.ObjectID)
		}
	}
	raw, err := c.Call("trigger.get", map[string]any{
		"triggerids":  ids,
		"output":      []string{"triggerid", "status"},
		"selectHosts": []string{"host", "status"},
		"selectItems": []string{"status"},
	})
	if err != nil {
		return fmt.Errorf("trigger.get (problem triggers): %w", err)
	}
	var trigs []struct {
		TriggerID string `json:"triggerid"`
		Status    string `json:"status"`
		Hosts     []struct {
			Host   string `json:"host"`
			Status string `json:"status"`
		} `json:"hosts"`
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &trigs); err != nil {
		return fmt.Errorf("parsing problem triggers: %w", err)
	}
	type info struct {
		hosts  []string
		orphan string
	}
	by := map[string]info{}
	for _, t := range trigs {
		in := info{}
		allHostsOff, allItemsOff := len(t.Hosts) > 0, len(t.Items) > 0
		for _, h := range t.Hosts {
			in.hosts = append(in.hosts, h.Host)
			if h.Status != "1" {
				allHostsOff = false
			}
		}
		for _, it := range t.Items {
			if it.Status != "1" {
				allItemsOff = false
			}
		}
		switch {
		case t.Status == "1":
			in.orphan = "trigger disabled"
		case allHostsOff:
			in.orphan = "host disabled"
		case allItemsOff:
			in.orphan = "items disabled"
		}
		by[t.TriggerID] = in
	}
	for i := range problems {
		in := by[problems[i].ObjectID]
		problems[i].Hosts = in.hosts
		if len(in.hosts) > 0 {
			problems[i].Host = in.hosts[0]
		}
		problems[i].Orphan = in.orphan
	}
	return nil
}
