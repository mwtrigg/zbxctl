package resources

import (
	"encoding/json"
	"fmt"
	"strconv"

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

// EventTriggerInfo contains event timing and host context for history anchoring.
type EventTriggerInfo struct {
	EventID   string
	Clock     int64
	EventName string
	HostID    string
	Host      string
}

// GetEventTriggerInfo resolves an event to its timestamp and the first host
// associated with the event's trigger. Used for --around-event history queries.
func GetEventTriggerInfo(c *api.Client, eventID string) (*EventTriggerInfo, error) {
	rawEvt, err := c.Call("event.get", map[string]any{
		"eventids": []string{eventID},
		"output":   []string{"eventid", "clock", "objectid", "name"},
	})
	if err != nil {
		return nil, fmt.Errorf("event.get: %w", err)
	}
	var evts []struct {
		EventID  string `json:"eventid"`
		Clock    string `json:"clock"`
		ObjectID string `json:"objectid"` // triggerid
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(rawEvt, &evts); err != nil {
		return nil, fmt.Errorf("parsing event: %w", err)
	}
	if len(evts) == 0 {
		return nil, &NotFoundError{Resource: "event", Query: eventID}
	}
	ev := evts[0]

	clockInt, err := strconv.ParseInt(ev.Clock, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing event clock %q: %w", ev.Clock, err)
	}

	rawTrig, err := c.Call("trigger.get", map[string]any{
		"triggerids":  []string{ev.ObjectID},
		"selectHosts": []string{"hostid", "host"},
		"output":      []string{"triggerid"},
	})
	if err != nil {
		return nil, fmt.Errorf("trigger.get: %w", err)
	}
	var trigs []struct {
		Hosts []struct {
			HostID string `json:"hostid"`
			Host   string `json:"host"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(rawTrig, &trigs); err != nil {
		return nil, fmt.Errorf("parsing trigger: %w", err)
	}
	info := &EventTriggerInfo{
		EventID:   ev.EventID,
		Clock:     clockInt,
		EventName: ev.Name,
	}
	if len(trigs) > 0 && len(trigs[0].Hosts) > 0 {
		info.HostID = trigs[0].Hosts[0].HostID
		info.Host = trigs[0].Hosts[0].Host
	}
	return info, nil
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
