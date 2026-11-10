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
	byID := func(id string) (*Template, error) {
		return templateGetOne(c, map[string]any{"templateids": []string{id}}, id)
	}
	byName := func(name string) (*Template, error) {
		t, err := templateGetOne(c, map[string]any{"filter": map[string]any{"host": []string{name}}}, name)
		var nfe *NotFoundError
		if err != nil && errors.As(err, &nfe) {
			return templateGetOne(c, map[string]any{"filter": map[string]any{"name": []string{name}}}, name)
		}
		return t, err
	}
	mode, v := splitQualifier(idOrName)
	switch {
	case mode == "id":
		return byID(v)
	case mode == "name" || !isNumeric(v):
		return byName(v)
	}
	// All digits: an ID, or a template named like one. See GetHost.
	tid, ierr := byID(v)
	tname, nerr := byName(v)
	for _, err := range []error{ierr, nerr} {
		var nfe *NotFoundError
		if err != nil && !errors.As(err, &nfe) {
			return nil, err
		}
	}
	switch {
	case ierr == nil && nerr == nil && tid.TemplateID != tname.TemplateID:
		return nil, &AmbiguousError{Resource: "template", Query: v, ByID: fmt.Sprintf("template %q", tid.Host), ByName: fmt.Sprintf("template %q (ID %s)", tname.Host, tname.TemplateID)}
	case ierr == nil:
		return tid, nil
	case nerr == nil:
		return tname, nil
	}
	return nil, &NotFoundError{Resource: "template", Query: v}
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
