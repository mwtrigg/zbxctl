package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Template represents a Zabbix template.
type Template struct {
	TemplateID  string `json:"templateid"`
	Host        string `json:"host"`        // technical name
	Name        string `json:"name"`        // visible name
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

	raw, err := c.Call("template.get", params)
	if err != nil {
		return nil, fmt.Errorf("template.get: %w", err)
	}
	var templates []Template
	if err := json.Unmarshal(raw, &templates); err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	return templates, nil
}

// GetTemplate returns a single template by numeric ID or technical name.
func GetTemplate(c *api.Client, idOrName string) (*Template, error) {
	params := map[string]any{
		"output":         []string{"templateid", "host", "name", "description"},
		"selectItems":    "count",
		"selectTriggers": "count",
		"selectGraphs":   "count",
	}
	if isNumeric(idOrName) {
		params["templateids"] = []string{idOrName}
	} else {
		params["filter"] = map[string]any{"host": []string{idOrName}}
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
		return nil, &NotFoundError{Resource: "template", Query: idOrName}
	}
	return &templates[0], nil
}
