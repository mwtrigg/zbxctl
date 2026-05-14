package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// User represents a Zabbix user.
type User struct {
	UserID   string `json:"userid"`
	Username string `json:"username"`
	Name     string `json:"name"`    // first name
	Surname  string `json:"surname"`
	RoleID   string `json:"roleid"`
	Type     string `json:"type"` // "1"=user, "2"=admin, "3"=super admin
}

// UserFilter controls which users are returned.
type UserFilter struct {
	Limit int
}

// GetUsers returns a list of users.
func GetUsers(c *api.Client, f UserFilter) ([]User, error) {
	params := map[string]any{
		"output":    []string{"userid", "username", "name", "surname", "roleid", "type"},
		"sortfield": "username",
		"sortorder": "ASC",
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("user.get", params)
	if err != nil {
		return nil, fmt.Errorf("user.get: %w", err)
	}
	var users []User
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("parsing users: %w", err)
	}
	return users, nil
}

// GetUser returns a single user by numeric ID or username.
func GetUser(c *api.Client, idOrName string) (*User, error) {
	params := map[string]any{
		"output": []string{"userid", "username", "name", "surname", "roleid", "type"},
	}
	if isNumeric(idOrName) {
		params["userids"] = []string{idOrName}
	} else {
		params["filter"] = map[string]any{"username": []string{idOrName}}
	}

	raw, err := c.Call("user.get", params)
	if err != nil {
		return nil, fmt.Errorf("user.get: %w", err)
	}
	var users []User
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("parsing user: %w", err)
	}
	if len(users) == 0 {
		return nil, &NotFoundError{Resource: "user", Query: idOrName}
	}
	return &users[0], nil
}
