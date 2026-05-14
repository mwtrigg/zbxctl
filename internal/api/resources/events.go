package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// EventHost is a minimal host reference embedded in event results.
type EventHost struct {
	HostID string `json:"hostid"`
	Host   string `json:"host"`
}

// Event represents a Zabbix event.
type Event struct {
	EventID      string      `json:"eventid"`
	ObjectID     string      `json:"objectid"` // triggerid
	Name         string      `json:"name"`
	Value        string      `json:"value"`        // "0"=OK, "1"=PROBLEM
	Acknowledged string      `json:"acknowledged"` // "0"|"1"
	Clock        string      `json:"clock"`        // Unix timestamp
	Hosts        []EventHost `json:"hosts"`
}

// EventFilter controls which events are returned.
type EventFilter struct {
	HostIDs []string
	Since   int64 // Unix timestamp; 0 = no filter
	Until   int64 // Unix timestamp; 0 = no filter
	Limit   int
}

// GetEvents returns a list of trigger events (source=0, object=0).
func GetEvents(c *api.Client, f EventFilter) ([]Event, error) {
	params := map[string]any{
		"output":      []string{"eventid", "objectid", "name", "value", "acknowledged", "clock"},
		"selectHosts": []string{"hostid", "host"},
		"source":      0, // trigger events
		"object":      0, // trigger
		"sortfield":   "clock",
		"sortorder":   "DESC",
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if f.Since > 0 {
		params["time_from"] = f.Since
	}
	if f.Until > 0 {
		params["time_till"] = f.Until
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("event.get", params)
	if err != nil {
		return nil, fmt.Errorf("event.get: %w", err)
	}
	var events []Event
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, fmt.Errorf("parsing events: %w", err)
	}
	return events, nil
}
