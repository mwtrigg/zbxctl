package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Role is a Zabbix user role.
type Role struct {
	RoleID string `json:"roleid"`
	Name   string `json:"name"`
}

// GetRole resolves a role by numeric ID or exact name.
func GetRole(c *api.Client, idOrName string) (*Role, error) {
	params := map[string]any{"output": []string{"roleid", "name"}}
	if isNumeric(idOrName) {
		params["roleids"] = []string{idOrName}
	} else {
		params["filter"] = map[string]any{"name": []string{idOrName}}
	}
	raw, err := c.Call("role.get", params)
	if err != nil {
		return nil, fmt.Errorf("role.get: %w", err)
	}
	var roles []Role
	if err := json.Unmarshal(raw, &roles); err != nil {
		return nil, fmt.Errorf("parsing roles: %w", err)
	}
	if len(roles) == 0 {
		return nil, &NotFoundError{Resource: "role", Query: idOrName}
	}
	return &roles[0], nil
}

// GetUserGroup resolves a user group by numeric ID or exact name.
func GetUserGroup(c *api.Client, idOrName string) (*UserGroup, error) {
	params := map[string]any{"output": []string{"usrgrpid", "name", "gui_access", "users_status"}}
	if isNumeric(idOrName) {
		params["usrgrpids"] = []string{idOrName}
	} else {
		params["filter"] = map[string]any{"name": []string{idOrName}}
	}
	raw, err := c.Call("usergroup.get", params)
	if err != nil {
		return nil, fmt.Errorf("usergroup.get: %w", err)
	}
	var groups []UserGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("parsing user groups: %w", err)
	}
	if len(groups) == 0 {
		return nil, &NotFoundError{Resource: "user group", Query: idOrName}
	}
	return &groups[0], nil
}

// CreateUserInput holds the fields for user.create.
type CreateUserInput struct {
	Username string
	Password string
	Name     string
	Surname  string
	RoleID   string
	GroupIDs []string
}

// CreateUser creates a user and returns its ID.
func CreateUser(c *api.Client, in CreateUserInput) (string, error) {
	groups := make([]map[string]string, len(in.GroupIDs))
	for i, id := range in.GroupIDs {
		groups[i] = map[string]string{"usrgrpid": id}
	}
	params := map[string]any{"username": in.Username, "passwd": in.Password, "roleid": in.RoleID, "usrgrps": groups}
	if in.Name != "" {
		params["name"] = in.Name
	}
	if in.Surname != "" {
		params["surname"] = in.Surname
	}
	raw, err := c.Call("user.create", params)
	if err != nil {
		return "", fmt.Errorf("user.create: %w", err)
	}
	var res struct {
		UserIDs []string `json:"userids"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.UserIDs) == 0 {
		return "", fmt.Errorf("parsing user.create result: %v", err)
	}
	return res.UserIDs[0], nil
}

// DeleteUser deletes a user by ID.
func DeleteUser(c *api.Client, userID string) error {
	if _, err := c.Call("user.delete", []string{userID}); err != nil {
		return fmt.Errorf("user.delete: %w", err)
	}
	return nil
}

// CreateUserGroup creates a user group and returns its ID. guiAccess is the
// Zabbix code "0".."3"; disabled sets users_status to "1".
func CreateUserGroup(c *api.Client, name, guiAccess string, disabled bool) (string, error) {
	status := 0
	if disabled {
		status = 1
	}
	raw, err := c.Call("usergroup.create", map[string]any{"name": name, "gui_access": guiAccess, "users_status": status})
	if err != nil {
		return "", fmt.Errorf("usergroup.create: %w", err)
	}
	var res struct {
		IDs []string `json:"usrgrpids"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.IDs) == 0 {
		return "", fmt.Errorf("parsing usergroup.create result: %v", err)
	}
	return res.IDs[0], nil
}

// DeleteUserGroup deletes a user group by ID.
func DeleteUserGroup(c *api.Client, id string) error {
	if _, err := c.Call("usergroup.delete", []string{id}); err != nil {
		return fmt.Errorf("usergroup.delete: %w", err)
	}
	return nil
}
