package resources

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// ProblemSpan is the time a real problem was open on a host.
type ProblemSpan struct {
	EventID string
	HostID  string
	Name    string
	Start   int64
	End     int64 // recovery time, or 0 when still open
}

// GetProblemSpans returns real problem periods, for comparing an alert idea
// against what actually happened. Select them by event name substring
// (nameSearch, across hostIDs) and/or by explicit event IDs.
func GetProblemSpans(c *api.Client, hostIDs []string, nameSearch string, eventIDs []string, from, till int64) ([]ProblemSpan, error) {
	params := map[string]any{
		"source": 0, "object": 0,
		"output":      []string{"eventid", "clock", "r_eventid", "name"},
		"selectHosts": []string{"hostid"},
		"sortfield":   []string{"clock", "eventid"}, "sortorder": "ASC",
	}
	if len(eventIDs) > 0 {
		params["eventids"] = eventIDs
	} else {
		params["filter"] = map[string]any{"value": []string{"1"}}
		if len(hostIDs) > 0 {
			params["hostids"] = hostIDs
		}
		if nameSearch != "" {
			params["search"] = map[string]any{"name": nameSearch}
		}
		if from > 0 {
			params["time_from"] = from
		}
		if till > 0 {
			params["time_till"] = till
		}
	}
	raw, err := c.Call("event.get", params)
	if err != nil {
		return nil, fmt.Errorf("event.get: %w", err)
	}
	var evs []struct {
		EventID string `json:"eventid"`
		Clock   string `json:"clock"`
		RID     string `json:"r_eventid"`
		Name    string `json:"name"`
		Hosts   []struct {
			HostID string `json:"hostid"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &evs); err != nil {
		return nil, fmt.Errorf("parsing events: %w", err)
	}
	var rids []string
	for _, e := range evs {
		if e.RID != "" && e.RID != "0" {
			rids = append(rids, e.RID)
		}
	}
	recovery := map[string]int64{}
	if len(rids) > 0 {
		raw, err := c.Call("event.get", map[string]any{"eventids": rids, "output": []string{"eventid", "clock"}})
		if err != nil {
			return nil, fmt.Errorf("event.get (recovery): %w", err)
		}
		var rs []struct {
			EventID string `json:"eventid"`
			Clock   string `json:"clock"`
		}
		if err := json.Unmarshal(raw, &rs); err != nil {
			return nil, fmt.Errorf("parsing recovery events: %w", err)
		}
		for _, r := range rs {
			recovery[r.EventID], _ = strconv.ParseInt(r.Clock, 10, 64)
		}
	}
	var out []ProblemSpan
	for _, e := range evs {
		start, _ := strconv.ParseInt(e.Clock, 10, 64)
		for _, h := range e.Hosts {
			out = append(out, ProblemSpan{EventID: e.EventID, HostID: h.HostID, Name: e.Name, Start: start, End: recovery[e.RID]})
		}
	}
	return out, nil
}
