package resources

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// ExportConfig exports one template or host with configuration.export.
// kind is "templates" or "hosts"; format is yaml, json or xml.
func ExportConfig(c *api.Client, kind, id, format string) (string, error) {
	raw, err := c.Call("configuration.export", map[string]any{
		"format":  format,
		"options": map[string]any{kind: []string{id}},
	})
	if err != nil {
		return "", fmt.Errorf("configuration.export: %w", err)
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("parsing export: %w", err)
	}
	return out, nil
}

// importRules returns the import rules for an export of the given kind. Missing
// objects are created, existing ones updated, and nothing is ever deleted.
func importRules(kind string) map[string]any {
	both := map[string]any{"createMissing": true, "updateExisting": true, "deleteMissing": false}
	createOnly := map[string]any{"createMissing": true, "updateExisting": false}
	rules := map[string]any{
		"host_groups":     createOnly,
		"template_groups": createOnly,
		"templateLinkage": map[string]any{"createMissing": true, "deleteMissing": false},
		"items":           both,
		"discoveryRules":  both,
		"triggers":        both,
		"graphs":          both,
		"httptests":       both,
		"valueMaps":       both,
	}
	if kind == "hosts" {
		rules["hosts"] = map[string]any{"createMissing": true, "updateExisting": true}
	} else {
		rules["templates"] = map[string]any{"createMissing": true, "updateExisting": true}
		rules["templateDashboards"] = both
	}
	return rules
}

// CompareConfig asks Zabbix what importing source would change, without importing.
func CompareConfig(c *api.Client, kind, format, source string) (json.RawMessage, error) {
	raw, err := c.Call("configuration.importcompare", map[string]any{"format": format, "source": source, "rules": importRules(kind)})
	if err != nil {
		return nil, fmt.Errorf("configuration.importcompare: %w", err)
	}
	return raw, nil
}

// ImportConfig imports source (same rules as CompareConfig).
func ImportConfig(c *api.Client, kind, format, source string) error {
	if _, err := c.Call("configuration.import", map[string]any{"format": format, "source": source, "rules": importRules(kind)}); err != nil {
		return fmt.Errorf("configuration.import: %w", err)
	}
	return nil
}

// ConfigChange is one flattened line of an import comparison.
type ConfigChange struct {
	Path   string   `json:"path"`   // e.g. templates/Demo agent heartbeat/items/Agent ping
	Action string   `json:"action"` // created | updated | deleted
	Fields []string `json:"fields"` // for updated: "delay: 30s -> 60s"
}

// FlattenComparison turns the nested importcompare result into a flat, sorted
// list of changes. Zabbix nests {type: {created|updated|deleted: [object]}}
// recursively; an updated object carries before/after plus its children.
func FlattenComparison(raw json.RawMessage) ([]ConfigChange, error) {
	if t := strings.TrimSpace(string(raw)); t == "[]" || t == "{}" || t == "" {
		return []ConfigChange{}, nil // Zabbix answers [] when nothing would change
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parsing comparison: %w", err)
	}
	var out []ConfigChange
	walkGroups(root, "", &out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

var actions = []string{"created", "updated", "deleted"}

func isGroup(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	for _, a := range actions {
		if _, has := m[a]; has {
			return m, true
		}
	}
	return nil, false
}

func walkGroups(node map[string]any, prefix string, out *[]ConfigChange) {
	types := make([]string, 0, len(node))
	for k := range node {
		types = append(types, k)
	}
	sort.Strings(types)
	for _, typ := range types {
		g, ok := isGroup(node[typ])
		if !ok {
			continue
		}
		for _, action := range actions {
			list, _ := g[action].([]any)
			for _, e := range list {
				obj, _ := e.(map[string]any)
				if obj == nil {
					continue
				}
				before, after := obj, obj
				if b, ok := obj["before"].(map[string]any); ok {
					before = b
				}
				if a, ok := obj["after"].(map[string]any); ok {
					after = a
				}
				ref := after
				if action == "deleted" {
					ref = before
				}
				path := prefix + typ + "/" + objectName(ref)
				ch := ConfigChange{Path: path, Action: action, Fields: []string{}}
				if action == "updated" {
					ch.Fields = diffFields(before, after)
				}
				// Report an updated parent only if its own fields changed;
				// children are reported on their own lines.
				if action != "updated" || len(ch.Fields) > 0 {
					*out = append(*out, ch)
				}
				walkGroups(obj, path+"/", out)
			}
		}
	}
}

func objectName(m map[string]any) string {
	for _, k := range []string{"name", "template", "host", "key", "uuid"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

func diffFields(before, after map[string]any) []string {
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []string
	for _, k := range names {
		b, _ := json.Marshal(before[k])
		a, _ := json.Marshal(after[k])
		if string(b) != string(a) {
			out = append(out, fmt.Sprintf("%s: %s -> %s", k, strings.Trim(string(b), `"`), strings.Trim(string(a), `"`)))
		}
	}
	return out
}
