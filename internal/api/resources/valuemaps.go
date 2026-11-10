package resources

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Mapping is one entry of a value map.
type Mapping struct {
	Type     string `json:"type"` // "0" equals, "1" >=, "2" <=, "3" range, "4" regexp, "5" default
	Value    string `json:"value"`
	NewValue string `json:"newvalue"`
}

// MappingTypes name the Zabbix mapping type codes.
var MappingTypes = map[string]string{"equals": "0", "ge": "1", "le": "2", "range": "3", "regexp": "4", "default": "5"}

// Label renders a mapping like "0=Down", ">=5=High", "1-5=Low", "~^x=Text" or "default=Unknown".
func (m Mapping) Label() string {
	switch m.Type {
	case "1":
		return ">=" + m.Value + "=" + m.NewValue
	case "2":
		return "<=" + m.Value + "=" + m.NewValue
	case "3":
		return m.Value + "=" + m.NewValue
	case "4":
		return "~" + m.Value + "=" + m.NewValue
	case "5":
		return "default=" + m.NewValue
	}
	return m.Value + "=" + m.NewValue
}

// ValueMap is a value map; it belongs to one host or template.
type ValueMap struct {
	ID       string    `json:"valuemapid"`
	HostID   string    `json:"hostid"`
	Name     string    `json:"name"`
	Mappings []Mapping `json:"mappings"`
	Owner    string    `json:"-"` // host or template name, filled in by GetValueMaps
}

// MappingLabels lists the mappings as strings.
func (v ValueMap) MappingLabels() []string {
	out := make([]string, len(v.Mappings))
	for i, m := range v.Mappings {
		out[i] = m.Label()
	}
	return out
}

// ValueMapFilter narrows GetValueMaps.
type ValueMapFilter struct {
	HostIDs []string // hosts or templates that own them
	Search  string
	IDs     []string
	Name    string // exact
	Limit   int
}

// GetValueMaps lists value maps with the name of the host or template that owns each.
func GetValueMaps(c *api.Client, f ValueMapFilter) ([]ValueMap, error) {
	params := map[string]any{"output": []string{"valuemapid", "hostid", "name"}, "selectMappings": "extend", "sortfield": "name"}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if len(f.IDs) > 0 {
		params["valuemapids"] = f.IDs
	}
	if f.Search != "" {
		params["search"] = map[string]any{"name": f.Search}
	}
	if f.Name != "" {
		params["filter"] = map[string]any{"name": []string{f.Name}}
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}
	raw, err := c.CallList("valuemap.get", params)
	if err != nil {
		return nil, fmt.Errorf("valuemap.get: %w", err)
	}
	var vms []ValueMap
	if err := json.Unmarshal(raw, &vms); err != nil {
		return nil, fmt.Errorf("parsing value maps: %w", err)
	}
	owners, err := ownerNames(c, vms)
	if err != nil {
		return nil, err
	}
	for i := range vms {
		vms[i].Owner = owners[vms[i].HostID]
	}
	return vms, nil
}

// ownerNames maps host/template IDs to names with one host.get and one template.get.
func ownerNames(c *api.Client, vms []ValueMap) (map[string]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, v := range vms {
		if !seen[v.HostID] {
			seen[v.HostID] = true
			ids = append(ids, v.HostID)
		}
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	sort.Strings(ids)
	for _, m := range []struct{ method, idParam string }{{"template.get", "templateids"}, {"host.get", "hostids"}} {
		raw, err := c.Call(m.method, map[string]any{m.idParam: ids, "output": []string{"host"}})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.method, err)
		}
		var rs []struct {
			HostID     string `json:"hostid"`
			TemplateID string `json:"templateid"`
			Host       string `json:"host"`
		}
		if err := json.Unmarshal(raw, &rs); err != nil {
			return nil, fmt.Errorf("parsing owners: %w", err)
		}
		for _, r := range rs {
			id := r.HostID
			if id == "" {
				id = r.TemplateID
			}
			out[id] = r.Host
		}
	}
	return out, nil
}

// CreateValueMap creates a value map on a host or template.
func CreateValueMap(c *api.Client, ownerID, name string, mappings []Mapping) (string, error) {
	raw, err := c.Call("valuemap.create", map[string]any{"hostid": ownerID, "name": name, "mappings": mappings})
	if err != nil {
		return "", fmt.Errorf("valuemap.create: %w", err)
	}
	var res struct {
		IDs []string `json:"valuemapids"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.IDs) == 0 {
		return "", fmt.Errorf("parsing valuemap.create result: %v", err)
	}
	return res.IDs[0], nil
}

// UpdateValueMap renames a value map and/or replaces all of its mappings.
func UpdateValueMap(c *api.Client, id string, name *string, mappings []Mapping) error {
	p := map[string]any{"valuemapid": id}
	if name != nil {
		p["name"] = *name
	}
	if mappings != nil {
		p["mappings"] = mappings
	}
	if _, err := c.Call("valuemap.update", p); err != nil {
		return fmt.Errorf("valuemap.update: %w", err)
	}
	return nil
}

// DeleteValueMap deletes a value map by ID.
func DeleteValueMap(c *api.Client, id string) error {
	if _, err := c.Call("valuemap.delete", []string{id}); err != nil {
		return fmt.Errorf("valuemap.delete: %w", err)
	}
	return nil
}

// ParseMapping reads "VALUE=TEXT" as a mapping of the given type name.
func ParseMapping(kind, spec string) (Mapping, error) {
	code, ok := MappingTypes[kind]
	if !ok {
		return Mapping{}, fmt.Errorf("unknown mapping type %q", kind)
	}
	if kind == "default" {
		return Mapping{Type: code, NewValue: spec}, nil
	}
	i := strings.LastIndex(spec, "=")
	if i <= 0 || i == len(spec)-1 {
		return Mapping{}, fmt.Errorf("invalid mapping %q: use VALUE=TEXT", spec)
	}
	return Mapping{Type: code, Value: spec[:i], NewValue: spec[i+1:]}, nil
}

// Regexp is a global regular expression.
type Regexp struct {
	ID          string       `json:"regexpid"`
	Name        string       `json:"name"`
	TestString  string       `json:"test_string"`
	Expressions []Expression `json:"expressions"`
}

// Expression is one expression of a global regular expression.
type Expression struct {
	Expression    string `json:"expression"`
	Type          string `json:"expression_type"` // 0 contains, 1 any of, 2 does not contain, 3 matches, 4 does not match
	Delimiter     string `json:"exp_delimiter,omitempty"`
	CaseSensitive string `json:"case_sensitive"`
}

// ExpressionTypes name the Zabbix expression type codes.
var ExpressionTypes = map[string]string{"contains": "0", "any": "1", "not-contains": "2", "matches": "3", "not-matches": "4"}

// Label renders an expression like "matches:^ext[234]$".
func (e Expression) Label() string {
	for name, code := range ExpressionTypes {
		if code == e.Type {
			return name + ":" + e.Expression
		}
	}
	return e.Type + ":" + e.Expression
}

// ExpressionLabels lists the expressions as strings.
func (r Regexp) ExpressionLabels() []string {
	out := make([]string, len(r.Expressions))
	for i, e := range r.Expressions {
		out[i] = e.Label()
	}
	return out
}

// GetRegexps lists global regular expressions.
func GetRegexps(c *api.Client) ([]Regexp, error) {
	raw, err := c.CallList("regexp.get", map[string]any{"output": "extend", "selectExpressions": "extend", "sortfield": "name"})
	if err != nil {
		return nil, fmt.Errorf("regexp.get: %w", err)
	}
	var rs []Regexp
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing regular expressions: %w", err)
	}
	return rs, nil
}

// FindRegexp finds one by ID or exact name.
func FindRegexp(c *api.Client, ref string) (*Regexp, error) {
	rs, err := GetRegexps(c)
	if err != nil {
		return nil, err
	}
	for i := range rs {
		if rs[i].ID == ref || rs[i].Name == ref {
			return &rs[i], nil
		}
	}
	return nil, &NotFoundError{Resource: "regular expression", Query: ref}
}

// ParseExpression reads "TYPE:EXPRESSION", e.g. "matches:^(ext4|xfs)$".
func ParseExpression(spec string, caseSensitive bool) (Expression, error) {
	i := strings.Index(spec, ":")
	if i <= 0 {
		return Expression{}, fmt.Errorf("invalid expression %q: use TYPE:EXPRESSION with TYPE one of contains, any, not-contains, matches, not-matches", spec)
	}
	code, ok := ExpressionTypes[spec[:i]]
	if !ok {
		return Expression{}, fmt.Errorf("unknown expression type %q: use contains, any, not-contains, matches or not-matches", spec[:i])
	}
	e := Expression{Expression: spec[i+1:], Type: code, CaseSensitive: "0"}
	if caseSensitive {
		e.CaseSensitive = "1"
	}
	if code == "1" {
		e.Delimiter = ","
	}
	return e, nil
}

// CreateRegexp creates a global regular expression.
func CreateRegexp(c *api.Client, name, testString string, exprs []Expression) (string, error) {
	p := map[string]any{"name": name, "expressions": exprs}
	if testString != "" {
		p["test_string"] = testString
	}
	raw, err := c.Call("regexp.create", p)
	if err != nil {
		return "", fmt.Errorf("regexp.create: %w", err)
	}
	var res struct {
		IDs []string `json:"regexpids"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.IDs) == 0 {
		return "", fmt.Errorf("parsing regexp.create result: %v", err)
	}
	return res.IDs[0], nil
}

// UpdateRegexp renames and/or replaces the expressions of a global regular expression.
func UpdateRegexp(c *api.Client, id string, name *string, exprs []Expression) error {
	p := map[string]any{"regexpid": id}
	if name != nil {
		p["name"] = *name
	}
	if exprs != nil {
		p["expressions"] = exprs
	}
	if _, err := c.Call("regexp.update", p); err != nil {
		return fmt.Errorf("regexp.update: %w", err)
	}
	return nil
}

// DeleteRegexp deletes a global regular expression by ID.
func DeleteRegexp(c *api.Client, id string) error {
	if _, err := c.Call("regexp.delete", []string{id}); err != nil {
		return fmt.Errorf("regexp.delete: %w", err)
	}
	return nil
}
