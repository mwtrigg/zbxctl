package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// EnableHost sets a host's status to monitored (status=0).
func EnableHost(c *api.Client, hostID string) error {
	return setHostStatus(c, hostID, "0")
}

// DisableHost sets a host's status to unmonitored (status=1).
func DisableHost(c *api.Client, hostID string) error {
	return setHostStatus(c, hostID, "1")
}

func setHostStatus(c *api.Client, hostID string, status string) error {
	params := map[string]any{
		"hostid": hostID,
		"status": status,
	}
	raw, err := c.Call("host.update", params)
	if err != nil {
		return fmt.Errorf("host.update: %w", err)
	}
	// Response is {"hostids": ["<id>"]}
	var result struct {
		HostIDs []string `json:"hostids"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("parsing host.update response: %w", err)
	}
	if len(result.HostIDs) == 0 {
		return fmt.Errorf("host.update returned empty hostids")
	}
	return nil
}

// EnableTrigger sets a trigger's status to enabled (status=0).
func EnableTrigger(c *api.Client, triggerID string) error {
	return setTriggerStatus(c, triggerID, "0")
}

// DisableTrigger sets a trigger's status to disabled (status=1).
func DisableTrigger(c *api.Client, triggerID string) error {
	return setTriggerStatus(c, triggerID, "1")
}

func setTriggerStatus(c *api.Client, triggerID string, status string) error {
	params := map[string]any{
		"triggerid": triggerID,
		"status":    status,
	}
	raw, err := c.Call("trigger.update", params)
	if err != nil {
		return fmt.Errorf("trigger.update: %w", err)
	}
	var result struct {
		TriggerIDs []string `json:"triggerids"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("parsing trigger.update response: %w", err)
	}
	if len(result.TriggerIDs) == 0 {
		return fmt.Errorf("trigger.update returned empty triggerids")
	}
	return nil
}

// GetTriggerByID returns a single trigger by numeric ID (for pre-flight validation).
func GetTriggerByID(c *api.Client, id string) (*Trigger, error) {
	params := map[string]any{
		"output":      []string{"triggerid", "description", "priority", "status", "value", "lastchange"},
		"selectHosts": []string{"hostid", "host"},
		"triggerids":  []string{id},
	}
	raw, err := c.Call("trigger.get", params)
	if err != nil {
		return nil, fmt.Errorf("trigger.get: %w", err)
	}
	var triggers []Trigger
	if err := json.Unmarshal(raw, &triggers); err != nil {
		return nil, fmt.Errorf("parsing trigger: %w", err)
	}
	if len(triggers) == 0 {
		return nil, &NotFoundError{Resource: "trigger", Query: id}
	}
	return &triggers[0], nil
}
