package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Alert represents a fired notification (alert) in Zabbix.
type Alert struct {
	AlertID     string `json:"alertid"`
	EventID     string `json:"eventid"`
	HostID      string // resolved via event enrichment
	Host        string // resolved via event enrichment
	Clock       string `json:"clock"`
	SendTo      string `json:"sendto"`
	Subject     string `json:"subject"`
	Status      string `json:"status"` // "0"=not_sent, "1"=sent, "2"=failed, "3"=new
	Error       string `json:"error"`
	UserID      string `json:"userid"`
	MediaTypeID string `json:"mediatypeid"`
}

// AlertFilter controls which alerts are returned.
type AlertFilter struct {
	HostIDs  []string
	EventIDs []string
	Since    int64  // Unix timestamp; 0 = no filter
	Status   string // "sent"|"failed"|"" (all)
	Limit    int
}

// GetAlerts returns notifications. Host and HostID fields are populated by
// resolving each alert's event to find the associated host.
func GetAlerts(c *api.Client, f AlertFilter) ([]Alert, error) {
	params := map[string]any{
		"output":    []string{"alertid", "eventid", "clock", "sendto", "subject", "status", "error", "userid", "mediatypeid"},
		"sortfield": "alertid",
		"sortorder": "DESC",
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if len(f.EventIDs) > 0 {
		params["eventids"] = f.EventIDs
	}
	if f.Since > 0 {
		params["time_from"] = f.Since
	}
	switch f.Status {
	case "sent":
		params["filter"] = map[string]any{"status": []string{"1"}}
	case "failed":
		params["filter"] = map[string]any{"status": []string{"2"}}
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.CallList("alert.get", params)
	if err != nil {
		return nil, fmt.Errorf("alert.get: %w", err)
	}
	var alerts []Alert
	if err := json.Unmarshal(raw, &alerts); err != nil {
		return nil, fmt.Errorf("parsing alerts: %w", err)
	}

	if len(alerts) == 0 {
		return alerts, nil
	}

	// Enrich alerts with host info via event lookup.
	eventIDs := make([]string, 0, len(alerts))
	seen := make(map[string]bool)
	for _, a := range alerts {
		if a.EventID != "" && !seen[a.EventID] {
			eventIDs = append(eventIDs, a.EventID)
			seen[a.EventID] = true
		}
	}

	hostByEvent, err := resolveEventHosts(c, eventIDs)
	if err != nil {
		// Non-fatal: return alerts without host enrichment rather than failing.
		return alerts, nil
	}

	for i := range alerts {
		if h, ok := hostByEvent[alerts[i].EventID]; ok {
			alerts[i].HostID = h.HostID
			alerts[i].Host = h.Host
		}
	}
	return alerts, nil
}

// resolveEventHosts returns a map of eventid → EventHost by calling event.get.
func resolveEventHosts(c *api.Client, eventIDs []string) (map[string]EventHost, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}
	params := map[string]any{
		"eventids":    eventIDs,
		"output":      []string{"eventid"},
		"selectHosts": []string{"hostid", "host"},
	}
	raw, err := c.Call("event.get", params)
	if err != nil {
		return nil, fmt.Errorf("event.get (host enrichment): %w", err)
	}
	var events []struct {
		EventID string      `json:"eventid"`
		Hosts   []EventHost `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, fmt.Errorf("parsing events (host enrichment): %w", err)
	}
	m := make(map[string]EventHost, len(events))
	for _, ev := range events {
		if len(ev.Hosts) > 0 {
			m[ev.EventID] = ev.Hosts[0]
		}
	}
	return m, nil
}

func AlertStatusLabel(s string) string {
	switch s {
	case "0":
		return "not_sent"
	case "1":
		return "sent"
	case "2":
		return "failed"
	case "3":
		return "new"
	default:
		return s
	}
}
