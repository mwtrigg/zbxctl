package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// UserGroup represents a Zabbix user group.
type UserGroup struct {
	UserGroupID string `json:"usrgrpid"`
	Name        string `json:"name"`
	GUIAccess   string `json:"gui_access"`   // "0"=default, "1"=internal, "2"=LDAP, "3"=disabled
	UsersStatus string `json:"users_status"` // "0"=enabled, "1"=disabled
}

// UserGroupFilter controls which user groups are returned.
type UserGroupFilter struct {
	Limit int
}

// GetUserGroups returns a list of user groups.
func GetUserGroups(c *api.Client, f UserGroupFilter) ([]UserGroup, error) {
	params := map[string]any{
		"output":    []string{"usrgrpid", "name", "gui_access", "users_status"},
		"sortfield": "name",
		"sortorder": "ASC",
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("usergroup.get", params)
	if err != nil {
		return nil, fmt.Errorf("usergroup.get: %w", err)
	}
	var groups []UserGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("parsing usergroups: %w", err)
	}
	return groups, nil
}
