package resources

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Proxy is a Zabbix proxy (7.0+ fields).
type Proxy struct {
	ID            string
	Name          string
	Mode          string // "0" active, "1" passive
	Address       string
	LastAccess    string // Unix; "0" never
	Version       string
	Compatibility string // "0" undefined, "1" current, "2" outdated, "3" unsupported
	State         string // "0" unknown, "1" offline, "2" online
	Hosts         int
}

// VersionLabel turns Zabbix's numeric version (70416) into 7.4.16.
func VersionLabel(v string) string {
	n, err := strconv.Atoi(v)
	if err != nil || n < 10000 {
		return v
	}
	return fmt.Sprintf("%d.%d.%d", n/10000, (n/100)%100, n%100)
}

// GetProxies lists proxies with how many hosts they monitor.
func GetProxies(c *api.Client) ([]Proxy, error) {
	raw, err := c.CallList("proxy.get", map[string]any{
		"output":      []string{"proxyid", "name", "operating_mode", "address", "lastaccess", "version", "compatibility", "state"},
		"selectHosts": "count",
		"sortfield":   "name",
	})
	if err != nil {
		return nil, fmt.Errorf("proxy.get: %w", err)
	}
	var rs []struct {
		ID            string `json:"proxyid"`
		Name          string `json:"name"`
		Mode          string `json:"operating_mode"`
		Address       string `json:"address"`
		LastAccess    string `json:"lastaccess"`
		Version       string `json:"version"`
		Compatibility string `json:"compatibility"`
		State         string `json:"state"`
		Hosts         string `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing proxies: %w", err)
	}
	out := make([]Proxy, len(rs))
	for i, r := range rs {
		n, _ := strconv.Atoi(r.Hosts)
		out[i] = Proxy{ID: r.ID, Name: r.Name, Mode: r.Mode, Address: r.Address, LastAccess: r.LastAccess, Version: r.Version, Compatibility: r.Compatibility, State: r.State, Hosts: n}
	}
	return out, nil
}

// ProxyGroup is a proxy group (7.0+).
type ProxyGroup struct {
	ID            string
	Name          string
	State         string // "0" unknown, "1" offline, "2" recovering, "3" online, "4" degrading
	FailoverDelay string
	MinOnline     string
	Proxies       int
}

// GetProxyGroups lists proxy groups.
func GetProxyGroups(c *api.Client) ([]ProxyGroup, error) {
	raw, err := c.CallList("proxygroup.get", map[string]any{
		"output":        []string{"proxy_groupid", "name", "state", "failover_delay", "min_online"},
		"selectProxies": "count",
		"sortfield":     "name",
	})
	if err != nil {
		return nil, fmt.Errorf("proxygroup.get: %w", err)
	}
	var rs []struct {
		ID            string `json:"proxy_groupid"`
		Name          string `json:"name"`
		State         string `json:"state"`
		FailoverDelay string `json:"failover_delay"`
		MinOnline     string `json:"min_online"`
		Proxies       string `json:"proxies"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing proxy groups: %w", err)
	}
	out := make([]ProxyGroup, len(rs))
	for i, r := range rs {
		n, _ := strconv.Atoi(r.Proxies)
		out[i] = ProxyGroup{ID: r.ID, Name: r.Name, State: r.State, FailoverDelay: r.FailoverDelay, MinOnline: r.MinOnline, Proxies: n}
	}
	return out, nil
}

// ResolveProxyID finds a proxy by ID or exact name.
func ResolveProxyID(c *api.Client, ref string) (string, error) {
	ps, err := GetProxies(c)
	if err != nil {
		return "", err
	}
	for _, p := range ps {
		if p.ID == ref || p.Name == ref {
			return p.ID, nil
		}
	}
	return "", &NotFoundError{Resource: "proxy", Query: ref}
}

// ResolveProxyGroupID finds a proxy group by ID or exact name.
func ResolveProxyGroupID(c *api.Client, ref string) (string, error) {
	gs, err := GetProxyGroups(c)
	if err != nil {
		return "", err
	}
	for _, g := range gs {
		if g.ID == ref || g.Name == ref {
			return g.ID, nil
		}
	}
	return "", &NotFoundError{Resource: "proxy group", Query: ref}
}
