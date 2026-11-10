package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// Interface represents a network interface configured in Zabbix for a host.
type Interface struct {
	InterfaceID      string
	HostID           string
	Host             string
	TypeRaw          string
	Type             string // agent | snmp | ipmi | jmx
	Main             string // "1" = default interface for its type
	UseIP            string // "1" = connect via IP, "0" = connect via DNS
	IP               string
	DNS              string
	Port             string
	AvailableRaw     string
	Available        string // unknown | available | unavailable
	Error            string
	SNMPVersion      string
	SNMPCommunity    string
	SNMPSecurityName string
}

// GetInterfaces returns all interfaces for the given host.
func GetInterfaces(c *api.Client, hostID string) ([]Interface, error) {
	params := map[string]any{
		"output":           []string{"hostid", "host", "name"},
		"selectInterfaces": "extend",
		"hostids":          []string{hostID},
	}
	raw, err := c.Call("host.get", params)
	if err != nil {
		return nil, fmt.Errorf("host.get (interfaces): %w", err)
	}

	var hosts []struct {
		HostID     string `json:"hostid"`
		Host       string `json:"host"`
		Interfaces []struct {
			InterfaceID string          `json:"interfaceid"`
			HostID      string          `json:"hostid"`
			Type        string          `json:"type"`
			Main        string          `json:"main"`
			UseIP       string          `json:"useip"`
			IP          string          `json:"ip"`
			DNS         string          `json:"dns"`
			Port        string          `json:"port"`
			Available   string          `json:"available"`
			Error       string          `json:"error"`
			Details     json.RawMessage `json:"details"`
		} `json:"interfaces"`
	}
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, fmt.Errorf("parsing host interfaces: %w", err)
	}
	if len(hosts) == 0 {
		return nil, &NotFoundError{Resource: "host", Query: hostID}
	}

	h := hosts[0]
	result := make([]Interface, 0, len(h.Interfaces))
	for _, ri := range h.Interfaces {
		iface := Interface{
			InterfaceID:  ri.InterfaceID,
			HostID:       ri.HostID,
			Host:         h.Host,
			TypeRaw:      ri.Type,
			Type:         interfaceTypeLabel(ri.Type),
			Main:         ri.Main,
			UseIP:        ri.UseIP,
			IP:           ri.IP,
			DNS:          ri.DNS,
			Port:         ri.Port,
			AvailableRaw: ri.Available,
			Available:    availabilityLabel(ri.Available),
			Error:        ri.Error,
		}
		// Parse SNMP details — Zabbix returns [] for non-SNMP, {...} for SNMP.
		if ri.Type == "2" && len(ri.Details) > 0 && ri.Details[0] == '{' {
			var d struct {
				Version      string `json:"version"`
				Community    string `json:"community"`
				SecurityName string `json:"securityname"`
			}
			if err := json.Unmarshal(ri.Details, &d); err == nil {
				iface.SNMPVersion = d.Version
				iface.SNMPCommunity = d.Community
				iface.SNMPSecurityName = d.SecurityName
			}
		}
		result = append(result, iface)
	}
	return result, nil
}

func interfaceTypeLabel(s string) string {
	switch s {
	case "1":
		return "agent"
	case "2":
		return "snmp"
	case "3":
		return "ipmi"
	case "4":
		return "jmx"
	default:
		return s
	}
}

func availabilityLabel(s string) string {
	switch s {
	case "0":
		return "unknown"
	case "1":
		return "available"
	case "2":
		return "unavailable"
	default:
		return s
	}
}
