package resources

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// DiscoveryRule is a low-level discovery (LLD) rule with its object counts.
type DiscoveryRule struct {
	RuleID            string
	HostID            string
	Host              string
	Name              string
	Key               string
	Enabled           bool
	Supported         bool
	Error             string
	Interval          string
	ItemPrototypes    int
	TriggerPrototypes int
	LostItems         int
	LostTriggers      int
}

// LostObject is a discovered item or trigger that the rule no longer finds.
// Zabbix deletes it itself at DeletesAt; until then it keeps existing (and keeps
// any problem it had open).
type LostObject struct {
	Kind      string // "item" or "trigger"
	ID        string
	Host      string
	HostID    string
	Name      string // item name or trigger description
	Key       string // item key; empty for triggers
	RuleID    string // the discovery rule it came from, when known
	DeletesAt string // Unix: when Zabbix removes it by itself
}

// GetDiscoveryRules lists LLD rules for hosts (all hosts when hostIDs is empty)
// together with prototype counts and how many discovered objects are lost.
func GetDiscoveryRules(c *api.Client, hostIDs []string) ([]DiscoveryRule, error) {
	params := map[string]any{
		"output":         []string{"itemid", "hostid", "name", "key_", "status", "state", "error", "delay"},
		"selectHosts":    []string{"host"},
		"selectItems":    "count",
		"selectTriggers": "count",
		"sortfield":      "name",
	}
	if len(hostIDs) > 0 {
		params["hostids"] = hostIDs
	}
	raw, err := c.Call("discoveryrule.get", params)
	if err != nil {
		return nil, fmt.Errorf("discoveryrule.get: %w", err)
	}
	var rs []struct {
		ItemID   string `json:"itemid"`
		HostID   string `json:"hostid"`
		Name     string `json:"name"`
		Key      string `json:"key_"`
		Status   string `json:"status"`
		State    string `json:"state"`
		Error    string `json:"error"`
		Delay    string `json:"delay"`
		Items    string `json:"items"`
		Triggers string `json:"triggers"`
		Hosts    []struct {
			Host string `json:"host"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("parsing discovery rules: %w", err)
	}
	lostItems, err := GetLost(c, "item", hostIDs, nil)
	if err != nil {
		return nil, err
	}
	lostTriggers, err := GetLost(c, "trigger", hostIDs, nil)
	if err != nil {
		return nil, err
	}
	perRule := func(objs []LostObject) map[string]int {
		m := map[string]int{}
		for _, o := range objs {
			m[o.RuleID]++
		}
		return m
	}
	li, lt := perRule(lostItems), perRule(lostTriggers)
	out := make([]DiscoveryRule, 0, len(rs))
	for _, r := range rs {
		host := ""
		if len(r.Hosts) > 0 {
			host = r.Hosts[0].Host
		}
		itemProtos, _ := strconv.Atoi(r.Items)
		trigProtos, _ := strconv.Atoi(r.Triggers)
		out = append(out, DiscoveryRule{
			RuleID: r.ItemID, HostID: r.HostID, Host: host, Name: r.Name, Key: r.Key, Enabled: r.Status == "0",
			Supported: r.State != "1", Error: r.Error, Interval: r.Delay,
			ItemPrototypes: itemProtos, TriggerPrototypes: trigProtos, LostItems: li[r.ItemID], LostTriggers: lt[r.ItemID],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// GetLost lists lost discovered objects. kind is "item" or "trigger". Only
// discovered objects (flags 4) whose discovery status is "lost" are returned.
func GetLost(c *api.Client, kind string, hostIDs, groupIDs []string) ([]LostObject, error) {
	method, sel, idField, nameField := "item.get", "selectItemDiscovery", "itemid", "name"
	out := []string{"itemid", "name", "key_", "hostid"}
	discField, parentField := "itemDiscovery", "parent_itemid"
	if kind == "trigger" {
		method, sel, idField, nameField = "trigger.get", "selectTriggerDiscovery", "triggerid", "description"
		out = []string{"triggerid", "description"}
		discField, parentField = "triggerDiscovery", "parent_triggerid"
	}
	params := map[string]any{
		"output":      out,
		"filter":      map[string]any{"flags": []int{4}},
		sel:           "extend",
		"selectHosts": []string{"hostid", "host"},
	}
	if len(hostIDs) > 0 {
		params["hostids"] = hostIDs
	}
	if len(groupIDs) > 0 {
		params["groupids"] = groupIDs
	}
	raw, err := c.Call(method, params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	var objs []map[string]any
	if err := json.Unmarshal(raw, &objs); err != nil {
		return nil, fmt.Errorf("parsing discovered %ss: %w", kind, err)
	}
	// Map prototypes to their rule so lost objects can be counted per rule.
	protoRule, err := prototypeRules(c, kind)
	if err != nil {
		return nil, err
	}
	var lost []LostObject
	for _, o := range objs {
		d, _ := o[discField].(map[string]any)
		if d == nil || fmt.Sprint(d["status"]) != "1" {
			continue
		}
		lo := LostObject{Kind: kind, ID: fmt.Sprint(o[idField]), Name: fmt.Sprint(o[nameField]),
			DeletesAt: fmt.Sprint(d["ts_delete"]), RuleID: protoRule[fmt.Sprint(d[parentField])]}
		if k, ok := o["key_"].(string); ok {
			lo.Key = k
		}
		if hs, ok := o["hosts"].([]any); ok && len(hs) > 0 {
			if h, ok := hs[0].(map[string]any); ok {
				lo.Host, _ = h["host"].(string)
				lo.HostID, _ = h["hostid"].(string)
			}
		}
		lost = append(lost, lo)
	}
	return lost, nil
}

// prototypeRules maps item/trigger prototype IDs to the rule that owns them.
func prototypeRules(c *api.Client, kind string) (map[string]string, error) {
	method, idField := "itemprototype.get", "itemid"
	if kind == "trigger" {
		method, idField = "triggerprototype.get", "triggerid"
	}
	raw, err := c.Call(method, map[string]any{"output": []string{idField}, "selectDiscoveryRule": []string{"itemid"}})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	var ps []map[string]any
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("parsing prototypes: %w", err)
	}
	m := map[string]string{}
	for _, p := range ps {
		if r, ok := p["discoveryRule"].(map[string]any); ok {
			m[fmt.Sprint(p[idField])] = fmt.Sprint(r["itemid"])
		}
	}
	return m, nil
}

// DeleteLost deletes one discovered item or trigger by ID. Callers check that
// it is lost first (GetLost); Zabbix itself also refuses to delete discovered
// objects that are still being discovered.
func DeleteLost(c *api.Client, kind, id string) error {
	method := "item.delete"
	if kind == "trigger" {
		method = "trigger.delete"
	}
	if _, err := c.Call(method, []string{id}); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

// OpenProblemEventIDs returns the open problem events of a trigger.
func OpenProblemEventIDs(c *api.Client, triggerID string) ([]string, error) {
	raw, err := c.Call("problem.get", map[string]any{"objectids": []string{triggerID}, "output": []string{"eventid"}})
	if err != nil {
		return nil, fmt.Errorf("problem.get: %w", err)
	}
	var ps []struct {
		EventID string `json:"eventid"`
	}
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("parsing problems: %w", err)
	}
	ids := make([]string, len(ps))
	for i, p := range ps {
		ids[i] = p.EventID
	}
	return ids, nil
}
