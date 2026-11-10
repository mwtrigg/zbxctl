package resources

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Template represents a Zabbix template.
type Template struct {
	TemplateID  string `json:"templateid"`
	Host        string `json:"host"` // technical name
	Name        string `json:"name"` // visible name
	Description string `json:"description"`
	Items       string `json:"items"`    // count (string from API)
	Triggers    string `json:"triggers"` // count
	Graphs      string `json:"graphs"`   // count
}

// TemplateFilter controls which templates are returned.
type TemplateFilter struct {
	Search string
	Limit  int
}

// GetTemplates returns a list of templates.
func GetTemplates(c *api.Client, f TemplateFilter) ([]Template, error) {
	params := map[string]any{
		"output":         []string{"templateid", "host", "name", "description"},
		"selectItems":    "count",
		"selectTriggers": "count",
		"selectGraphs":   "count",
		"sortfield":      "host",
		"sortorder":      "ASC",
	}
	if f.Search != "" {
		params["search"] = map[string]any{
			"host": f.Search,
			"name": f.Search,
		}
		params["searchByAny"] = true
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.CallList("template.get", params)
	if err != nil {
		return nil, fmt.Errorf("template.get: %w", err)
	}
	var templates []Template
	if err := json.Unmarshal(raw, &templates); err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	return templates, nil
}

// GetTemplate returns a single template by numeric ID, technical name, or
// visible name (looked up in that order, like GetHost).
func GetTemplate(c *api.Client, idOrName string) (*Template, error) {
	if isNumeric(idOrName) {
		return templateGetOne(c, map[string]any{"templateids": []string{idOrName}}, idOrName)
	}
	t, err := templateGetOne(c, map[string]any{"filter": map[string]any{"host": []string{idOrName}}}, idOrName)
	var nfe *NotFoundError
	if err != nil && errors.As(err, &nfe) {
		return templateGetOne(c, map[string]any{"filter": map[string]any{"name": []string{idOrName}}}, idOrName)
	}
	return t, err
}

func templateGetOne(c *api.Client, extra map[string]any, query string) (*Template, error) {
	params := map[string]any{
		"output":         []string{"templateid", "host", "name", "description"},
		"selectItems":    "count",
		"selectTriggers": "count",
		"selectGraphs":   "count",
	}
	for k, v := range extra {
		params[k] = v
	}
	raw, err := c.Call("template.get", params)
	if err != nil {
		return nil, fmt.Errorf("template.get: %w", err)
	}
	var templates []Template
	if err := json.Unmarshal(raw, &templates); err != nil {
		return nil, fmt.Errorf("parsing template: %w", err)
	}
	if len(templates) == 0 {
		return nil, &NotFoundError{Resource: "template", Query: query}
	}
	return &templates[0], nil
}
