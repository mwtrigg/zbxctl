package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// TemplateLinks lists what is linked to a template.
type TemplateLinks struct {
	Hosts     []string // hosts that use it
	Templates []string // templates that link it
}

// GetTemplateLinks returns the hosts and templates that use a template.
func GetTemplateLinks(c *api.Client, templateID string) (*TemplateLinks, error) {
	raw, err := c.Call("template.get", map[string]any{
		"templateids":     []string{templateID},
		"output":          []string{"templateid"},
		"selectHosts":     []string{"host"},
		"selectTemplates": []string{"host"},
	})
	if err != nil {
		return nil, fmt.Errorf("template.get: %w", err)
	}
	var rs []struct {
		Hosts []struct {
			Host string `json:"host"`
		} `json:"hosts"`
		Templates []struct {
			Host string `json:"host"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing template links: %w", err)
	}
	l := &TemplateLinks{}
	if len(rs) > 0 {
		for _, h := range rs[0].Hosts {
			l.Hosts = append(l.Hosts, h.Host)
		}
		for _, t := range rs[0].Templates {
			l.Templates = append(l.Templates, t.Host)
		}
	}
	return l, nil
}

// DeleteTemplate deletes a template by ID.
func DeleteTemplate(c *api.Client, templateID string) error {
	if _, err := c.Call("template.delete", []string{templateID}); err != nil {
		return fmt.Errorf("template.delete: %w", err)
	}
	return nil
}
