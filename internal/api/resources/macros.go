package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Macro is a user macro. Secret macros come back without a value.
type Macro struct {
	Macro       string
	Value       string
	Type        string // "0" text, "1" secret, "2" vault
	Description string
	Host        string // empty for global macros
}

// TypeLabel names the macro type.
func (m Macro) TypeLabel() string {
	switch m.Type {
	case "1":
		return "secret"
	case "2":
		return "vault"
	}
	return "text"
}

// GetMacros returns host macros for hostIDs (all hosts when empty), or the
// global macros when global is true.
func GetMacros(c *api.Client, hostIDs []string, global bool, search string) ([]Macro, error) {
	params := map[string]any{"output": []string{"macro", "value", "type", "description"}, "sortfield": "macro"}
	if global {
		params["globalmacro"] = true
	} else {
		params["selectHosts"] = []string{"host"}
		if len(hostIDs) > 0 {
			params["hostids"] = hostIDs
		}
	}
	if search != "" {
		params["search"] = map[string]any{"macro": search}
	}
	raw, err := c.CallList("usermacro.get", params)
	if err != nil {
		return nil, fmt.Errorf("usermacro.get: %w", err)
	}
	var rs []struct {
		Macro       string `json:"macro"`
		Value       string `json:"value"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Hosts       []struct {
			Host string `json:"host"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing macros: %w", err)
	}
	out := make([]Macro, len(rs))
	for i, r := range rs {
		out[i] = Macro{Macro: r.Macro, Value: r.Value, Type: r.Type, Description: r.Description}
		if len(r.Hosts) > 0 {
			out[i].Host = r.Hosts[0].Host
		}
	}
	return out, nil
}

// Action is an action (trigger action, discovery, autoregistration, ...).
type Action struct {
	ActionID    string
	Name        string
	EventSource string // "0" trigger, "1" discovery, "2" autoregistration, "3" internal, "4" service
	Enabled     bool
	Conditions  int
}

// GetActions lists actions with their condition counts.
func GetActions(c *api.Client, limit int) ([]Action, error) {
	params := map[string]any{"output": []string{"actionid", "name", "eventsource", "status"}, "selectFilter": "extend", "sortfield": "name"}
	if limit > 0 {
		params["limit"] = limit
	}
	raw, err := c.CallList("action.get", params)
	if err != nil {
		return nil, fmt.Errorf("action.get: %w", err)
	}
	var rs []struct {
		ActionID    string `json:"actionid"`
		Name        string `json:"name"`
		EventSource string `json:"eventsource"`
		Status      string `json:"status"`
		Filter      struct {
			Conditions []json.RawMessage `json:"conditions"`
		} `json:"filter"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing actions: %w", err)
	}
	out := make([]Action, len(rs))
	for i, r := range rs {
		out[i] = Action{ActionID: r.ActionID, Name: r.Name, EventSource: r.EventSource, Enabled: r.Status == "0", Conditions: len(r.Filter.Conditions)}
	}
	return out, nil
}
