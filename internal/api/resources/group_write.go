package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// GroupInfo is a host group with how many hosts are in it.
type GroupInfo struct {
	GroupID   string
	Name      string
	HostCount int
}

// GetGroupInfo resolves a host group by ID or exact name.
func GetGroupInfo(c *api.Client, idOrName string) (*GroupInfo, error) {
	params := map[string]any{"output": []string{"groupid", "name"}, "selectHosts": "count"}
	if isNumeric(idOrName) {
		params["groupids"] = []string{idOrName}
	} else {
		params["filter"] = map[string]any{"name": []string{idOrName}}
	}
	raw, err := c.Call("hostgroup.get", params)
	if err != nil {
		return nil, fmt.Errorf("hostgroup.get: %w", err)
	}
	var rs []struct {
		GroupID string `json:"groupid"`
		Name    string `json:"name"`
		Hosts   string `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing host group: %w", err)
	}
	if len(rs) == 0 {
		return nil, &NotFoundError{Resource: "host group", Query: idOrName}
	}
	n := 0
	fmt.Sscanf(rs[0].Hosts, "%d", &n)
	return &GroupInfo{GroupID: rs[0].GroupID, Name: rs[0].Name, HostCount: n}, nil
}

// CreateGroup creates a host group and returns its ID.
func CreateGroup(c *api.Client, name string) (string, error) {
	raw, err := c.Call("hostgroup.create", map[string]any{"name": name})
	if err != nil {
		return "", fmt.Errorf("hostgroup.create: %w", err)
	}
	var res struct {
		IDs []string `json:"groupids"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.IDs) == 0 {
		return "", fmt.Errorf("parsing hostgroup.create result: %v", err)
	}
	return res.IDs[0], nil
}

// RenameGroup renames a host group.
func RenameGroup(c *api.Client, id, name string) error {
	if _, err := c.Call("hostgroup.update", map[string]any{"groupid": id, "name": name}); err != nil {
		return fmt.Errorf("hostgroup.update: %w", err)
	}
	return nil
}

// DeleteGroup deletes a host group by ID.
func DeleteGroup(c *api.Client, id string) error {
	if _, err := c.Call("hostgroup.delete", []string{id}); err != nil {
		return fmt.Errorf("hostgroup.delete: %w", err)
	}
	return nil
}
