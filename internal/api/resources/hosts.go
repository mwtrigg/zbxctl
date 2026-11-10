package resources

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Host represents a Zabbix host object.
type Host struct {
	HostID       string `json:"hostid"`
	Host         string `json:"host"`      // technical name
	Name         string `json:"name"`      // visible name
	Status       string `json:"status"`    // "0"=monitored, "1"=unmonitored
	Available    string `json:"available"` // "0"=unknown, "1"=available, "2"=unavailable
	Description  string `json:"description"`
	MonitoredBy  string `json:"monitored_by"` // "0" server, "1" proxy, "2" proxy group
	ProxyID      string `json:"proxyid"`
	ProxyGroupID string `json:"proxy_groupid"`

	// Populated via select* parameters (see hostSelects).
	HostGroups      []NamedRef  `json:"hostgroups"`
	ParentTemplates []NamedRef  `json:"parentTemplates"`
	Interfaces      []HostIface `json:"interfaces"`
	Tags            []HostTag   `json:"tags"`
	Macros          []HostMacro `json:"macros"`
}

// NamedRef is a linked object reduced to its name.
type NamedRef struct {
	Name string `json:"name"`
}

// HostIface is a host interface as returned by selectInterfaces.
type HostIface struct {
	IP        string `json:"ip"`
	DNS       string `json:"dns"`
	Port      string `json:"port"`
	Main      string `json:"main"`
	Available string `json:"available"` // "0"=unknown, "1"=available, "2"=unavailable
}

// HostTag is a host tag.
type HostTag struct {
	Tag   string `json:"tag"`
	Value string `json:"value"`
}

// HostMacro is a user macro. Secret macros (type "1") carry no value.
type HostMacro struct {
	Macro string `json:"macro"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

// AvailableState summarises interface availability. Zabbix 7.x moved
// availability from the host to its interfaces, so host.get no longer
// returns a usable "available" field.
func (h Host) AvailableState() string {
	state := "0"
	for _, i := range h.Interfaces {
		switch i.Available {
		case "2":
			return "2"
		case "1":
			state = "1"
		}
	}
	return state
}

// GroupNames returns the names of the host's groups.
func (h Host) GroupNames() []string { return names(h.HostGroups) }

// TemplateNames returns the names of directly linked templates.
func (h Host) TemplateNames() []string { return names(h.ParentTemplates) }

func names(refs []NamedRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}

// hostSelects adds the related-object selects. detail adds tags and macros.
func hostSelects(params map[string]any, detail bool) {
	params["selectHostGroups"] = []string{"name"}
	params["selectParentTemplates"] = []string{"name"}
	params["selectInterfaces"] = []string{"ip", "dns", "port", "main", "available"}
	if detail {
		params["selectTags"] = []string{"tag", "value"}
		params["selectMacros"] = []string{"macro", "value", "type"}
	}
}

// HostFilter controls which hosts are returned by GetHosts.
type HostFilter struct {
	GroupIDs  []string
	Status    string // "0"=monitored, "1"=unmonitored, ""=all
	Search    string // substring search on host/name fields
	Limit     int
	SortField string // "host" | "name" | "hostid"
	SortOrder string // "ASC" | "DESC"
}

// GetHosts returns a filtered list of hosts.
func GetHosts(c *api.Client, f HostFilter) ([]Host, error) {
	params := map[string]any{
		"output": []string{"hostid", "host", "name", "status", "description"},
	}
	hostSelects(params, false)
	if len(f.GroupIDs) > 0 {
		params["groupids"] = f.GroupIDs
	}
	if f.Status != "" {
		params["filter"] = map[string]any{"status": []string{f.Status}}
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
	sf := f.SortField
	if sf == "" {
		sf = "host"
	}
	params["sortfield"] = sf
	so := f.SortOrder
	if so == "" {
		so = "ASC"
	}
	params["sortorder"] = so

	raw, err := c.CallList("host.get", params)
	if err != nil {
		return nil, fmt.Errorf("host.get: %w", err)
	}
	var hosts []Host
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, fmt.Errorf("parsing hosts: %w", err)
	}
	return hosts, nil
}

// GetHost returns a single host by numeric ID, technical name, or visible name.
// Lookup order: numeric ID → technical name (host field) → visible name (name field).
func GetHost(c *api.Client, idOrName string) (*Host, error) {
	outputFields := []string{"hostid", "host", "name", "status", "description", "monitored_by", "proxyid", "proxy_groupid"}

	if isNumeric(idOrName) {
		return hostGetOne(c, outputFields, map[string]any{
			"hostids": []string{idOrName},
		}, idOrName)
	}

	// Try technical name.
	h, err := hostGetOne(c, outputFields, map[string]any{
		"filter": map[string]any{"host": []string{idOrName}},
	}, "")
	if err == nil {
		return h, nil
	}
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		return nil, err
	}

	// Fall back to visible name.
	return hostGetOne(c, outputFields, map[string]any{
		"filter": map[string]any{"name": []string{idOrName}},
	}, idOrName)
}

func hostGetOne(c *api.Client, outputFields []string, extraParams map[string]any, query string) (*Host, error) {
	params := map[string]any{"output": outputFields}
	hostSelects(params, true)
	for k, v := range extraParams {
		params[k] = v
	}
	raw, err := c.Call("host.get", params)
	if err != nil {
		return nil, fmt.Errorf("host.get: %w", err)
	}
	var hosts []Host
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, fmt.Errorf("parsing host: %w", err)
	}
	if len(hosts) == 0 {
		if query == "" {
			return nil, &NotFoundError{Resource: "host", Query: "unknown"}
		}
		return nil, &NotFoundError{Resource: "host", Query: query}
	}
	return &hosts[0], nil
}

// ResolveHostID returns the hostid for a given numeric ID or technical name.
func ResolveHostID(c *api.Client, idOrName string) (string, error) {
	if isNumeric(idOrName) {
		return idOrName, nil
	}
	h, err := GetHost(c, idOrName)
	if err != nil {
		return "", err
	}
	return h.HostID, nil
}

// isNumeric returns true if s consists entirely of decimal digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// ResolveGroupID returns the groupid for a given name or numeric ID.
func ResolveGroupID(c *api.Client, nameOrID string) (string, error) {
	if isNumeric(nameOrID) {
		return nameOrID, nil
	}
	g, err := GetGroupByName(c, nameOrID)
	if err != nil {
		return "", err
	}
	return g.GroupID, nil
}

// ResolveTemplateID returns the templateid for a given name or numeric ID.
func ResolveTemplateID(c *api.Client, nameOrID string) (string, error) {
	if isNumeric(nameOrID) {
		return nameOrID, nil
	}
	params := map[string]any{
		"output": []string{"templateid", "host"},
		"filter": map[string]any{"host": []string{nameOrID}},
	}
	raw, err := c.Call("template.get", params)
	if err != nil {
		return "", fmt.Errorf("template.get: %w", err)
	}
	var templates []struct {
		TemplateID string `json:"templateid"`
	}
	if err := json.Unmarshal(raw, &templates); err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	if len(templates) == 0 {
		return "", &NotFoundError{Resource: "template", Query: nameOrID}
	}
	return templates[0].TemplateID, nil
}

// ResolveHostNames returns a map of hostid → hostname for the given IDs.
func ResolveHostNames(c *api.Client, hostIDs []string) (map[string]string, error) {
	if len(hostIDs) == 0 {
		return map[string]string{}, nil
	}
	params := map[string]any{
		"output":  []string{"hostid", "host"},
		"hostids": hostIDs,
	}
	raw, err := c.Call("host.get", params)
	if err != nil {
		return nil, fmt.Errorf("host.get: %w", err)
	}
	var hosts []struct {
		HostID string `json:"hostid"`
		Host   string `json:"host"`
	}
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, fmt.Errorf("parsing hosts: %w", err)
	}
	m := make(map[string]string, len(hosts))
	for _, h := range hosts {
		m[h.HostID] = h.Host
	}
	return m, nil
}

// InterfaceInput describes a host interface for create/update operations.
type InterfaceInput struct {
	Type          int // 1=agent, 2=snmp, 3=ipmi, 4=jmx
	UseIP         int // 1=IP, 0=DNS
	IP            string
	DNS           string
	Port          string
	SNMPVersion   string // "1"|"2"|"3"; SNMP only
	SNMPCommunity string // v1/v2c only
}

// CreateHostInput holds all fields for host.create.
type CreateHostInput struct {
	Host        string // technical name (required)
	VisibleName string // defaults to Host if empty
	Description string
	GroupIDs    []string // resolved groupids (required, at least one)
	Interface   InterfaceInput
}

// CreateHost creates a new Zabbix host and returns the created host record.
func CreateHost(c *api.Client, in CreateHostInput) (*Host, error) {
	if in.Host == "" {
		return nil, fmt.Errorf("Host is required")
	}
	if len(in.GroupIDs) == 0 {
		return nil, fmt.Errorf("at least one GroupID is required")
	}
	groups := make([]map[string]string, len(in.GroupIDs))
	for i, id := range in.GroupIDs {
		groups[i] = map[string]string{"groupid": id}
	}

	iface := map[string]any{
		"type":  in.Interface.Type,
		"main":  1,
		"useip": in.Interface.UseIP,
		"ip":    in.Interface.IP,
		"dns":   in.Interface.DNS,
		"port":  in.Interface.Port,
	}
	if in.Interface.Type == 2 { // SNMP
		details := map[string]any{
			"version":   in.Interface.SNMPVersion,
			"community": in.Interface.SNMPCommunity,
		}
		iface["details"] = details
	}

	visibleName := in.VisibleName
	if visibleName == "" {
		visibleName = in.Host
	}

	params := map[string]any{
		"host":       in.Host,
		"name":       visibleName,
		"groups":     groups,
		"interfaces": []map[string]any{iface},
	}
	if in.Description != "" {
		params["description"] = in.Description
	}

	raw, err := c.Call("host.create", params)
	if err != nil {
		return nil, fmt.Errorf("host.create: %w", err)
	}
	var result struct {
		HostIDs []string `json:"hostids"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing host.create response: %w", err)
	}
	if len(result.HostIDs) == 0 {
		return nil, fmt.Errorf("host.create returned no hostids")
	}
	return GetHost(c, result.HostIDs[0])
}

// UpdateHostInput holds fields for host.update. Only non-empty/non-nil fields
// are applied. At least one field must be set.
type UpdateHostInput struct {
	HostID            string
	VisibleName       string
	Description       string
	AddGroupIDs       []string // resolved groupids to add
	RemoveGroupIDs    []string // resolved groupids to remove
	AddTemplateIDs    []string // resolved templateids to link
	RemoveTemplateIDs []string // resolved templateids to unlink

	// Monitoring source: "" leave alone, "server", "proxy" (ProxyID) or "group" (ProxyGroupID).
	MonitoredBy  string
	ProxyID      string
	ProxyGroupID string
}

// UpdateHost fetches current groups/templates, applies diffs, and calls host.update.
func UpdateHost(c *api.Client, in UpdateHostInput) (*Host, error) {
	// Fetch current groups and templates.
	params := map[string]any{
		"output":                []string{"hostid", "host", "name", "status"},
		"hostids":               []string{in.HostID},
		"selectHostGroups":      []string{"groupid"},
		"selectParentTemplates": []string{"templateid"},
	}
	raw, err := c.Call("host.get", params)
	if err != nil {
		return nil, fmt.Errorf("host.get: %w", err)
	}
	var hosts []struct {
		HostID    string              `json:"hostid"`
		Host      string              `json:"host"`
		Name      string              `json:"name"`
		Status    string              `json:"status"`
		Groups    []map[string]string `json:"hostgroups"`
		Templates []map[string]string `json:"parentTemplates"`
	}
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, fmt.Errorf("parsing host: %w", err)
	}
	if len(hosts) == 0 {
		return nil, &NotFoundError{Resource: "host", Query: in.HostID}
	}
	h := hosts[0]

	// Build updated groups set.
	groupSet := make(map[string]struct{})
	for _, g := range h.Groups {
		groupSet[g["groupid"]] = struct{}{}
	}
	for _, id := range in.AddGroupIDs {
		groupSet[id] = struct{}{}
	}
	for _, id := range in.RemoveGroupIDs {
		delete(groupSet, id)
	}

	// Build updated templates set.
	tmplSet := make(map[string]struct{})
	for _, t := range h.Templates {
		tmplSet[t["templateid"]] = struct{}{}
	}
	for _, id := range in.AddTemplateIDs {
		tmplSet[id] = struct{}{}
	}
	for _, id := range in.RemoveTemplateIDs {
		delete(tmplSet, id)
	}

	// Build update payload.
	updateParams := map[string]any{"hostid": in.HostID}
	if in.VisibleName != "" {
		updateParams["name"] = in.VisibleName
	}
	if in.Description != "" {
		updateParams["description"] = in.Description
	}
	if len(in.AddGroupIDs) > 0 || len(in.RemoveGroupIDs) > 0 {
		if len(groupSet) == 0 {
			return nil, fmt.Errorf("cannot remove all host groups: a host must belong to at least one group")
		}
		groups := make([]map[string]string, 0, len(groupSet))
		for id := range groupSet {
			groups = append(groups, map[string]string{"groupid": id})
		}
		updateParams["groups"] = groups
	}
	if len(in.AddTemplateIDs) > 0 || len(in.RemoveTemplateIDs) > 0 {
		tmpls := make([]map[string]string, 0, len(tmplSet))
		for id := range tmplSet {
			tmpls = append(tmpls, map[string]string{"templateid": id})
		}
		updateParams["templates"] = tmpls
		if len(in.RemoveTemplateIDs) > 0 {
			clear := make([]map[string]string, len(in.RemoveTemplateIDs))
			for i, id := range in.RemoveTemplateIDs {
				clear[i] = map[string]string{"templateid": id}
			}
			updateParams["templates_clear"] = clear
		}
	}

	switch in.MonitoredBy {
	case "server":
		updateParams["monitored_by"] = 0
	case "proxy":
		updateParams["monitored_by"], updateParams["proxyid"] = 1, in.ProxyID
	case "group":
		updateParams["monitored_by"], updateParams["proxy_groupid"] = 2, in.ProxyGroupID
	}

	_, err = c.Call("host.update", updateParams)
	if err != nil {
		return nil, fmt.Errorf("host.update: %w", err)
	}
	return GetHost(c, in.HostID)
}

// DeleteHost fetches the host record (for output), then deletes it.
func DeleteHost(c *api.Client, hostID string) (*Host, error) {
	h, err := GetHost(c, hostID)
	if err != nil {
		return nil, err
	}
	if _, err = c.Call("host.delete", []string{hostID}); err != nil {
		return nil, fmt.Errorf("host.delete: %w", err)
	}
	return h, nil
}
