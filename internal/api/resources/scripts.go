package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Script represents a Zabbix script.
type Script struct {
	ScriptID    string `json:"scriptid"`
	Name        string `json:"name"`
	Type        string `json:"type"`       // see scriptTypeLabel
	ExecuteOn   string `json:"execute_on"` // see executeOnLabel; only meaningful for type=script
	Description string `json:"description"`
	Command     string `json:"command"`

	// What decides where and how the command runs. They are part of the definition
	// that an allowlist pin covers: changing a script from the agent to the server
	// keeps its name and command but changes who runs it.
	Scope      string          `json:"scope"`
	Port       string          `json:"port"`
	AuthType   string          `json:"authtype"`
	Username   string          `json:"username"`
	Timeout    string          `json:"timeout"`
	Parameters json.RawMessage `json:"parameters"`
}

// Definition is the canonical JSON of everything about a script that decides what
// runs where: the allowlist pin hashes exactly this.
func (s Script) Definition() []byte {
	b, _ := json.Marshal(struct {
		Name       string          `json:"name"`
		Type       string          `json:"type"`
		Scope      string          `json:"scope"`
		ExecuteOn  string          `json:"execute_on"`
		Command    string          `json:"command"`
		Parameters json.RawMessage `json:"parameters"`
		Username   string          `json:"username"`
		AuthType   string          `json:"authtype"`
		Port       string          `json:"port"`
		Timeout    string          `json:"timeout"`
	}{s.Name, s.Type, s.Scope, s.ExecuteOn, s.Command, s.Parameters, s.Username, s.AuthType, s.Port, s.Timeout})
	return b
}

// ScriptFilter controls which scripts are returned.
type ScriptFilter struct {
	HostIDs []string // if set, only scripts applicable to these hosts
}

// ScriptResult is the output of a script execution.
type ScriptResult struct {
	ScriptID   string `json:"scriptid"`
	ScriptName string `json:"script_name"`
	HostID     string `json:"hostid"`
	Host       string `json:"host"`
	Response   string `json:"response"` // "success" or "failed"
	Value      string `json:"value"`    // stdout on success, error message on failure
}

// GetScripts returns scripts visible to the authenticated user.
func GetScripts(c *api.Client, f ScriptFilter) ([]Script, error) {
	params := map[string]any{
		"output": []string{"scriptid", "name", "type", "execute_on", "description", "command"},
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}

	raw, err := c.Call("script.get", params)
	if err != nil {
		return nil, fmt.Errorf("script.get: %w", err)
	}
	var scripts []Script
	if err := json.Unmarshal(raw, &scripts); err != nil {
		return nil, fmt.Errorf("parsing scripts: %w", err)
	}
	return scripts, nil
}

// GetScriptByNameOrID returns a single script by numeric ID or exact name.
// If hostID is non-empty, the search is scoped to scripts applicable to that host.
func GetScriptByNameOrID(c *api.Client, nameOrID string, hostID string) (*Script, error) {
	params := map[string]any{"output": "extend"}
	if isNumeric(nameOrID) {
		params["scriptids"] = []string{nameOrID}
	} else {
		params["filter"] = map[string]any{"name": []string{nameOrID}}
	}
	if hostID != "" {
		params["hostids"] = []string{hostID}
	}

	raw, err := c.Call("script.get", params)
	if err != nil {
		return nil, fmt.Errorf("script.get: %w", err)
	}
	var scripts []Script
	if err := json.Unmarshal(raw, &scripts); err != nil {
		return nil, fmt.Errorf("parsing scripts: %w", err)
	}
	if len(scripts) == 0 {
		return nil, &NotFoundError{Resource: "script", Query: nameOrID}
	}
	if len(scripts) > 1 {
		// Several scripts with this name: taking the first would run whichever Zabbix
		// happens to list first, which is not what anyone reviewed.
		return nil, fmt.Errorf("%d scripts are named %q: use the script ID", len(scripts), nameOrID)
	}
	return &scripts[0], nil
}

// RunScript executes a script on a host and returns the result.
func RunScript(c *api.Client, scriptID, hostID string) (*ScriptResult, error) {
	params := map[string]any{
		"scriptid": scriptID,
		"hostid":   hostID,
	}
	raw, err := c.Call("script.execute", params)
	if err != nil {
		return nil, fmt.Errorf("script.execute: %w", err)
	}
	var result struct {
		Response string `json:"response"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing script result: %w", err)
	}
	return &ScriptResult{
		ScriptID: scriptID,
		HostID:   hostID,
		Response: result.Response,
		Value:    result.Value,
	}, nil
}

func ScriptTypeLabel(s string) string {
	switch s {
	case "0":
		return "script"
	case "1":
		return "ipmi"
	case "2":
		return "ssh"
	case "3":
		return "telnet"
	case "5":
		return "webhook"
	default:
		return s
	}
}

func ExecuteOnLabel(s string) string {
	switch s {
	case "0":
		return "server"
	case "1":
		return "agent"
	case "2":
		return "server-proxy"
	default:
		return s
	}
}
