package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// ItemTypes are the item types zbxctl can create, by flag name.
var ItemTypes = map[string]string{"agent": "0", "trapper": "2", "simple": "3", "agent-active": "7", "calculated": "15", "dependent": "18"}

// ValueTypes are the item value types, by flag name.
var ValueTypes = map[string]string{"float": "0", "char": "1", "log": "2", "uint": "3", "text": "4"}

// ItemFull is an item with everything create/update/delete need to show.
type ItemFull struct {
	ID, HostID, Host, Name, Key, Type, ValueType, Delay, Units, History, Trends, Status, Flags, Description, Params, TemplateID string
}

// GetItemFull resolves an item by numeric ID, or by exact key on hostID.
func GetItemFull(c *api.Client, ref, hostID string) (*ItemFull, error) {
	params := map[string]any{
		"output":      []string{"itemid", "hostid", "name", "key_", "type", "value_type", "delay", "units", "history", "trends", "status", "flags", "description", "params", "templateid"},
		"selectHosts": []string{"host"},
	}
	if isNumeric(ref) {
		params["itemids"] = []string{ref}
	} else {
		if hostID == "" {
			return nil, fmt.Errorf("an item key needs --host; or pass the numeric item ID")
		}
		params["hostids"] = []string{hostID}
		params["filter"] = map[string]any{"key_": []string{ref}}
	}
	raw, err := c.Call("item.get", params)
	if err != nil {
		return nil, fmt.Errorf("item.get: %w", err)
	}
	var rs []struct {
		ItemID      string `json:"itemid"`
		HostID      string `json:"hostid"`
		Name        string `json:"name"`
		Key         string `json:"key_"`
		Type        string `json:"type"`
		ValueType   string `json:"value_type"`
		Delay       string `json:"delay"`
		Units       string `json:"units"`
		History     string `json:"history"`
		Trends      string `json:"trends"`
		Status      string `json:"status"`
		Flags       string `json:"flags"`
		Description string `json:"description"`
		Params      string `json:"params"`
		TemplateID  string `json:"templateid"`
		Hosts       []struct {
			Host string `json:"host"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing item: %w", err)
	}
	if len(rs) == 0 {
		return nil, &NotFoundError{Resource: "item", Query: ref}
	}
	r := rs[0]
	it := &ItemFull{ID: r.ItemID, HostID: r.HostID, Name: r.Name, Key: r.Key, Type: r.Type, ValueType: r.ValueType, Delay: r.Delay, Units: r.Units,
		History: r.History, Trends: r.Trends, Status: r.Status, Flags: r.Flags, Description: r.Description, Params: r.Params, TemplateID: r.TemplateID}
	if len(r.Hosts) > 0 {
		it.Host = r.Hosts[0].Host
	}
	return it, nil
}

// CreateItem creates an item from a parameter map (hostid, name, key_, type, ...).
func CreateItem(c *api.Client, params map[string]any) (string, error) {
	raw, err := c.Call("item.create", params)
	if err != nil {
		return "", fmt.Errorf("item.create: %w", err)
	}
	var res struct {
		IDs []string `json:"itemids"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.IDs) == 0 {
		return "", fmt.Errorf("parsing item.create result: %v", err)
	}
	return res.IDs[0], nil
}

// UpdateItem changes fields of an item.
func UpdateItem(c *api.Client, id string, fields map[string]any) error {
	p := map[string]any{"itemid": id}
	for k, v := range fields {
		p[k] = v
	}
	if _, err := c.Call("item.update", p); err != nil {
		return fmt.Errorf("item.update: %w", err)
	}
	return nil
}

// DeleteItem deletes an item by ID.
func DeleteItem(c *api.Client, id string) error {
	if _, err := c.Call("item.delete", []string{id}); err != nil {
		return fmt.Errorf("item.delete: %w", err)
	}
	return nil
}

// MainAgentInterfaceID returns the host's main agent interface ID, which
// agent and simple-check items need.
func MainAgentInterfaceID(c *api.Client, hostID string) (string, error) {
	raw, err := c.Call("hostinterface.get", map[string]any{"hostids": []string{hostID}, "output": []string{"interfaceid", "type", "main"}})
	if err != nil {
		return "", fmt.Errorf("hostinterface.get: %w", err)
	}
	var ifs []struct {
		ID   string `json:"interfaceid"`
		Type string `json:"type"`
		Main string `json:"main"`
	}
	if err := json.Unmarshal(raw, &ifs); err != nil {
		return "", fmt.Errorf("parsing interfaces: %w", err)
	}
	for _, i := range ifs {
		if i.Type == "1" && i.Main == "1" {
			return i.ID, nil
		}
	}
	return "", fmt.Errorf("the host has no main agent interface; this item type needs one")
}

// TriggerFull is a trigger with its expression and settings.
type TriggerFull struct {
	ID, Description, Expression, Priority, Status, Comments, ManualClose, Flags, TemplateID string
	Hosts                                                                                   []string
}

// GetTriggerFull looks a trigger up by ID with its expanded expression.
func GetTriggerFull(c *api.Client, id string) (*TriggerFull, error) {
	raw, err := c.Call("trigger.get", map[string]any{
		"triggerids":        []string{id},
		"output":            []string{"triggerid", "description", "expression", "priority", "status", "comments", "manual_close", "flags", "templateid"},
		"selectHosts":       []string{"host"},
		"expandExpression":  true,
		"expandDescription": true,
	})
	if err != nil {
		return nil, fmt.Errorf("trigger.get: %w", err)
	}
	var rs []struct {
		TriggerID   string `json:"triggerid"`
		Description string `json:"description"`
		Expression  string `json:"expression"`
		Priority    string `json:"priority"`
		Status      string `json:"status"`
		Comments    string `json:"comments"`
		ManualClose string `json:"manual_close"`
		Flags       string `json:"flags"`
		TemplateID  string `json:"templateid"`
		Hosts       []struct {
			Host string `json:"host"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing trigger: %w", err)
	}
	if len(rs) == 0 {
		return nil, &NotFoundError{Resource: "trigger", Query: id}
	}
	r := rs[0]
	t := &TriggerFull{ID: r.TriggerID, Description: r.Description, Expression: r.Expression, Priority: r.Priority, Status: r.Status,
		Comments: r.Comments, ManualClose: r.ManualClose, Flags: r.Flags, TemplateID: r.TemplateID}
	for _, h := range r.Hosts {
		t.Hosts = append(t.Hosts, h.Host)
	}
	return t, nil
}

// CreateTrigger creates a trigger from a parameter map and returns its ID.
func CreateTrigger(c *api.Client, params map[string]any) (string, error) {
	raw, err := c.Call("trigger.create", params)
	if err != nil {
		return "", fmt.Errorf("trigger.create: %w", err)
	}
	var res struct {
		IDs []string `json:"triggerids"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.IDs) == 0 {
		return "", fmt.Errorf("parsing trigger.create result: %v", err)
	}
	return res.IDs[0], nil
}

// UpdateTrigger changes fields of a trigger.
func UpdateTrigger(c *api.Client, id string, fields map[string]any) error {
	p := map[string]any{"triggerid": id}
	for k, v := range fields {
		p[k] = v
	}
	if _, err := c.Call("trigger.update", p); err != nil {
		return fmt.Errorf("trigger.update: %w", err)
	}
	return nil
}

// DeleteTrigger deletes a trigger by ID.
func DeleteTrigger(c *api.Client, id string) error {
	if _, err := c.Call("trigger.delete", []string{id}); err != nil {
		return fmt.Errorf("trigger.delete: %w", err)
	}
	return nil
}
