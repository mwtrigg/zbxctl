package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// EventAckState is what an acknowledge needs to know about an event.
type EventAckState struct {
	EventID      string
	Name         string
	Acknowledged bool
	Closed       bool   // the problem has a recovery event
	TriggerID    string // the event's trigger
	ManualClose  bool   // the trigger allows closing problems by hand
}

// GetEventAckState looks up an event and whether its trigger allows manual close.
func GetEventAckState(c *api.Client, eventID string) (*EventAckState, error) {
	raw, err := c.Call("event.get", map[string]any{
		"eventids": []string{eventID},
		"output":   []string{"eventid", "name", "acknowledged", "r_eventid", "objectid"},
	})
	if err != nil {
		return nil, fmt.Errorf("event.get: %w", err)
	}
	var evs []struct {
		EventID      string `json:"eventid"`
		Name         string `json:"name"`
		Acknowledged string `json:"acknowledged"`
		RecoveryID   string `json:"r_eventid"`
		ObjectID     string `json:"objectid"`
	}
	if err := json.Unmarshal(raw, &evs); err != nil {
		return nil, fmt.Errorf("parsing event: %w", err)
	}
	if len(evs) == 0 {
		return nil, &NotFoundError{Resource: "event", Query: eventID}
	}
	e := evs[0]
	st := &EventAckState{EventID: e.EventID, Name: e.Name, Acknowledged: e.Acknowledged == "1",
		Closed: e.RecoveryID != "" && e.RecoveryID != "0", TriggerID: e.ObjectID}

	raw, err = c.Call("trigger.get", map[string]any{"triggerids": []string{e.ObjectID}, "output": []string{"triggerid", "manual_close"}})
	if err != nil {
		return nil, fmt.Errorf("trigger.get: %w", err)
	}
	var trigs []struct {
		ManualClose string `json:"manual_close"`
	}
	if err := json.Unmarshal(raw, &trigs); err == nil && len(trigs) > 0 {
		st.ManualClose = trigs[0].ManualClose == "1"
	}
	return st, nil
}
