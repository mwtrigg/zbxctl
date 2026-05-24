# Agent Troubleshooting Surface — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add permission tiers to context config, surface broken items and interface state, add script discovery/execution with strict guardrails, add alert delivery status, add a composite `diagnose host` command, and extend `get history` with event-anchored queries.

**Architecture:** Resource functions in `internal/api/resources/` handle all Zabbix API calls. CLI commands in `internal/cli/get.go`, `internal/cli/write.go`, and a new `internal/cli/diagnose.go` wire flags to resources. Permission enforcement lives in `internal/config/config.go` and is checked at the top of each write command. Script execution uses `crypto/rand` token confirmation and bypasses the `--yes` flag entirely.

**Tech Stack:** Go, github.com/spf13/cobra, internal api.Client (JSON-RPC), internal output.Format, golang.org/x/sync/errgroup

**Requires Plan 1 complete first** — `get history --around-event` (Task 9) calls `GetHistory` defined in Plan 1. The `Dangerous` field addition to `CommandSchema` (Task 10) is independent but should be done last.

---

## File Map

| File | Action | What changes |
|---|---|---|
| `internal/config/config.go` | Modify | Add `Permissions`, `AllowedScripts` to `Context`; add `CheckPermission` method |
| `internal/config/config_test.go` | Create | Unit tests for `CheckPermission` |
| `internal/api/resources/items.go` | Modify | Add `State`, `StateRaw`, `Error` to `Item`; add `State` to `ItemFilter`; extend `GetItems` output |
| `internal/api/resources/scripts.go` | Create | `Script`, `ScriptResult`, `ScriptFilter`, `GetScripts`, `GetScriptByNameOrID`, `RunScript` |
| `internal/api/resources/scripts_test.go` | Create | Unit tests for label helpers |
| `internal/api/resources/alerts.go` | Create | `Alert`, `AlertFilter`, `GetAlerts` |
| `internal/api/resources/interfaces.go` | Create | `Interface`, `InterfaceDetails`, `GetInterfaces` |
| `internal/api/resources/events.go` | Modify | Add `GetEventTriggerInfo` helper |
| `internal/cli/get.go` | Modify | Add `--state` flag to items; add `runGetScripts`, `runGetAlerts`, `runGetInterfaces`; extend `runGetHistory` with `--around-event` |
| `internal/cli/write.go` | Modify | Add permission checks to existing commands; add `runRunScript`, `runCmd` |
| `internal/cli/diagnose.go` | Create | `runDiagnoseHost`, `diagnoseCmd`, `diagnoseHostCmd` |
| `internal/cli/root.go` | Modify | Register `runCmd` and `diagnoseCmd` |
| `internal/cli/llm.go` | Modify | Add `Dangerous` field to `CommandSchema`; add manifest entries for all new commands |

---

## Task 1: Config permission tiers

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `internal/cli/write.go` (add permission checks to existing commands)

- [ ] **Step 1: Write failing tests for CheckPermission**

```go
// internal/config/config_test.go
package config

import "testing"

func TestCheckPermission(t *testing.T) {
	tests := []struct {
		name     string
		tier     string
		required string
		wantErr  bool
	}{
		{"write permits write", "write", "write", false},
		{"write permits operator", "write", "operator", false},
		{"write permits readonly", "write", "readonly", false},
		{"operator permits operator", "operator", "operator", false},
		{"operator permits readonly", "operator", "readonly", false},
		{"operator blocks write", "operator", "write", true},
		{"readonly permits readonly", "readonly", "readonly", false},
		{"readonly blocks operator", "readonly", "operator", true},
		{"readonly blocks write", "readonly", "write", true},
		{"empty defaults to write", "", "write", false},
		{"unknown tier defaults to write", "superadmin", "write", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &Context{Permissions: tt.tier}
			err := ctx.CheckPermission(tt.required)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckPermission(%q) with tier %q: err=%v wantErr=%v",
					tt.required, tt.tier, err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go test ./internal/config/...
```
Expected: `FAIL — CheckPermission undefined`

- [ ] **Step 3: Add Permissions/AllowedScripts to Context and implement CheckPermission**

Add to `internal/config/config.go` — insert the two new fields in the `Context` struct after the existing fields, and add the `CheckPermission` method and `permLevel` helper after the `Context` type block:

```go
// Context holds per-context connection settings.
type Context struct {
	URL          string   `yaml:"url"`
	APIToken     string   `yaml:"api_token"`
	Username     string   `yaml:"username"`
	Password     string   `yaml:"password"`
	TLSVerify    *bool    `yaml:"tls_verify"`    // nil = default true
	CAFile       string   `yaml:"ca_file"`
	Proxy        string   `yaml:"proxy"`
	DefaultLimit int      `yaml:"default_limit"` // default 100
	Permissions    string   `yaml:"permissions"`     // readonly | operator | write; default write
	AllowedScripts []string `yaml:"allowed_scripts"` // allowlist for run script; absent = blocked
}

// PermissionError is returned when the context lacks the required permission tier.
type PermissionError struct {
	Required string
	Have     string
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission denied: command requires %q tier, context has %q", e.Required, e.Have)
}

// CheckPermission returns an error if the context's tier does not permit required.
// An empty Permissions field defaults to "write" (backwards-compatible).
func (ctx *Context) CheckPermission(required string) error {
	tier := ctx.Permissions
	if tier == "" {
		tier = "write"
	}
	if permLevel(tier) < permLevel(required) {
		have := tier
		if ctx.Permissions == "" {
			have = "write (default)"
		}
		return &PermissionError{Required: required, Have: have}
	}
	return nil
}

func permLevel(tier string) int {
	switch tier {
	case "readonly":
		return 0
	case "operator":
		return 1
	case "write":
		return 2
	default:
		return 2 // unknown tiers are permissive (human operator use case)
	}
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go test ./internal/config/...
```
Expected: `ok  codeberg.org/mwtrigg/zbxctl/internal/config`

- [ ] **Step 5: Add permission checks to existing write commands in write.go**

In `runSetHostStatus` (enable/disable host), `runSetTriggerStatus` (enable/disable trigger), `runAcknowledge`, `runMaintenanceCreate`, `runMaintenanceDelete` — add the check immediately after `writeClient()` succeeds. Example for `runSetHostStatus`:

```go
func runSetHostStatus(idOrName string, enable bool) error {
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if err := ctx.CheckPermission("operator"); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	// ... rest unchanged ...
```

Apply the same pattern (checking `"operator"`) to `runSetTriggerStatus`, `runAcknowledge`, `runMaintenanceCreate`, `runMaintenanceDelete`.

- [ ] **Step 6: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output (clean build)

- [ ] **Step 7: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/config/config.go internal/config/config_test.go internal/cli/write.go
git commit -m "feat: add permission tiers to context config"
```

---

## Task 2: `get items --state` resource extension

**Files:**
- Modify: `internal/api/resources/items.go`

- [ ] **Step 1: Extend Item struct and ItemFilter**

In `internal/api/resources/items.go`, add `State`, `StateRaw`, and `Error` to `Item`, and add `State` to `ItemFilter`:

```go
// Item represents a Zabbix item.
type Item struct {
	ItemID    string `json:"itemid"`
	HostID    string `json:"hostid"`
	Host      string `json:"host"` // populated when WithHosts is used
	Name      string `json:"name"`
	Key       string `json:"key_"`
	Type      string `json:"type"`
	ValueType string `json:"value_type"`
	Status    string `json:"status"`    // "0"=enabled, "1"=disabled
	State     string `json:"state"`     // "0"=normal, "1"=not_supported
	Error     string `json:"error"`     // non-empty when state=1
	LastValue string `json:"lastvalue"`
	LastClock string `json:"lastclock"` // Unix timestamp string; "0" = never collected
	Units     string `json:"units"`
	Delay     string `json:"delay"`
}

// ItemFilter controls which items are returned.
type ItemFilter struct {
	HostIDs   []string
	GroupIDs  []string // added by Plan 1
	KeySearch string   // substring search on key_
	Status    string   // "0"=enabled, "1"=disabled, ""=all
	State     string   // "0"=normal, "1"=not_supported, ""=all
	WithHosts bool     // if true, request host name alongside each item
	Limit     int
}
```

- [ ] **Step 2: Extend GetItems to include state/error fields and state filter**

Replace the `GetItems` function body to include `state` and `error` in output and apply the `State` filter:

```go
// GetItems returns items for the given filter.
func GetItems(c *api.Client, f ItemFilter) ([]Item, error) {
	outputFields := []string{
		"itemid", "hostid", "name", "key_", "type", "value_type",
		"status", "state", "error", "lastvalue", "lastclock", "units", "delay",
	}
	params := map[string]any{
		"output":    outputFields,
		"sortfield": "name",
		"sortorder": "ASC",
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if len(f.GroupIDs) > 0 {
		params["groupids"] = f.GroupIDs
	}
	if f.KeySearch != "" {
		params["search"] = map[string]any{"key_": f.KeySearch}
	}
	if f.Status != "" {
		params["filter"] = map[string]any{"status": []string{f.Status}}
	}
	if f.State != "" {
		existing, _ := params["filter"].(map[string]any)
		if existing == nil {
			existing = make(map[string]any)
		}
		existing["state"] = []string{f.State}
		params["filter"] = existing
	}
	if f.WithHosts {
		params["selectHosts"] = []string{"hostid", "host"}
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("item.get", params)
	if err != nil {
		return nil, fmt.Errorf("item.get: %w", err)
	}

	if f.WithHosts {
		// Zabbix returns hosts as a nested array; we need a custom unmarshal.
		type itemWithHosts struct {
			Item
			Hosts []struct {
				HostID string `json:"hostid"`
				Host   string `json:"host"`
			} `json:"hosts"`
		}
		var raw2 []itemWithHosts
		if err := json.Unmarshal(raw, &raw2); err != nil {
			return nil, fmt.Errorf("parsing items (with hosts): %w", err)
		}
		items := make([]Item, len(raw2))
		for i, r := range raw2 {
			items[i] = r.Item
			if len(r.Hosts) > 0 {
				items[i].Host = r.Hosts[0].Host
				items[i].HostID = r.Hosts[0].HostID
			}
		}
		return items, nil
	}

	var items []Item
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parsing items: %w", err)
	}
	return items, nil
}
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 4: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/api/resources/items.go
git commit -m "feat: add state/error fields and State filter to Item/ItemFilter"
```

---

## Task 3: Scripts resource

**Files:**
- Create: `internal/api/resources/scripts.go`
- Create: `internal/api/resources/scripts_test.go`

- [ ] **Step 1: Write failing tests for label helpers**

```go
// internal/api/resources/scripts_test.go
package resources

import "testing"

func TestScriptTypeLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0", "script"},
		{"1", "ipmi"},
		{"2", "ssh"},
		{"3", "telnet"},
		{"5", "webhook"},
		{"99", "99"},
	}
	for _, tt := range tests {
		if got := scriptTypeLabel(tt.in); got != tt.want {
			t.Errorf("scriptTypeLabel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExecuteOnLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0", "server"},
		{"1", "agent"},
		{"2", "server-proxy"},
		{"99", "99"},
	}
	for _, tt := range tests {
		if got := executeOnLabel(tt.in); got != tt.want {
			t.Errorf("executeOnLabel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go test ./internal/api/resources/... -run TestScriptTypeLabel -run TestExecuteOnLabel
```
Expected: `FAIL — scriptTypeLabel undefined`

- [ ] **Step 3: Create scripts.go**

```go
// internal/api/resources/scripts.go
package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// Script represents a Zabbix script.
type Script struct {
	ScriptID    string `json:"scriptid"`
	Name        string `json:"name"`
	Type        string `json:"type"`        // see scriptTypeLabel
	ExecuteOn   string `json:"execute_on"`  // see executeOnLabel; only meaningful for type=script
	Description string `json:"description"`
	Command     string `json:"command"`
}

// ScriptFilter controls which scripts are returned.
type ScriptFilter struct {
	HostIDs []string // if set, only scripts applicable to these hosts
}

// ScriptResult is the output of a script execution.
type ScriptResult struct {
	ScriptID   string `json:"scriptid"`
	ScriptName string `json:"script_name"`
	HostID     string `json:"hostid"`
	Host       string `json:"host"`
	Response   string `json:"response"` // "success" or "failed"
	Value      string `json:"value"`    // stdout on success, error message on failure
}

// GetScripts returns scripts visible to the authenticated user.
func GetScripts(c *api.Client, f ScriptFilter) ([]Script, error) {
	params := map[string]any{
		"output": []string{"scriptid", "name", "type", "execute_on", "description", "command"},
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}

	raw, err := c.Call("script.get", params)
	if err != nil {
		return nil, fmt.Errorf("script.get: %w", err)
	}
	var scripts []Script
	if err := json.Unmarshal(raw, &scripts); err != nil {
		return nil, fmt.Errorf("parsing scripts: %w", err)
	}
	return scripts, nil
}

// GetScriptByNameOrID returns a single script by numeric ID or exact name.
// If hostID is non-empty, the search is scoped to scripts applicable to that host.
func GetScriptByNameOrID(c *api.Client, nameOrID string, hostID string) (*Script, error) {
	var extraParams map[string]any
	if isNumeric(nameOrID) {
		extraParams = map[string]any{"scriptids": []string{nameOrID}}
	} else {
		extraParams = map[string]any{"filter": map[string]any{"name": []string{nameOrID}}}
	}

	params := map[string]any{
		"output": []string{"scriptid", "name", "type", "execute_on", "description", "command"},
	}
	for k, v := range extraParams {
		params[k] = v
	}
	if hostID != "" {
		params["hostids"] = []string{hostID}
	}

	raw, err := c.Call("script.get", params)
	if err != nil {
		return nil, fmt.Errorf("script.get: %w", err)
	}
	var scripts []Script
	if err := json.Unmarshal(raw, &scripts); err != nil {
		return nil, fmt.Errorf("parsing scripts: %w", err)
	}
	if len(scripts) == 0 {
		return nil, &NotFoundError{Resource: "script", Query: nameOrID}
	}
	return &scripts[0], nil
}

// RunScript executes a script on a host and returns the result.
func RunScript(c *api.Client, scriptID, hostID string) (*ScriptResult, error) {
	params := map[string]any{
		"scriptid": scriptID,
		"hostid":   hostID,
	}
	raw, err := c.Call("script.execute", params)
	if err != nil {
		return nil, fmt.Errorf("script.execute: %w", err)
	}
	var result struct {
		Response string `json:"response"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing script result: %w", err)
	}
	return &ScriptResult{
		ScriptID: scriptID,
		HostID:   hostID,
		Response: result.Response,
		Value:    result.Value,
	}, nil
}

func scriptTypeLabel(s string) string {
	switch s {
	case "0":
		return "script"
	case "1":
		return "ipmi"
	case "2":
		return "ssh"
	case "3":
		return "telnet"
	case "5":
		return "webhook"
	default:
		return s
	}
}

func executeOnLabel(s string) string {
	switch s {
	case "0":
		return "server"
	case "1":
		return "agent"
	case "2":
		return "server-proxy"
	default:
		return s
	}
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go test ./internal/api/resources/... -run TestScriptTypeLabel -run TestExecuteOnLabel
```
Expected: `ok`

- [ ] **Step 5: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 6: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/api/resources/scripts.go internal/api/resources/scripts_test.go
git commit -m "feat: add scripts resource (GetScripts, RunScript)"
```

---

## Task 4: Alerts resource

**Files:**
- Create: `internal/api/resources/alerts.go`

- [ ] **Step 1: Create alerts.go**

```go
// internal/api/resources/alerts.go
package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
)

// Alert represents a fired notification (alert) in Zabbix.
type Alert struct {
	AlertID     string `json:"alertid"`
	EventID     string `json:"eventid"`
	HostID      string // resolved via event enrichment
	Host        string // resolved via event enrichment
	Clock       string `json:"clock"`
	SendTo      string `json:"sendto"`
	Subject     string `json:"subject"`
	Status      string `json:"status"`      // "0"=not_sent, "1"=sent, "2"=failed, "3"=new
	Error       string `json:"error"`
	UserID      string `json:"userid"`
	MediaTypeID string `json:"mediatypeid"`
}

// AlertFilter controls which alerts are returned.
type AlertFilter struct {
	HostIDs  []string
	EventIDs []string
	Since    int64  // Unix timestamp; 0 = no filter
	Status   string // "sent"|"failed"|"" (all)
	Limit    int
}

// GetAlerts returns notifications. Host and HostID fields are populated by
// resolving each alert's event to find the associated host.
func GetAlerts(c *api.Client, f AlertFilter) ([]Alert, error) {
	params := map[string]any{
		"output":    []string{"alertid", "eventid", "clock", "sendto", "subject", "status", "error", "userid", "mediatypeid"},
		"sortfield": "alertid",
		"sortorder": "DESC",
	}
	if len(f.HostIDs) > 0 {
		params["hostids"] = f.HostIDs
	}
	if len(f.EventIDs) > 0 {
		params["eventids"] = f.EventIDs
	}
	if f.Since > 0 {
		params["time_from"] = f.Since
	}
	switch f.Status {
	case "sent":
		params["filter"] = map[string]any{"status": []string{"1"}}
	case "failed":
		params["filter"] = map[string]any{"status": []string{"2"}}
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}

	raw, err := c.Call("alert.get", params)
	if err != nil {
		return nil, fmt.Errorf("alert.get: %w", err)
	}
	var alerts []Alert
	if err := json.Unmarshal(raw, &alerts); err != nil {
		return nil, fmt.Errorf("parsing alerts: %w", err)
	}

	if len(alerts) == 0 {
		return alerts, nil
	}

	// Enrich alerts with host info via event lookup.
	eventIDs := make([]string, 0, len(alerts))
	seen := make(map[string]bool)
	for _, a := range alerts {
		if a.EventID != "" && !seen[a.EventID] {
			eventIDs = append(eventIDs, a.EventID)
			seen[a.EventID] = true
		}
	}

	hostByEvent, err := resolveEventHosts(c, eventIDs)
	if err != nil {
		// Non-fatal: return alerts without host enrichment rather than failing.
		return alerts, nil
	}

	for i := range alerts {
		if h, ok := hostByEvent[alerts[i].EventID]; ok {
			alerts[i].HostID = h.HostID
			alerts[i].Host = h.Host
		}
	}
	return alerts, nil
}

// resolveEventHosts returns a map of eventid → EventHost by calling event.get.
func resolveEventHosts(c *api.Client, eventIDs []string) (map[string]EventHost, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}
	params := map[string]any{
		"eventids":    eventIDs,
		"output":      []string{"eventid"},
		"selectHosts": []string{"hostid", "host"},
	}
	raw, err := c.Call("event.get", params)
	if err != nil {
		return nil, fmt.Errorf("event.get (host enrichment): %w", err)
	}
	var events []struct {
		EventID string      `json:"eventid"`
		Hosts   []EventHost `json:"hosts"`
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, fmt.Errorf("parsing events (host enrichment): %w", err)
	}
	m := make(map[string]EventHost, len(events))
	for _, ev := range events {
		if len(ev.Hosts) > 0 {
			m[ev.EventID] = ev.Hosts[0]
		}
	}
	return m, nil
}

func alertStatusLabel(s string) string {
	switch s {
	case "0":
		return "not_sent"
	case "1":
		return "sent"
	case "2":
		return "failed"
	case "3":
		return "new"
	default:
		return s
	}
}
```

- [ ] **Step 2: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 3: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/api/resources/alerts.go
git commit -m "feat: add alerts resource (GetAlerts with event host enrichment)"
```

---

## Task 5: Interfaces resource

**Files:**
- Create: `internal/api/resources/interfaces.go`

- [ ] **Step 1: Create interfaces.go**

Zabbix returns `"details": []` (empty array) for non-SNMP interfaces and `"details": {...}` for SNMP. Use `json.RawMessage` to handle the type mismatch.

```go
// internal/api/resources/interfaces.go
package resources

import (
	"encoding/json"
	"fmt"

	"codeberg.org/mwtrigg/zbxctl/internal/api"
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
```

- [ ] **Step 2: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 3: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/api/resources/interfaces.go
git commit -m "feat: add interfaces resource (GetInterfaces via host.get selectInterfaces)"
```

---

## Task 6: CLI get commands — items state, scripts, alerts, interfaces

**Files:**
- Modify: `internal/cli/get.go`

- [ ] **Step 1: Add `--state` flag and state output to runGetItems**

In `get.go`, add `getItemsState string` to the flag block near the other `getItems*` vars, register it in `init()`, and extend `runGetItems`:

```go
var (
	getItemsKey    string
	getItemsStatus string
	getItemsState  string // "normal" | "unsupported" | ""
	getItemsLimit  int
)
```

In the `init()` that registers `getItemsCmd` flags, add:
```go
f.StringVar(&getItemsState, "state", "", "filter by collection state: normal|unsupported")
```

In `runGetItems`, translate and apply the state filter (add this after the status switch):
```go
switch getItemsState {
case "normal":
    filter.State = "0"
case "unsupported":
    filter.State = "1"
case "":
    // no filter
default:
    return cliPrintErr(outFmt, fmt.Sprintf("invalid --state %q: use normal or unsupported", getItemsState), output.ExitError)
}
```

Add `state` and `error` to the columns and rows in `runGetItems`:
```go
cols := []output.Column{
    {Key: "itemid", Header: "ITEM_ID"},
    {Key: "name", Header: "NAME"},
    {Key: "key_", Header: "KEY"},
    {Key: "status", Header: "STATUS"},
    {Key: "state", Header: "STATE"},
    {Key: "units", Header: "UNITS"},
    {Key: "delay", Header: "DELAY"},
}
rows := make([]map[string]any, len(items))
for i, it := range items {
    stateLabel := "normal"
    if it.State == "1" {
        stateLabel = "unsupported"
    }
    rows[i] = map[string]any{
        "itemid":         it.ItemID,
        "hostid":         it.HostID,
        "name":           it.Name,
        "key_":           it.Key,
        "type":           it.Type,
        "value_type":     it.ValueType,
        "status":         enabledLabel(it.Status),
        "status_raw":     it.Status,
        "state":          stateLabel,
        "state_raw":      it.State,
        "error":          it.Error,
        "lastvalue":      it.LastValue,
        "lastclock":      fmtUnix(it.LastClock),
        "lastclock_unix": it.LastClock,
        "units":          it.Units,
        "delay":          it.Delay,
    }
}
```

- [ ] **Step 2: Add runGetScripts and getScriptsCmd**

Add near the bottom of get.go, before the severity parsing section:

```go
// ── get scripts ───────────────────────────────────────────────────────────────

var getScriptsHost string

var getScriptsCmd = &cobra.Command{
	Use:   "scripts",
	Short: "List Zabbix scripts",
	RunE:  runGetScripts,
}

func init() {
	getScriptsCmd.Flags().StringVar(&getScriptsHost, "host", "", "filter to scripts applicable to this host (name or ID)")
	getCmd.AddCommand(getScriptsCmd)
}

func runGetScripts(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	f := resources.ScriptFilter{}
	if getScriptsHost != "" {
		id, err := resources.ResolveHostID(c, getScriptsHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		f.HostIDs = []string{id}
	}

	scripts, err := resources.GetScripts(c, f)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "scriptid", Header: "SCRIPT_ID"},
		{Key: "name", Header: "NAME"},
		{Key: "type", Header: "TYPE"},
		{Key: "execute_on", Header: "EXECUTE_ON"},
		{Key: "description", Header: "DESCRIPTION"},
	}
	rows := make([]map[string]any, len(scripts))
	for i, s := range scripts {
		rows[i] = map[string]any{
			"scriptid":    s.ScriptID,
			"name":        s.Name,
			"type":        resources.ScriptTypeLabel(s.Type),
			"type_raw":    s.Type,
			"execute_on":  resources.ExecuteOnLabel(s.ExecuteOn),
			"execute_on_raw": s.ExecuteOn,
			"description": s.Description,
			"command":     s.Command,
		}
	}
	return output.Print(outFmt, cols, rows)
}
```

Note: `scriptTypeLabel` and `executeOnLabel` are unexported in scripts.go. Export them by renaming to `ScriptTypeLabel` and `ExecuteOnLabel` in scripts.go, or add thin wrappers. The simplest fix: export them directly in scripts.go (rename `scriptTypeLabel` → `ScriptTypeLabel`, `executeOnLabel` → `ExecuteOnLabel`) and update scripts_test.go accordingly.

- [ ] **Step 3: Export label helpers in scripts.go and update test**

In `internal/api/resources/scripts.go`, rename:
```go
func ScriptTypeLabel(s string) string { ... }
func ExecuteOnLabel(s string) string  { ... }
```

In `internal/api/resources/scripts_test.go`, update call sites:
```go
if got := resources.ScriptTypeLabel(tt.in); ... // won't work in same package test
// Since tests are in package resources (no _test suffix), call without package prefix:
if got := ScriptTypeLabel(tt.in); ...
if got := ExecuteOnLabel(tt.in); ...
```

- [ ] **Step 4: Add runGetAlerts and getAlertsCmd**

```go
// ── get alerts ────────────────────────────────────────────────────────────────

var (
	getAlertsHost   string
	getAlertsEvent  string
	getAlertsSince  string
	getAlertsStatus string
	getAlertsLimit  int
)

var getAlertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "List notification alerts (delivery status)",
	RunE:  runGetAlerts,
}

func init() {
	f := getAlertsCmd.Flags()
	f.StringVar(&getAlertsHost, "host", "", "filter by host name or ID")
	f.StringVar(&getAlertsEvent, "event", "", "filter by event ID")
	f.StringVar(&getAlertsSince, "since", "", "alerts after this time: unix timestamp, RFC3339, or YYYY-MM-DD")
	f.StringVar(&getAlertsStatus, "status", "", "filter by delivery status: sent|failed")
	f.IntVar(&getAlertsLimit, "limit", 0, "max results")
	getCmd.AddCommand(getAlertsCmd)
}

func runGetAlerts(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	f := resources.AlertFilter{
		Limit:  effectiveLimit(getAlertsLimit, ctx),
		Status: getAlertsStatus,
	}

	if getAlertsHost != "" {
		id, err := resources.ResolveHostID(c, getAlertsHost)
		if err != nil {
			var nfe *resources.NotFoundError
			if errors.As(err, &nfe) {
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		f.HostIDs = []string{id}
	}
	if getAlertsEvent != "" {
		f.EventIDs = []string{getAlertsEvent}
	}
	if getAlertsSince != "" {
		ts, err := parseTimestamp(getAlertsSince)
		if err != nil {
			return cliPrintErr(outFmt, err.Error(), output.ExitError)
		}
		f.Since = ts
	}

	switch getAlertsStatus {
	case "sent", "failed", "":
		// valid
	default:
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --status %q: use sent or failed", getAlertsStatus), output.ExitError)
	}

	alerts, err := resources.GetAlerts(c, f)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "alertid", Header: "ALERT_ID"},
		{Key: "eventid", Header: "EVENT_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "sendto", Header: "SEND_TO"},
		{Key: "status", Header: "STATUS"},
		{Key: "clock", Header: "TIME"},
	}
	rows := make([]map[string]any, len(alerts))
	for i, a := range alerts {
		rows[i] = map[string]any{
			"alertid":     a.AlertID,
			"eventid":     a.EventID,
			"hostid":      a.HostID,
			"host":        a.Host,
			"clock":       fmtUnix(a.Clock),
			"clock_unix":  a.Clock,
			"sendto":      a.SendTo,
			"subject":     a.Subject,
			"status":      resources.AlertStatusLabel(a.Status),
			"status_raw":  a.Status,
			"error":       a.Error,
			"userid":      a.UserID,
			"mediatypeid": a.MediaTypeID,
		}
	}
	return output.Print(outFmt, cols, rows)
}
```

Export `alertStatusLabel` → `AlertStatusLabel` in alerts.go.

- [ ] **Step 5: Add runGetInterfaces and getInterfacesCmd**

```go
// ── get interfaces ────────────────────────────────────────────────────────────

var getInterfacesCmd = &cobra.Command{
	Use:   "interfaces <host>",
	Short: "Show network interfaces for a host",
	Args:  cobra.ExactArgs(1),
	RunE:  runGetInterfaces,
}

func init() { getCmd.AddCommand(getInterfacesCmd) }

func runGetInterfaces(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	hostID, err := resources.ResolveHostID(c, args[0])
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	ifaces, err := resources.GetInterfaces(c, hostID)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "interfaceid", Header: "IFACE_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "type", Header: "TYPE"},
		{Key: "ip", Header: "IP"},
		{Key: "dns", Header: "DNS"},
		{Key: "port", Header: "PORT"},
		{Key: "available", Header: "AVAILABLE"},
		{Key: "error", Header: "ERROR"},
	}
	rows := make([]map[string]any, len(ifaces))
	for i, iface := range ifaces {
		rows[i] = map[string]any{
			"interfaceid":       iface.InterfaceID,
			"hostid":            iface.HostID,
			"host":              iface.Host,
			"type":              iface.Type,
			"type_raw":          iface.TypeRaw,
			"main":              iface.Main,
			"useip":             iface.UseIP,
			"ip":                iface.IP,
			"dns":               iface.DNS,
			"port":              iface.Port,
			"available":         iface.Available,
			"available_raw":     iface.AvailableRaw,
			"error":             iface.Error,
			"snmp_version":      iface.SNMPVersion,
			"snmp_community":    iface.SNMPCommunity,
			"snmp_securityname": iface.SNMPSecurityName,
		}
	}
	return output.Print(outFmt, cols, rows)
}
```

- [ ] **Step 6: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 7: Run tests**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go test ./...
```
Expected: all pass

- [ ] **Step 8: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/api/resources/scripts.go internal/api/resources/scripts_test.go internal/api/resources/alerts.go internal/api/resources/interfaces.go internal/cli/get.go
git commit -m "feat: get items --state, get scripts, get alerts, get interfaces CLI commands"
```

---

## Task 7: `run script` command

**Files:**
- Modify: `internal/cli/write.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Add generateToken helper to write.go**

Add these imports to write.go (merge with existing import block):
```go
import (
    "bufio"
    "crypto/rand"
    "errors"
    "fmt"
    "math/big"
    "os"
    "strings"
    "time"

    "github.com/spf13/cobra"

    "codeberg.org/mwtrigg/zbxctl/internal/api"
    "codeberg.org/mwtrigg/zbxctl/internal/api/resources"
    "codeberg.org/mwtrigg/zbxctl/internal/config"
    "codeberg.org/mwtrigg/zbxctl/internal/output"
)
```

Add at the bottom of write.go:
```go
// generateToken returns a 6-character random alphanumeric string for run script confirmation.
func generateToken() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}
```

- [ ] **Step 2: Add runRunScript, runScriptCmd, and runCmd to write.go**

```go
// ── run script ────────────────────────────────────────────────────────────────

var runScriptHost string

var runScriptCmd = &cobra.Command{
	Use:   "script <name-or-id>",
	Short: "Execute a Zabbix script on a host",
	Long: `Execute a script from the Zabbix script library on a monitored host.

SECURITY: This command executes code on a monitored host. It requires:
  - allowed_scripts in the active context config (run script is blocked otherwise)
  - Manual token confirmation (--yes has no effect on this command)

Recommended: use a read-only API token for agent contexts. Enable run script
only for pre-reviewed diagnostic scripts (ping, traceroute, read-only checks).`,
	Args: cobra.ExactArgs(1),
	RunE: runRunScript,
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute remote operations",
}

func init() {
	runScriptCmd.Flags().StringVar(&runScriptHost, "host", "", "host name or ID (required)")
	_ = runScriptCmd.MarkFlagRequired("host")
	runCmd.AddCommand(runScriptCmd)
}

func runRunScript(cmd *cobra.Command, args []string) error {
	// Note: writeClient() internally calls newClient(); permission check below.
	c, outFmt, ctx, err := writeClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	// allowed_scripts check — independent of permission tier.
	if len(ctx.AllowedScripts) == 0 {
		return cliPrintErr(outFmt,
			"run script is blocked: context has no allowed_scripts configured\n"+
				"Add allowed_scripts to this context in ~/.config/zbxctl/config.yaml",
			output.ExitError)
	}

	nameOrID := args[0]

	// Resolve host.
	hostID, err := resources.ResolveHostID(c, runScriptHost)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	// Resolve script.
	script, err := resources.GetScriptByNameOrID(c, nameOrID, hostID)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	// Check against allowed_scripts allowlist.
	allowed := false
	for _, name := range ctx.AllowedScripts {
		if name == script.Name {
			allowed = true
			break
		}
	}
	if !allowed {
		return cliPrintErr(outFmt,
			fmt.Sprintf("script %q is not in allowed_scripts for this context", script.Name),
			output.ExitError)
	}

	// Always print script details — no flag can suppress this output.
	fmt.Fprintf(os.Stderr, "\n⚠  Script execution details:\n")
	fmt.Fprintf(os.Stderr, "   Script:     %s (id: %s)\n", script.Name, script.ScriptID)
	fmt.Fprintf(os.Stderr, "   Type:       %s\n", resources.ScriptTypeLabel(script.Type))
	fmt.Fprintf(os.Stderr, "   Execute on: %s\n", resources.ExecuteOnLabel(script.ExecuteOn))
	fmt.Fprintf(os.Stderr, "   Host:       %s (id: %s)\n", runScriptHost, hostID)
	if script.Command != "" {
		fmt.Fprintf(os.Stderr, "   Command:\n")
		for _, line := range strings.Split(script.Command, "\n") {
			fmt.Fprintf(os.Stderr, "     %s\n", line)
		}
	}
	fmt.Fprintln(os.Stderr)

	// Token confirmation — --yes has no effect.
	token := generateToken()
	deadline := time.Now().Add(60 * time.Second)
	fmt.Fprintf(os.Stderr, "Confirmation token (expires in 60s): %s\n", token)
	fmt.Fprintf(os.Stderr, "Type the token above to execute: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		fmt.Fprintln(os.Stderr, "\naborted")
		return &cliErr{code: output.ExitError}
	}
	input := strings.TrimSpace(scanner.Text())
	if time.Now().After(deadline) {
		return cliPrintErr(outFmt, "token expired (60s TTL exceeded)", output.ExitError)
	}
	if input != token {
		return cliPrintErr(outFmt, "token mismatch: aborted", output.ExitError)
	}

	// Execute.
	result, err := resources.RunScript(c, script.ScriptID, hostID)
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	// Populate readable host/script name on result.
	result.ScriptName = script.Name
	result.Host = runScriptHost

	return output.Print(outFmt, []output.Column{
		{Key: "scriptid", Header: "SCRIPT_ID"},
		{Key: "script_name", Header: "SCRIPT"},
		{Key: "hostid", Header: "HOST_ID"},
		{Key: "host", Header: "HOST"},
		{Key: "response", Header: "RESPONSE"},
		{Key: "value", Header: "OUTPUT"},
	}, []map[string]any{{
		"scriptid":    result.ScriptID,
		"script_name": result.ScriptName,
		"hostid":      result.HostID,
		"host":        result.Host,
		"response":    result.Response,
		"value":       result.Value,
	}})
}
```

- [ ] **Step 3: Register runCmd in root.go**

In `internal/cli/root.go`, add `rootCmd.AddCommand(runCmd)` to the `init()` function:

```go
func init() {
	// ... existing flag setup ...

	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(contextCmd)
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(runCmd)        // add this
	rootCmd.AddCommand(enableCmd)
	rootCmd.AddCommand(disableCmd)
	rootCmd.AddCommand(acknowledgeCmd)
	rootCmd.AddCommand(maintenanceCmd)
	rootCmd.AddCommand(llmCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(versionCmd)
}
```

- [ ] **Step 4: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 5: Verify --yes is silently ignored by run script**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && ./zbxctl run script --help
```
Expected: `--host` flag shown, `--yes` flag NOT listed.

- [ ] **Step 6: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/cli/write.go internal/cli/root.go
git commit -m "feat: run script command with token confirmation and allowed_scripts enforcement"
```

---

## Task 8: `diagnose host`

**Files:**
- Create: `internal/cli/diagnose.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Create diagnose.go**

```go
// internal/cli/diagnose.go
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"codeberg.org/mwtrigg/zbxctl/internal/api/resources"
	"codeberg.org/mwtrigg/zbxctl/internal/output"
)

var diagSince string

var diagnoseHostCmd = &cobra.Command{
	Use:   "host <name|id>",
	Short: "Single-call composite snapshot for agent-driven triage",
	Long: `Returns a composite snapshot of host state: open problems, recent events,
latest metric values, broken items, and interface availability.

Output is always JSON or JSONL (table falls back to JSON — the structure is
too nested for flat table output).`,
	Args: cobra.ExactArgs(1),
	RunE: runDiagnoseHost,
}

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose",
	Short: "Composite diagnostic commands",
}

func init() {
	diagnoseHostCmd.Flags().StringVar(&diagSince, "since", "24h",
		"event window before now (e.g. 1h, 6h, 24h)")
	diagnoseCmd.AddCommand(diagnoseHostCmd)
}

func runDiagnoseHost(cmd *cobra.Command, args []string) error {
	c, outFmt, _, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	// Force JSON if table was requested — composite structure cannot be tabulated.
	if string(outFmt) == "table" {
		outFmt = output.Format("json")
	}

	target := args[0]

	// Resolve host ID first; all parallel calls need it.
	hostID, err := resources.ResolveHostID(c, target)
	if err != nil {
		var nfe *resources.NotFoundError
		if err != nil {
			if nfErr, ok := err.(*resources.NotFoundError); ok {
				_ = nfErr
				return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
			}
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	sinceDur, err := time.ParseDuration(diagSince)
	if err != nil {
		return cliPrintErr(outFmt, fmt.Sprintf("invalid --since %q: use Go duration e.g. 1h, 24h", diagSince), output.ExitError)
	}
	sinceTS := time.Now().Add(-sinceDur).Unix()

	// Five parallel API calls.
	var (
		hostObj     *resources.Host
		ifaces      []resources.Interface
		problems    []resources.Problem
		evts        []resources.Event
		latestItems []resources.Item
		brokenItems []resources.Item
	)

	g, _ := errgroup.WithContext(cmd.Context())

	g.Go(func() error {
		h, err := resources.GetHost(c, hostID)
		if err != nil {
			return err
		}
		hostObj = h
		return nil
	})
	g.Go(func() error {
		ii, err := resources.GetInterfaces(c, hostID)
		if err != nil {
			ifaces = nil // non-fatal: interfaces may be empty
			return nil
		}
		ifaces = ii
		return nil
	})
	g.Go(func() error {
		ps, err := resources.GetProblems(c, resources.ProblemFilter{
			HostIDs:     []string{hostID},
			MinSeverity: -1,
		})
		if err != nil {
			return err
		}
		problems = ps
		return nil
	})
	g.Go(func() error {
		evs, err := resources.GetEvents(c, resources.EventFilter{
			HostIDs: []string{hostID},
			Since:   sinceTS,
		})
		if err != nil {
			return err
		}
		evts = evs
		return nil
	})
	g.Go(func() error {
		items, err := resources.GetLatestData(c, resources.ItemFilter{
			HostIDs: []string{hostID},
		})
		if err != nil {
			return err
		}
		latestItems = items
		return nil
	})
	g.Go(func() error {
		items, err := resources.GetItems(c, resources.ItemFilter{
			HostIDs: []string{hostID},
			State:   "1", // not_supported
		})
		if err != nil {
			return err
		}
		brokenItems = items
		return nil
	})

	if err := g.Wait(); err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	// Build interface availability summary for host section.
	ifaceAvail := "unknown"
	if len(ifaces) > 0 {
		for _, iface := range ifaces {
			if iface.Main == "1" && iface.TypeRaw == "1" { // main agent interface
				ifaceAvail = iface.Available
				break
			}
		}
	}

	// Assemble sections.
	hostSection := map[string]any{
		"hostid":    hostObj.HostID,
		"host":      hostObj.Host,
		"name":      hostObj.Name,
		"status":    hostStatusLabel(hostObj.Status),
		"available": availableLabel(hostObj.Available),
		"iface_available": ifaceAvail,
	}

	openProblems := make([]map[string]any, len(problems))
	for i, p := range problems {
		openProblems[i] = map[string]any{
			"eventid":      p.EventID,
			"name":         p.Name,
			"severity":     severityLabel(p.Severity),
			"severity_raw": p.Severity,
			"clock":        fmtUnix(p.Clock),
			"clock_unix":   p.Clock,
			"acknowledged": acknowledgedLabel(p.Acknowledged),
		}
	}

	recentEvents := make([]map[string]any, len(evts))
	for i, ev := range evts {
		hostName := ""
		if len(ev.Hosts) > 0 {
			hostName = ev.Hosts[0].Host
		}
		recentEvents[i] = map[string]any{
			"eventid":    ev.EventID,
			"host":       hostName,
			"name":       ev.Name,
			"value":      triggerValueLabel(ev.Value),
			"clock":      fmtUnix(ev.Clock),
			"clock_unix": ev.Clock,
		}
	}

	latestData := make([]map[string]any, len(latestItems))
	for i, it := range latestItems {
		latestData[i] = map[string]any{
			"itemid":         it.ItemID,
			"key_":           it.Key,
			"name":           it.Name,
			"lastvalue":      it.LastValue,
			"lastclock":      fmtUnix(it.LastClock),
			"lastclock_unix": it.LastClock,
			"units":          it.Units,
		}
	}

	brokenSection := make([]map[string]any, len(brokenItems))
	for i, it := range brokenItems {
		brokenSection[i] = map[string]any{
			"itemid": it.ItemID,
			"key_":   it.Key,
			"name":   it.Name,
			"error":  it.Error,
		}
	}

	metaSection := map[string]any{
		"host":         hostObj.Host,
		"since":        diagSince,
		"since_unix":   sinceTS,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}

	type section struct {
		name string
		data any
	}
	sections := []section{
		{"host", hostSection},
		{"open_problems", openProblems},
		{"recent_events", recentEvents},
		{"latestdata", latestData},
		{"broken_items", brokenSection},
		{"meta", metaSection},
	}

	enc := json.NewEncoder(os.Stdout)

	if string(outFmt) == "jsonl" {
		for _, s := range sections {
			if err := enc.Encode(map[string]any{"section": s.name, "data": s.data}); err != nil {
				return fmt.Errorf("encoding %s: %w", s.name, err)
			}
		}
		return nil
	}

	// JSON: emit single object.
	snap := map[string]any{}
	for _, s := range sections {
		snap[s.name] = s.data
	}
	enc.SetIndent("", "  ")
	return enc.Encode(snap)
}
```

- [ ] **Step 2: Register diagnoseCmd in root.go**

In `internal/cli/root.go` `init()`, add:
```go
rootCmd.AddCommand(diagnoseCmd)
```

Place it after `runCmd` in the AddCommand list.

- [ ] **Step 3: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 4: Verify help text appears**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && ./zbxctl diagnose host --help
```
Expected: help output showing `--since` flag.

- [ ] **Step 5: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/cli/diagnose.go internal/cli/root.go
git commit -m "feat: diagnose host composite snapshot command"
```

---

## Task 9: `get history --around-event`

**Files:**
- Modify: `internal/api/resources/events.go`
- Modify: `internal/cli/get.go`

**Prerequisite:** Plan 1 must be complete (Task 1 defines `GetHistory`, Task 4 defines `runGetHistory`).

- [ ] **Step 1: Add GetEventTriggerInfo to events.go**

```go
// EventTriggerInfo contains event timing and host context for history anchoring.
type EventTriggerInfo struct {
	EventID   string
	Clock     int64
	EventName string
	HostID    string
	Host      string
}

// GetEventTriggerInfo resolves an event to its timestamp and the first host
// associated with the event's trigger. Used for --around-event history queries.
func GetEventTriggerInfo(c *api.Client, eventID string) (*EventTriggerInfo, error) {
	// Step 1: resolve event → clock + triggerid.
	rawEvt, err := c.Call("event.get", map[string]any{
		"eventids": []string{eventID},
		"output":   []string{"eventid", "clock", "objectid", "name"},
	})
	if err != nil {
		return nil, fmt.Errorf("event.get: %w", err)
	}
	var evts []struct {
		EventID  string `json:"eventid"`
		Clock    string `json:"clock"`
		ObjectID string `json:"objectid"` // triggerid
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(rawEvt, &evts); err != nil {
		return nil, fmt.Errorf("parsing event: %w", err)
	}
	if len(evts) == 0 {
		return nil, &NotFoundError{Resource: "event", Query: eventID}
	}
	ev := evts[0]

	clockInt, err := strconv.ParseInt(ev.Clock, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing event clock %q: %w", ev.Clock, err)
	}

	// Step 2: resolve trigger → hostid.
	rawTrig, err := c.Call("trigger.get", map[string]any{
		"triggerids":  []string{ev.ObjectID},
		"selectHosts": []string{"hostid", "host"},
		"output":      []string{"triggerid"},
	})
	if err != nil {
		return nil, fmt.Errorf("trigger.get: %w", err)
	}
	var trigs []struct {
		Hosts []struct {
			HostID string `json:"hostid"`
			Host   string `json:"host"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(rawTrig, &trigs); err != nil {
		return nil, fmt.Errorf("parsing trigger: %w", err)
	}
	info := &EventTriggerInfo{
		EventID:   ev.EventID,
		Clock:     clockInt,
		EventName: ev.Name,
	}
	if len(trigs) > 0 && len(trigs[0].Hosts) > 0 {
		info.HostID = trigs[0].Hosts[0].HostID
		info.Host = trigs[0].Hosts[0].Host
	}
	return info, nil
}
```

Add `"strconv"` to the imports in events.go.

- [ ] **Step 2: Add --around-event and --window flags to getHistoryCmd in get.go**

In the flag block near the other `getHistory*` vars, add:
```go
var (
	// existing flags from Plan 1 ...
	getHistoryAroundEvent string
	getHistoryWindow      string
)
```

In `init()` for `getHistoryCmd`, add:
```go
f.StringVar(&getHistoryAroundEvent, "around-event", "", "anchor query to this event ID (resolves host, centers window on event clock)")
f.StringVar(&getHistoryWindow, "window", "1h", "time window for --around-event: 30m|1h|2h|6h")
```

- [ ] **Step 3: Add around-event branch to runGetHistory**

At the top of `runGetHistory`, add an early branch before the existing logic:

```go
func runGetHistory(cmd *cobra.Command, args []string) error {
	c, outFmt, ctx, err := newClient()
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	// ── around-event mode ────────────────────────────────────────────────────
	if getHistoryAroundEvent != "" {
		return runGetHistoryAroundEvent(c, outFmt, ctx)
	}

	// ── existing logic (from Plan 1) ─────────────────────────────────────────
	// ... rest of existing runGetHistory unchanged ...
}

func runGetHistoryAroundEvent(c *api.Client, outFmt output.Format, ctx *config.Context) error {
	windowDur, err := time.ParseDuration(getHistoryWindow)
	if err != nil {
		return cliPrintErr(outFmt,
			fmt.Sprintf("invalid --window %q: use 30m, 1h, 2h, or 6h", getHistoryWindow),
			output.ExitError)
	}
	half := windowDur / 2

	info, err := resources.GetEventTriggerInfo(c, getHistoryAroundEvent)
	if err != nil {
		var nfe *resources.NotFoundError
		if errors.As(err, &nfe) {
			return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
		}
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}
	if info.HostID == "" {
		return cliPrintErr(outFmt, fmt.Sprintf("could not resolve host for event %s", getHistoryAroundEvent), output.ExitError)
	}

	timeFrom := info.Clock - int64(half.Seconds())
	timeTill := info.Clock + int64(half.Seconds())

	records, err := resources.GetHistory(c, resources.HistoryQuery{
		HostIDs:  []string{info.HostID},
		KeySearch: getHistoryItem, // reuse existing --item flag
		Since:    timeFrom,
		Until:    timeTill,
		Limit:    effectiveLimit(getHistoryLimit, ctx),
	})
	if err != nil {
		return cliPrintErr(outFmt, err.Error(), output.ExitError)
	}

	cols := []output.Column{
		{Key: "host", Header: "HOST"},
		{Key: "key_", Header: "KEY"},
		{Key: "clock", Header: "TIME"},
		{Key: "value", Header: "VALUE"},
	}
	rows := make([]map[string]any, len(records))
	for i, r := range records {
		rows[i] = map[string]any{
			"host":       r.Host,
			"hostid":     r.HostID,
			"itemid":     r.ItemID,
			"key_":       r.Key,
			"clock":      fmtUnix(r.Clock),
			"clock_unix": r.ClockUnix,
			"value":      r.Value,
		}
	}

	// In JSON mode, append meta section.
	if string(outFmt) == "json" || string(outFmt) == "jsonl" {
		meta := map[string]any{
			"event_id":    info.EventID,
			"event_clock": fmtUnix(fmt.Sprintf("%d", info.Clock)),
			"event_name":  info.EventName,
			"window_from": fmtUnix(fmt.Sprintf("%d", timeFrom)),
			"window_till": fmtUnix(fmt.Sprintf("%d", timeTill)),
		}
		rows = append(rows, map[string]any{"_meta": meta})
	}

	return output.Print(outFmt, cols, rows)
}
```

Note: `GetHistory` and `HistoryQuery` are defined in Plan 1. The `getHistoryItem` and `getHistoryLimit` flag vars are also defined in Plan 1's `runGetHistory`.

- [ ] **Step 4: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 5: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/api/resources/events.go internal/cli/get.go
git commit -m "feat: get history --around-event event-anchored history query"
```

---

## Task 10: LLM manifest updates

**Files:**
- Modify: `internal/cli/llm.go`

- [ ] **Step 1: Add Dangerous field to CommandSchema**

In `internal/cli/llm.go`, extend `CommandSchema`:

```go
// CommandSchema describes a single zbxctl command for agent consumption.
type CommandSchema struct {
	Command      string       `json:"command"`
	Description  string       `json:"description"`
	Flags        []FlagSchema `json:"flags"`
	OutputFields []string     `json:"output_fields"`
	Examples     []string     `json:"examples"`
	Dangerous    bool         `json:"dangerous,omitempty"`
}
```

- [ ] **Step 2: Add Plan 2 manifest entries to the capabilities slice**

Append the following entries to the `capabilities` var in `llm.go`. These cover all Plan 2 commands. (Plan 1 commands are added by Plan 1 Task 7.)

```go
	// ── Plan 2: agent troubleshooting surface ─────────────────────────────────
	{
		Command:     "get items",
		Description: "List items (metrics) for a host. --state unsupported surfaces items where Zabbix cannot collect data (broken monitoring, not real problems). --state normal returns only healthy items.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "key", Type: "string", Description: "Substring search on item key.", Required: false, Default: ""},
			FlagSchema{Name: "status", Type: "string", Description: "Filter by status: enabled|disabled.", Required: false, Default: ""},
			FlagSchema{Name: "state", Type: "string", Description: "Filter by collection state: normal|unsupported. Use unsupported to find broken items.", Required: false, Default: ""},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"itemid", "hostid", "name", "key_", "type", "value_type", "status", "status_raw", "state", "state_raw", "error", "lastvalue", "lastclock", "lastclock_unix", "units", "delay"},
		Examples: []string{
			"zbxctl get items web-server-01 --state unsupported --output jsonl",
			"zbxctl get items 10001 --key cpu --status enabled",
		},
	},
	{
		Command:      "get scripts",
		Description:  "List Zabbix scripts visible to the authenticated user. Use --host to filter to scripts applicable to a specific host.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "host", Type: "string", Description: "Filter to scripts applicable to this host (name or ID).", Required: false, Default: ""},
		),
		OutputFields: []string{"scriptid", "name", "type", "type_raw", "execute_on", "execute_on_raw", "description", "command"},
		Examples: []string{
			"zbxctl get scripts",
			"zbxctl get scripts --host web-server-01 --output jsonl",
		},
	},
	{
		Command:     "run script",
		Description: "Execute a Zabbix script on a host. DANGEROUS: executes code on the monitored host. Requires allowed_scripts in context config. Always prompts with a token — --yes has no effect.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "host", Type: "string", Description: "Host name or ID (required).", Required: true, Default: ""},
		),
		OutputFields: []string{"scriptid", "script_name", "hostid", "host", "response", "value"},
		Examples: []string{
			"zbxctl run script Ping --host web-server-01",
			"zbxctl run script 'Check disk space' --host db-01 --output json",
		},
		Dangerous: true,
	},
	{
		Command:     "get alerts",
		Description: "List notification alerts — who was paged, via which media, and delivery status. Useful for determining whether humans are already aware of an incident.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "host", Type: "string", Description: "Filter by host name or ID.", Required: false, Default: ""},
			FlagSchema{Name: "event", Type: "string", Description: "Filter by event ID.", Required: false, Default: ""},
			FlagSchema{Name: "since", Type: "string", Description: "Alerts after this time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: false, Default: ""},
			FlagSchema{Name: "status", Type: "string", Description: "Filter by delivery status: sent|failed.", Required: false, Default: ""},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"alertid", "eventid", "hostid", "host", "clock", "clock_unix", "sendto", "subject", "status", "status_raw", "error", "userid", "mediatypeid"},
		Examples: []string{
			"zbxctl get alerts --host web-server-01 --output jsonl",
			"zbxctl get alerts --event 208317877",
			"zbxctl get alerts --status failed --since 2026-05-01",
		},
	},
	{
		Command:      "get interfaces",
		Description:  "Show all network interfaces Zabbix has configured for a host, their availability state, and connection errors. Answers 'is Zabbix reaching the right IP?' and 'why is the agent unavailable?'",
		Flags:        globalFlags(),
		OutputFields: []string{"interfaceid", "hostid", "host", "type", "type_raw", "main", "useip", "ip", "dns", "port", "available", "available_raw", "error", "snmp_version", "snmp_community", "snmp_securityname"},
		Examples: []string{
			"zbxctl get interfaces web-server-01",
			"zbxctl get interfaces 10001 --output json",
		},
	},
	{
		Command:     "diagnose host",
		Description: "Single-call composite snapshot for agent-driven triage. Returns open problems, recent events, latest metric values, broken items, and interface availability. Always JSON or JSONL.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "since", Type: "string", Description: "Event window before now (e.g. 1h, 6h, 24h). Default: 24h.", Required: false, Default: "24h"},
		),
		OutputFields: []string{"host{hostid,host,name,status,available}", "open_problems[]", "recent_events[]", "latestdata[]", "broken_items[]", "meta{host,since,generated_at}"},
		Examples: []string{
			"zbxctl diagnose host web-server-01 --output json",
			"zbxctl diagnose host 10001 --since 6h --output jsonl",
		},
	},
	{
		Command:     "get history",
		Description: "Fetch time-series metric history for items on one or more hosts. Use --around-event to automatically anchor the time window to a specific event. Supports multi-host queries via repeated --host and --group flags.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "item", Type: "string", Description: "Item key pattern (required unless using a numeric item ID).", Required: false, Default: ""},
			FlagSchema{Name: "host", Type: "string", Description: "Host name or ID (repeatable).", Required: false, Default: ""},
			FlagSchema{Name: "group", Type: "string", Description: "Host group name or ID (repeatable).", Required: false, Default: ""},
			FlagSchema{Name: "since", Type: "string", Description: "Start time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: false, Default: ""},
			FlagSchema{Name: "until", Type: "string", Description: "End time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: false, Default: ""},
			FlagSchema{Name: "around-event", Type: "string", Description: "Event ID to anchor query: resolves host and centers window on event clock.", Required: false, Default: ""},
			FlagSchema{Name: "window", Type: "string", Description: "Time window for --around-event: 30m|1h|2h|6h. Default: 1h (±30m).", Required: false, Default: "1h"},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"host", "hostid", "itemid", "key_", "clock", "clock_unix", "value"},
		Examples: []string{
			"zbxctl get history --item system.cpu.load --host web-server-01 --since 2026-05-01",
			"zbxctl get history --around-event 208317877 --window 1h --output jsonl",
			"zbxctl get history --item agent.ping --group 'Linux servers' --since 2026-05-20",
		},
	},
```

Note: The `get history` and `get items` entries above **replace** any earlier entries for those commands. Remove the original `get items` entry (the one without `state`/`error` fields) and add the extended version. If Plan 1 Task 7 already added a `get history` entry, replace it.

- [ ] **Step 3: Build to verify no compile errors**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go build ./...
```
Expected: no output

- [ ] **Step 4: Verify manifest contains dangerous flag**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && ./zbxctl llm capabilities | python3 -m json.tool | grep -A2 '"run script"'
```
Expected: output containing `"dangerous": true`

- [ ] **Step 5: Run full test suite**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && go test ./...
```
Expected: all pass

- [ ] **Step 6: Commit**

```bash
cd /Users/mtrigg/src/mwtrigg/zbxctl && git add internal/cli/llm.go
git commit -m "feat: LLM manifest — Dangerous field, all Plan 2 command entries"
```

---

## Self-Review

**Spec coverage check:**

| Spec item | Task |
|---|---|
| Permission tiers (readonly/operator/write) + enforcement | Task 1 |
| `allowed_scripts` config field | Task 1 |
| `get items --state` (normal/unsupported) + error field | Tasks 2, 6 |
| `get scripts` | Tasks 3, 6 |
| `run script` with token confirmation + dangerous flag | Tasks 3, 7, 10 |
| `run script` `--yes` has no effect | Task 7 |
| `run script` always prints script body | Task 7 |
| Script blocked when `allowed_scripts` absent | Task 7 |
| `get alerts` with host enrichment | Tasks 4, 6 |
| `get interfaces` with SNMP details | Tasks 5, 6 |
| `diagnose host` 5-parallel-call composite | Task 8 |
| `diagnose host` JSON/JSONL output (table → JSON fallback) | Task 8 |
| `get history --around-event` 4-step resolution | Tasks 9 |
| `Dangerous bool` in `CommandSchema` | Task 10 |
| LLM manifest entries for all new commands | Task 10 |

**Type consistency check:**

- `resources.ScriptTypeLabel` / `resources.ExecuteOnLabel` — exported in Task 3, used in Tasks 6 and 7. ✓
- `resources.AlertStatusLabel` — exported in Task 4, used in Task 6. ✓
- `resources.HistoryQuery` / `resources.GetHistory` — defined in Plan 1 Task 1, used in Task 9. ✓
- `resources.ItemFilter.State` — added in Task 2, used in Tasks 6 and 8. ✓
- `resources.GetEventTriggerInfo` — added in Task 9's events.go step. ✓
- `config.Context.AllowedScripts` — added in Task 1, checked in Task 7. ✓
- `config.Context.CheckPermission` — added in Task 1, called in Task 1 (existing commands) and in Plan 1's create/update/delete host. ✓

**No-op check:** Plan 1's create/update/delete host commands in `write.go` should also call `ctx.CheckPermission("write")`. This is noted in Task 1 Step 5 above — apply the same pattern. Plan 1 implementors: after `writeClient()` succeeds in `runCreateHost`, `runUpdateHost`, `runDeleteHost`, add `ctx.CheckPermission("write")`.
