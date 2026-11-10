package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Group represents a Zabbix host group.
type Group struct {
	GroupID string `json:"groupid"`
	Name    string `json:"name"`
}

// GroupFilter controls which groups are returned by GetGroups.
type GroupFilter struct {
	Search string
	Limit  int
}

// GetGroups returns a list of host groups.
func GetGroups(c *api.Client, f GroupFilter) ([]Group, error) {
	params := map[string]any{
		"output":    []string{"groupid", "name"},
		"sortfield": "name",
		"sortorder": "ASC",
	}
	if f.Search != "" {
		params["search"] = map[string]any{"name": f.Search}
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("hostgroup.get", params)
	if err != nil {
		return nil, fmt.Errorf("hostgroup.get: %w", err)
	}
	var groups []Group
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("parsing groups: %w", err)
	}
	return groups, nil
}

// GetGroupByName returns a single group by exact name.
func GetGroupByName(c *api.Client, name string) (*Group, error) {
	params := map[string]any{
		"output": []string{"groupid", "name"},
		"filter": map[string]any{"name": []string{name}},
	}

	raw, err := c.Call("hostgroup.get", params)
	if err != nil {
		return nil, fmt.Errorf("hostgroup.get: %w", err)
	}
	var groups []Group
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("parsing group: %w", err)
	}
	if len(groups) == 0 {
		return nil, &NotFoundError{Resource: "group", Query: name}
	}
	return &groups[0], nil
}
