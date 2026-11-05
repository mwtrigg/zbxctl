# History, Multi-Host Latestdata, and Host CRUD — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `get history`, extend `get latestdata` for multi-host/group queries, and add `create host`, `update host`, `delete host` commands.

**Architecture:** Resource functions in `internal/api/resources/` handle all Zabbix API calls. CLI commands in `internal/cli/get.go` and `internal/cli/write.go` wire flags to resources and format output. History requires a two-step API composition (item.get → history.get); host CRUD requires group/template ID resolution. Multi-host output always includes `host` and `hostid` fields regardless of cardinality.

**Tech Stack:** Go, github.com/spf13/cobra, internal api.Client (JSON-RPC), internal output.Format

**Must complete Plan 1 before Plan 2** (`get history --around-event` in Plan 2 builds on `GetHistory` defined here).

---

## File Map

| File | Action | What changes |
|---|---|---|
| `internal/api/resources/history.go` | Create | `HistoryRecord`, `HistoryQuery`, `GetHistory` |
| `internal/api/resources/items.go` | Modify | Add `GroupIDs` to `ItemFilter`; add `Host` field to `Item`; extend `GetItems`/`GetLatestData` |
| `internal/api/resources/hosts.go` | Modify | Add `ResolveGroupID`, `ResolveTemplateID`, `ResolveHostNames`, `CreateHost`, `UpdateHost`, `DeleteHost` |
| `internal/cli/get.go` | Modify | Add `runGetHistory`; extend `runGetLatestdata` |
| `internal/cli/write.go` | Modify | Add `runCreateHost`, `runUpdateHost`, `runDeleteHost` |
| `internal/cli/llm.go` | Modify | Add manifest entries for all new commands |
| `internal/api/resources/history_test.go` | Create | Unit tests for grouping and merge logic |

---

## Task 1: History resource — `internal/api/resources/history.go`

**Files:**
- Create: `internal/api/resources/history.go`
- Create: `internal/api/resources/history_test.go`

- [ ] **Step 1: Write failing tests for groupByValueType and mergeAndSort**

```go
// internal/api/resources/history_test.go
package resources

import (
    "testing"
)

func TestGroupByValueType(t *testing.T) {
    items := []itemRef{
        {ItemID: "1", HostID: "10", Host: "web-01", Key: "cpu.load[1]", ValueType: "0"},
        {ItemID: "2", HostID: "10", Host: "web-01", Key: "agent.version", ValueType: "1"},
        {ItemID: "3", HostID: "11", Host: "web-02", Key: "cpu.load[1]", ValueType: "0"},
    }
    groups := groupByValueType(items)
    if len(groups[0]) != 2 {
        t.Errorf("expected 2 float items, got %d", len(groups[0]))
    }
    if len(groups[1]) != 1 {
        t.Errorf("expected 1 string item, got %d", len(groups[1]))
    }
}

func TestMergeAndSort(t *testing.T) {
    a := []HistoryRecord{
        {ItemID: "1", Clock: "100", ClockUnix: 100},
        {ItemID: "1", Clock: "300", ClockUnix: 300},
    }
    b := []HistoryRecord{
        {ItemID: "2", Clock: "200", ClockUnix: 200},
    }
    merged := mergeAndSort(a, b)
    if len(merged) != 3 {
        t.Fatalf("expected 3 records, got %d", len(merged))
    }
    if merged[0].ClockUnix != 100 || merged[1].ClockUnix != 200 || merged[2].ClockUnix != 300 {
        t.Errorf("records not sorted by clock: %v", merged)
    }
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/api/resources/... -run TestGroupByValueType -v
```
Expected: `FAIL` — `groupByValueType undefined`

- [ ] **Step 3: Create `internal/api/resources/history.go`**

```go
package resources

import (
    "encoding/json"
    "fmt"
    "sort"
    "strconv"

    "codeberg.org/mwtrigg/zbxctl/internal/api"
)

// HistoryRecord is a single history data point enriched with host and item metadata.
type HistoryRecord struct {
    ItemID    string
    HostID    string
    Host      string
    Key       string
    Clock     string // Unix timestamp string from Zabbix
    ClockUnix int64
    Value     string // all Zabbix history values are strings
}

// HistoryQuery describes a history fetch request.
// Set ItemIDs for direct lookup. Set HostIDs/GroupIDs + KeySearch for resolution.
// If neither is set and ItemIDs is empty, returns an error.
type HistoryQuery struct {
    ItemIDs   []string
    HostIDs   []string
    GroupIDs  []string
    KeySearch string
    TimeFrom  int64
    TimeTill  int64
    Limit     int
}

// itemRef is a lightweight struct used during item resolution.
type itemRef struct {
    ItemID    string
    HostID    string
    Host      string
    Key       string
    ValueType string // "0"=float, "1"=string, "2"=log, "3"=uint, "4"=text
}

// rawHistoryRecord matches the Zabbix history.get response shape.
type rawHistoryRecord struct {
    ItemID string `json:"itemid"`
    Clock  string `json:"clock"`
    NS     string `json:"ns"`
    Value  string `json:"value"`
}

// GetHistory resolves items and fetches history, returning enriched records
// sorted by clock ascending.
func GetHistory(c *api.Client, q HistoryQuery) ([]HistoryRecord, error) {
    var refs []itemRef

    if len(q.ItemIDs) > 0 {
        // Direct item ID path — skip resolution, no host/key enrichment.
        for _, id := range q.ItemIDs {
            refs = append(refs, itemRef{ItemID: id, ValueType: ""})
        }
        // Resolve value_type for each item ID.
        resolved, err := resolveItemRefs(c, q.ItemIDs, nil, nil, "")
        if err != nil {
            return nil, err
        }
        refs = resolved
    } else {
        var err error
        refs, err = resolveItemRefs(c, nil, q.HostIDs, q.GroupIDs, q.KeySearch)
        if err != nil {
            return nil, err
        }
    }

    if len(refs) == 0 {
        return nil, &NotFoundError{Resource: "item", Query: q.KeySearch}
    }

    groups := groupByValueType(refs)
    var all []HistoryRecord

    for vt, group := range groups {
        ids := make([]string, len(group))
        for i, r := range group {
            ids[i] = r.ItemID
        }
        raw, err := fetchHistoryRaw(c, ids, vt, q.TimeFrom, q.TimeTill, q.Limit)
        if err != nil {
            return nil, err
        }
        // Build lookup map for enrichment.
        refMap := make(map[string]itemRef, len(group))
        for _, r := range group {
            refMap[r.ItemID] = r
        }
        for _, r := range raw {
            ref := refMap[r.ItemID]
            ts, _ := strconv.ParseInt(r.Clock, 10, 64)
            all = append(all, HistoryRecord{
                ItemID:    r.ItemID,
                HostID:    ref.HostID,
                Host:      ref.Host,
                Key:       ref.Key,
                Clock:     r.Clock,
                ClockUnix: ts,
                Value:     r.Value,
            })
        }
    }

    return mergeAndSort(all), nil
}

// resolveItemRefs calls item.get to get itemids, hostids, host names, keys, and value_types.
func resolveItemRefs(c *api.Client, itemIDs, hostIDs, groupIDs []string, keySearch string) ([]itemRef, error) {
    params := map[string]any{
        "output":      []string{"itemid", "hostid", "key_", "value_type"},
        "selectHosts": []string{"hostid", "host"},
    }
    if len(itemIDs) > 0 {
        params["itemids"] = itemIDs
    }
    if len(hostIDs) > 0 {
        params["hostids"] = hostIDs
    }
    if len(groupIDs) > 0 {
        params["groupids"] = groupIDs
    }
    if keySearch != "" {
        params["search"] = map[string]any{"key_": keySearch}
    }

    raw, err := c.Call("item.get", params)
    if err != nil {
        return nil, fmt.Errorf("item.get: %w", err)
    }

    var items []struct {
        ItemID    string `json:"itemid"`
        HostID    string `json:"hostid"`
        Key       string `json:"key_"`
        ValueType string `json:"value_type"`
        Hosts     []struct {
            HostID string `json:"hostid"`
            Host   string `json:"host"`
        } `json:"hosts"`
    }
    if err := json.Unmarshal(raw, &items); err != nil {
        return nil, fmt.Errorf("parsing items: %w", err)
    }

    refs := make([]itemRef, len(items))
    for i, it := range items {
        hostname := ""
        if len(it.Hosts) > 0 {
            hostname = it.Hosts[0].Host
        }
        refs[i] = itemRef{
            ItemID:    it.ItemID,
            HostID:    it.HostID,
            Host:      hostname,
            Key:       it.Key,
            ValueType: it.ValueType,
        }
    }
    return refs, nil
}

// fetchHistoryRaw calls history.get for a single value_type.
func fetchHistoryRaw(c *api.Client, itemIDs []string, valueType int, from, till int64, limit int) ([]rawHistoryRecord, error) {
    params := map[string]any{
        "output":  "extend",
        "itemids": itemIDs,
        "history": valueType,
        "sortfield": "clock",
        "sortorder": "ASC",
    }
    if from > 0 {
        params["time_from"] = from
    }
    if till > 0 {
        params["time_till"] = till
    }
    if limit > 0 {
        params["limit"] = limit
    }

    raw, err := c.Call("history.get", params)
    if err != nil {
        return nil, fmt.Errorf("history.get (type %d): %w", valueType, err)
    }
    var records []rawHistoryRecord
    if err := json.Unmarshal(raw, &records); err != nil {
        return nil, fmt.Errorf("parsing history: %w", err)
    }
    return records, nil
}

// groupByValueType groups itemRefs by their value_type integer.
func groupByValueType(refs []itemRef) map[int][]itemRef {
    groups := make(map[int][]itemRef)
    for _, r := range refs {
        vt, _ := strconv.Atoi(r.ValueType)
        groups[vt] = append(groups[vt], r)
    }
    return groups
}

// mergeAndSort merges multiple slices of HistoryRecord and sorts by ClockUnix ascending.
func mergeAndSort(slices ...[]HistoryRecord) []HistoryRecord {
    var total int
    for _, s := range slices {
        total += len(s)
    }
    out := make([]HistoryRecord, 0, total)
    for _, s := range slices {
        out = append(out, s...)
    }
    sort.Slice(out, func(i, j int) bool {
        return out[i].ClockUnix < out[j].ClockUnix
    })
    return out
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/api/resources/... -run "TestGroupByValueType|TestMergeAndSort" -v
```
Expected: both PASS

- [ ] **Step 5: Verify it compiles**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add internal/api/resources/history.go internal/api/resources/history_test.go
git commit -m "feat: history resource layer — GetHistory with item resolution and value_type grouping"
```

---

## Task 2: Extend ItemFilter and Item struct for multi-host/group

**Files:**
- Modify: `internal/api/resources/items.go`

- [ ] **Step 1: Add `GroupIDs` to `ItemFilter`, `Host` to `Item`, extend both Get functions**

Replace the current `ItemFilter` struct and both functions:

```go
// Item represents a Zabbix item.
type Item struct {
    ItemID    string `json:"itemid"`
    HostID    string `json:"hostid"`
    Host      string // enriched from selectHosts; empty if not requested
    Name      string `json:"name"`
    Key       string `json:"key_"`
    Type      string `json:"type"`
    ValueType string `json:"value_type"`
    Status    string `json:"status"`
    LastValue string `json:"lastvalue"`
    LastClock string `json:"lastclock"`
    Units     string `json:"units"`
    Delay     string `json:"delay"`
}

// ItemHosts is used to unmarshal selectHosts from item.get.
type itemHostRef struct {
    HostID string `json:"hostid"`
    Host   string `json:"host"`
}

// itemWithHosts is the raw shape returned when selectHosts is requested.
type itemWithHosts struct {
    Item
    Hosts []itemHostRef `json:"hosts"`
}

// ItemFilter controls which items are returned.
type ItemFilter struct {
    HostIDs   []string
    GroupIDs  []string // new: filter by host group
    KeySearch string
    Status    string
    Limit     int
    WithHost  bool // if true, include host name in output via selectHosts
}
```

Update `GetItems`:

```go
func GetItems(c *api.Client, f ItemFilter) ([]Item, error) {
    params := map[string]any{
        "output":    []string{"itemid", "hostid", "name", "key_", "type", "value_type", "status", "lastvalue", "lastclock", "units", "delay"},
        "sortfield": "name",
        "sortorder": "ASC",
    }
    if f.WithHost {
        params["selectHosts"] = []string{"hostid", "host"}
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
    if f.Limit > 0 {
        params["limit"] = f.Limit
    }

    raw, err := c.Call("item.get", params)
    if err != nil {
        return nil, fmt.Errorf("item.get: %w", err)
    }

    if !f.WithHost {
        var items []Item
        if err := json.Unmarshal(raw, &items); err != nil {
            return nil, fmt.Errorf("parsing items: %w", err)
        }
        return items, nil
    }

    var withHosts []itemWithHosts
    if err := json.Unmarshal(raw, &withHosts); err != nil {
        return nil, fmt.Errorf("parsing items: %w", err)
    }
    items := make([]Item, len(withHosts))
    for i, wh := range withHosts {
        items[i] = wh.Item
        if len(wh.Hosts) > 0 {
            items[i].Host = wh.Hosts[0].Host
        }
    }
    return items, nil
}
```

Update `GetLatestData`:

```go
func GetLatestData(c *api.Client, f ItemFilter) ([]Item, error) {
    params := map[string]any{
        "output":    []string{"itemid", "hostid", "name", "key_", "lastvalue", "lastclock", "units", "value_type"},
        "sortfield": "name",
        "sortorder": "ASC",
    }
    if f.WithHost {
        params["selectHosts"] = []string{"hostid", "host"}
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
    if f.Limit > 0 {
        params["limit"] = f.Limit
    }

    raw, err := c.Call("item.get", params)
    if err != nil {
        return nil, fmt.Errorf("item.get (latestdata): %w", err)
    }

    var withHosts []itemWithHosts
    if err := json.Unmarshal(raw, &withHosts); err != nil {
        return nil, fmt.Errorf("parsing items: %w", err)
    }

    var out []Item
    for _, wh := range withHosts {
        if wh.Item.LastClock == "" || wh.Item.LastClock == "0" {
            continue
        }
        item := wh.Item
        if len(wh.Hosts) > 0 {
            item.Host = wh.Hosts[0].Host
        }
        out = append(out, item)
    }
    return out, nil
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/api/resources/items.go
git commit -m "feat: extend ItemFilter with GroupIDs and WithHost enrichment"
```

---

## Task 3: Host write operations

**Files:**
- Modify: `internal/api/resources/hosts.go`

- [ ] **Step 1: Add helpers and write functions to `hosts.go`**

Add after the existing functions:

```go
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
    Type      int    // 1=agent, 2=snmp, 3=ipmi, 4=jmx
    UseIP     int    // 1=IP, 0=DNS
    IP        string
    DNS       string
    Port      string
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
    HostID         string
    VisibleName    string
    Description    string
    AddGroupIDs    []string // resolved groupids to add
    RemoveGroupIDs []string // resolved groupids to remove
    AddTemplateIDs []string // resolved templateids to link
    RemoveTemplateIDs []string // resolved templateids to unlink
}

// UpdateHost fetches current groups/templates, applies diffs, and calls host.update.
func UpdateHost(c *api.Client, in UpdateHostInput) (*Host, error) {
    // Fetch current groups and templates.
    params := map[string]any{
        "output":              []string{"hostid", "host", "name", "status"},
        "hostids":             []string{in.HostID},
        "selectGroups":        []string{"groupid"},
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
        Groups    []map[string]string `json:"groups"`
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
    }

    _, err = c.Call("host.update", updateParams)
    if err != nil {
        return nil, fmt.Errorf("host.update: %w", err)
    }
    return GetHost(c, in.HostID)
}

// DeleteHost deletes a host by ID and returns the deleted host's record (fetched before deletion).
func DeleteHost(c *api.Client, hostID string) (*Host, error) {
    h, err := GetHost(c, hostID)
    if err != nil {
        return nil, err
    }
    raw, err := c.Call("host.delete", []string{hostID})
    if err != nil {
        return nil, fmt.Errorf("host.delete: %w", err)
    }
    var result struct {
        HostIDs []string `json:"hostids"`
    }
    if err := json.Unmarshal(raw, &result); err != nil {
        return nil, fmt.Errorf("parsing host.delete response: %w", err)
    }
    return h, nil
}
```

- [ ] **Step 2: Build**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/api/resources/hosts.go
git commit -m "feat: host write operations — CreateHost, UpdateHost, DeleteHost, group/template resolution helpers"
```

---

## Task 4: `get history` CLI command

**Files:**
- Modify: `internal/cli/get.go`

- [ ] **Step 1: Add flags and command wiring to `get.go` init()**

Add these flag variables near the top of the file with the other flag vars:

```go
var (
    flagHistoryItem   string
    flagHistoryHosts  []string
    flagHistoryGroups []string
    flagHistorySince  string
    flagHistoryUntil  string
)
```

Add to `init()` inside the `getCmd` block:

```go
var historyCmd = &cobra.Command{
    Use:   "history",
    Short: "Fetch time-series metric history",
    RunE:  runGetHistory,
}
historyCmd.Flags().StringVar(&flagHistoryItem, "item", "", "item key pattern or numeric item ID (required)")
historyCmd.Flags().StringArrayVar(&flagHistoryHosts, "host", nil, "filter by host name or ID (repeatable)")
historyCmd.Flags().StringArrayVar(&flagHistoryGroups, "group", nil, "filter by host group name or ID (repeatable)")
historyCmd.Flags().StringVar(&flagHistorySince, "since", "", "start time: unix timestamp, RFC3339, or YYYY-MM-DD")
historyCmd.Flags().StringVar(&flagHistoryUntil, "until", "", "end time: unix timestamp, RFC3339, or YYYY-MM-DD")
historyCmd.Flags().IntVar(&flagLimit, "limit", 0, "maximum results")
_ = historyCmd.MarkFlagRequired("item")
getCmd.AddCommand(historyCmd)
```

- [ ] **Step 2: Add `runGetHistory` function**

```go
func runGetHistory(_ *cobra.Command, _ []string) error {
    c, outFmt, ctx, err := newClient()
    if err != nil {
        return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
    }

    // Validate: non-numeric item requires at least one host or group.
    if !isNumericStr(flagHistoryItem) && len(flagHistoryHosts) == 0 && len(flagHistoryGroups) == 0 {
        return cliPrintErr(outFmt, "non-numeric --item requires at least one --host or --group for resolution", output.ExitError)
    }

    // Resolve host IDs.
    var hostIDs []string
    for _, h := range flagHistoryHosts {
        id, err := resources.ResolveHostID(c, h)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("host %q: %s", h, err), output.ExitNotFound)
        }
        hostIDs = append(hostIDs, id)
    }

    // Resolve group IDs.
    var groupIDs []string
    for _, g := range flagHistoryGroups {
        id, err := resources.ResolveGroupID(c, g)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitNotFound)
        }
        groupIDs = append(groupIDs, id)
    }

    var since, until int64
    if flagHistorySince != "" {
        since, err = parseTimestamp(flagHistorySince)
        if err != nil {
            return cliPrintErr(outFmt, "invalid --since: "+err.Error(), output.ExitError)
        }
    }
    if flagHistoryUntil != "" {
        until, err = parseTimestamp(flagHistoryUntil)
        if err != nil {
            return cliPrintErr(outFmt, "invalid --until: "+err.Error(), output.ExitError)
        }
    }

    q := resources.HistoryQuery{
        HostIDs:   hostIDs,
        GroupIDs:  groupIDs,
        KeySearch: flagHistoryItem,
        TimeFrom:  since,
        TimeTill:  until,
        Limit:     effectiveLimit(flagLimit, ctx),
    }
    if isNumericStr(flagHistoryItem) {
        q.ItemIDs = []string{flagHistoryItem}
        q.KeySearch = ""
    }

    records, err := resources.GetHistory(c, q)
    if err != nil {
        var nfe *resources.NotFoundError
        if errors.As(err, &nfe) {
            return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
        }
        return cliPrintErr(outFmt, err.Error(), output.ExitError)
    }

    cols := []output.Column{
        {Key: "host", Header: "HOST"},
        {Key: "hostid", Header: "HOSTID"},
        {Key: "itemid", Header: "ITEMID"},
        {Key: "key_", Header: "KEY"},
        {Key: "clock", Header: "TIME"},
        {Key: "clock_unix", Header: "CLOCK_UNIX"},
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
    return output.Print(outFmt, cols, rows)
}
```

Also add the helper (if not already present):

```go
func isNumericStr(s string) bool {
    if s == "" {
        return false
    }
    for _, r := range s {
        if r < '0' || r > '9' {
            return false
        }
    }
    return true
}
```

- [ ] **Step 3: Build**

```bash
go build ./... && ./zbxctl get history --help
```
Expected: help text showing `--item`, `--host`, `--group`, `--since`, `--until`, `--limit`

- [ ] **Step 4: Commit**

```bash
git add internal/cli/get.go
git commit -m "feat: zbxctl get history — two-step item resolution with multi-host support"
```

---

## Task 5: Extend `get latestdata` for multi-host/group

**Files:**
- Modify: `internal/cli/get.go`

- [ ] **Step 1: Add flag variables for multi-host latestdata**

Add near existing latestdata flag vars:

```go
var (
    flagLatestdataHosts  []string
    flagLatestdataGroups []string
    flagLatestdataItem   string // replaces/aliases --key
)
```

- [ ] **Step 2: Update the latestdata cobra command definition in `init()`**

Find the existing `latestdataCmd` definition and update it to add the new flags. The positional arg `[<host>]` becomes optional:

```go
latestdataCmd.Use = "latestdata [<host>]"
latestdataCmd.Flags().StringArrayVar(&flagLatestdataHosts, "host", nil, "host name or ID (repeatable)")
latestdataCmd.Flags().StringArrayVar(&flagLatestdataGroups, "group", nil, "host group name or ID (repeatable)")
latestdataCmd.Flags().StringVar(&flagLatestdataItem, "item", "", "filter by item key substring")
// keep existing --key as hidden alias
latestdataCmd.Flags().StringVar(&flagKey, "key", "", "")
_ = latestdataCmd.Flags().MarkHidden("key")
```

- [ ] **Step 3: Update `runGetLatestdata`**

Replace the function body to handle multi-host resolution:

```go
func runGetLatestdata(_ *cobra.Command, args []string) error {
    c, outFmt, ctx, err := newClient()
    if err != nil {
        return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
    }

    // Collect all host targets: positional arg + --host flags.
    hostTargets := append([]string{}, flagLatestdataHosts...)
    if len(args) > 0 {
        hostTargets = append(hostTargets, args[0])
    }

    if len(hostTargets) == 0 && len(flagLatestdataGroups) == 0 {
        return cliPrintErr(outFmt, "provide at least one host (positional or --host) or --group", output.ExitError)
    }

    var hostIDs []string
    for _, h := range hostTargets {
        id, err := resources.ResolveHostID(c, h)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("host %q: %s", h, err), output.ExitNotFound)
        }
        hostIDs = append(hostIDs, id)
    }

    var groupIDs []string
    for _, g := range flagLatestdataGroups {
        id, err := resources.ResolveGroupID(c, g)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitNotFound)
        }
        groupIDs = append(groupIDs, id)
    }

    // --item overrides --key if both set.
    keyFilter := flagKey
    if flagLatestdataItem != "" {
        keyFilter = flagLatestdataItem
    }

    items, err := resources.GetLatestData(c, resources.ItemFilter{
        HostIDs:   hostIDs,
        GroupIDs:  groupIDs,
        KeySearch: keyFilter,
        Limit:     effectiveLimit(flagLimit, ctx),
        WithHost:  true,
    })
    if err != nil {
        return cliPrintErr(outFmt, err.Error(), output.ExitError)
    }

    cols := []output.Column{
        {Key: "host", Header: "HOST"},
        {Key: "hostid", Header: "HOSTID"},
        {Key: "itemid", Header: "ITEMID"},
        {Key: "key_", Header: "KEY"},
        {Key: "name", Header: "NAME"},
        {Key: "value_type", Header: "VALUE_TYPE"},
        {Key: "lastvalue", Header: "LAST_VALUE"},
        {Key: "lastclock", Header: "LAST_SEEN"},
        {Key: "lastclock_unix", Header: "LAST_SEEN_UNIX"},
        {Key: "units", Header: "UNITS"},
    }
    rows := make([]map[string]any, len(items))
    for i, it := range items {
        rows[i] = map[string]any{
            "host":           it.Host,
            "hostid":         it.HostID,
            "itemid":         it.ItemID,
            "key_":           it.Key,
            "name":           it.Name,
            "value_type":     it.ValueType,
            "lastvalue":      it.LastValue,
            "lastclock":      fmtUnix(it.LastClock),
            "lastclock_unix": it.LastClock,
            "units":          it.Units,
        }
    }
    return output.Print(outFmt, cols, rows)
}
```

- [ ] **Step 4: Build**

```bash
go build ./... && ./zbxctl get latestdata --help
```
Expected: shows `[<host>]`, `--host`, `--group`, `--item`, `--key` (hidden)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/get.go
git commit -m "feat: get latestdata — multi-host/group support, --item flag, host/hostid always in output"
```

---

## Task 6: Host write commands

**Files:**
- Modify: `internal/cli/write.go`

- [ ] **Step 1: Add flag variables for host write commands**

Add at the top of write.go with other flag vars:

```go
var (
    // create host flags
    flagCreateHostName        string
    flagCreateHostGroups      []string
    flagCreateHostIP          string
    flagCreateHostDNS         string
    flagCreateHostPort        string
    flagCreateHostIfaceType   string
    flagCreateHostVisibleName string
    flagCreateHostDescription string
    flagCreateHostSNMPVersion string
    flagCreateHostSNMPCommunity string

    // update host flags
    flagUpdateHostVisibleName   string
    flagUpdateHostDescription   string
    flagUpdateHostAddGroups     []string
    flagUpdateHostRemoveGroups  []string
    flagUpdateHostAddTemplates  []string
    flagUpdateHostRemoveTemplates []string
)
```

- [ ] **Step 2: Add command definitions in `init()`**

```go
// create host
var createCmd = &cobra.Command{Use: "create", Short: "Create resources"}
var createHostCmd = &cobra.Command{
    Use:   "host",
    Short: "Create a new Zabbix host",
    RunE:  runCreateHost,
}
createHostCmd.Flags().StringVar(&flagCreateHostName, "name", "", "technical host name (required)")
createHostCmd.Flags().StringArrayVar(&flagCreateHostGroups, "group", nil, "host group name or ID (required, repeatable)")
createHostCmd.Flags().StringVar(&flagCreateHostIP, "ip", "", "interface IP address")
createHostCmd.Flags().StringVar(&flagCreateHostDNS, "dns", "", "interface DNS name")
createHostCmd.Flags().StringVar(&flagCreateHostPort, "port", "10050", "interface port")
createHostCmd.Flags().StringVar(&flagCreateHostIfaceType, "interface-type", "agent", "interface type: agent|snmp|ipmi|jmx")
createHostCmd.Flags().StringVar(&flagCreateHostVisibleName, "visible-name", "", "visible name (defaults to --name)")
createHostCmd.Flags().StringVar(&flagCreateHostDescription, "description", "", "host description")
createHostCmd.Flags().StringVar(&flagCreateHostSNMPVersion, "snmp-version", "2", "SNMP version: 1|2|3 (SNMP only)")
createHostCmd.Flags().StringVar(&flagCreateHostSNMPCommunity, "snmp-community", "public", "SNMP community (SNMP v1/v2c only)")
_ = createHostCmd.MarkFlagRequired("name")
_ = createHostCmd.MarkFlagRequired("group")
createCmd.AddCommand(createHostCmd)

// update host
var updateCmd = &cobra.Command{Use: "update", Short: "Update resources"}
var updateHostCmd = &cobra.Command{
    Use:   "host <id|name>",
    Short: "Update a Zabbix host",
    Args:  cobra.ExactArgs(1),
    RunE:  runUpdateHost,
}
updateHostCmd.Flags().StringVar(&flagUpdateHostVisibleName, "visible-name", "", "new visible name")
updateHostCmd.Flags().StringVar(&flagUpdateHostDescription, "description", "", "new description")
updateHostCmd.Flags().StringArrayVar(&flagUpdateHostAddGroups, "add-group", nil, "host group to add (repeatable)")
updateHostCmd.Flags().StringArrayVar(&flagUpdateHostRemoveGroups, "remove-group", nil, "host group to remove (repeatable)")
updateHostCmd.Flags().StringArrayVar(&flagUpdateHostAddTemplates, "add-template", nil, "template to link (repeatable)")
updateHostCmd.Flags().StringArrayVar(&flagUpdateHostRemoveTemplates, "remove-template", nil, "template to unlink (repeatable)")
updateCmd.AddCommand(updateHostCmd)

// delete host
var deleteCmd = &cobra.Command{Use: "delete", Short: "Delete resources"}
var deleteHostCmd = &cobra.Command{
    Use:   "host <id|name>",
    Short: "Delete a Zabbix host",
    Args:  cobra.ExactArgs(1),
    RunE:  runDeleteHost,
}
deleteCmd.AddCommand(deleteHostCmd)

// register top-level commands
rootCmd.AddCommand(createCmd)
rootCmd.AddCommand(updateCmd)
rootCmd.AddCommand(deleteCmd)
```

- [ ] **Step 3: Add `runCreateHost`**

```go
func runCreateHost(_ *cobra.Command, _ []string) error {
    c, outFmt, _, err := writeClient()
    if err != nil {
        return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
    }

    if flagCreateHostIP == "" && flagCreateHostDNS == "" {
        return cliPrintErr(outFmt, "provide --ip or --dns", output.ExitError)
    }

    // Resolve group IDs.
    groupIDs := make([]string, 0, len(flagCreateHostGroups))
    for _, g := range flagCreateHostGroups {
        id, err := resources.ResolveGroupID(c, g)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitNotFound)
        }
        groupIDs = append(groupIDs, id)
    }

    ifaceTypeMap := map[string]int{"agent": 1, "snmp": 2, "ipmi": 3, "jmx": 4}
    ifaceType, ok := ifaceTypeMap[flagCreateHostIfaceType]
    if !ok {
        return cliPrintErr(outFmt, "invalid --interface-type; use agent|snmp|ipmi|jmx", output.ExitError)
    }
    useIP := 0
    if flagCreateHostIP != "" {
        useIP = 1
    }

    in := resources.CreateHostInput{
        Host:        flagCreateHostName,
        VisibleName: flagCreateHostVisibleName,
        Description: flagCreateHostDescription,
        GroupIDs:    groupIDs,
        Interface: resources.InterfaceInput{
            Type:          ifaceType,
            UseIP:         useIP,
            IP:            flagCreateHostIP,
            DNS:           flagCreateHostDNS,
            Port:          flagCreateHostPort,
            SNMPVersion:   flagCreateHostSNMPVersion,
            SNMPCommunity: flagCreateHostSNMPCommunity,
        },
    }

    if !confirm(fmt.Sprintf("create host %q?", flagCreateHostName)) {
        fmt.Fprintln(os.Stderr, "cancelled")
        return nil
    }

    host, err := resources.CreateHost(c, in)
    if err != nil {
        return cliPrintErr(outFmt, err.Error(), output.ExitError)
    }

    if outFmt == output.FormatTable {
        fmt.Printf("created host %s (id: %s)\n", host.Host, host.HostID)
        return nil
    }
    return printWriteResult(outFmt, map[string]any{
        "hostid": host.HostID,
        "host":   host.Host,
        "name":   host.Name,
        "status": hostStatusLabel(host.Status),
    })
}
```

- [ ] **Step 4: Add `runUpdateHost`**

```go
func runUpdateHost(_ *cobra.Command, args []string) error {
    c, outFmt, _, err := writeClient()
    if err != nil {
        return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
    }

    // Require at least one change flag.
    if flagUpdateHostVisibleName == "" && flagUpdateHostDescription == "" &&
        len(flagUpdateHostAddGroups) == 0 && len(flagUpdateHostRemoveGroups) == 0 &&
        len(flagUpdateHostAddTemplates) == 0 && len(flagUpdateHostRemoveTemplates) == 0 {
        return cliPrintErr(outFmt, "no update flags provided; nothing to do", output.ExitError)
    }

    hostID, err := resources.ResolveHostID(c, args[0])
    if err != nil {
        return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
    }

    in := resources.UpdateHostInput{
        HostID:      hostID,
        VisibleName: flagUpdateHostVisibleName,
        Description: flagUpdateHostDescription,
    }

    for _, g := range flagUpdateHostAddGroups {
        id, err := resources.ResolveGroupID(c, g)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitNotFound)
        }
        in.AddGroupIDs = append(in.AddGroupIDs, id)
    }
    for _, g := range flagUpdateHostRemoveGroups {
        id, err := resources.ResolveGroupID(c, g)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("group %q: %s", g, err), output.ExitNotFound)
        }
        in.RemoveGroupIDs = append(in.RemoveGroupIDs, id)
    }
    for _, t := range flagUpdateHostAddTemplates {
        id, err := resources.ResolveTemplateID(c, t)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("template %q: %s", t, err), output.ExitNotFound)
        }
        in.AddTemplateIDs = append(in.AddTemplateIDs, id)
    }
    for _, t := range flagUpdateHostRemoveTemplates {
        id, err := resources.ResolveTemplateID(c, t)
        if err != nil {
            return cliPrintErr(outFmt, fmt.Sprintf("template %q: %s", t, err), output.ExitNotFound)
        }
        in.RemoveTemplateIDs = append(in.RemoveTemplateIDs, id)
    }

    if !confirm(fmt.Sprintf("update host %q?", args[0])) {
        fmt.Fprintln(os.Stderr, "cancelled")
        return nil
    }

    host, err := resources.UpdateHost(c, in)
    if err != nil {
        return cliPrintErr(outFmt, err.Error(), output.ExitError)
    }

    if outFmt == output.FormatTable {
        fmt.Printf("updated host %s (id: %s)\n", host.Host, host.HostID)
        return nil
    }
    return printWriteResult(outFmt, map[string]any{
        "hostid": host.HostID,
        "host":   host.Host,
        "name":   host.Name,
        "status": hostStatusLabel(host.Status),
    })
}
```

- [ ] **Step 5: Add `runDeleteHost`**

```go
func runDeleteHost(_ *cobra.Command, args []string) error {
    c, outFmt, _, err := writeClient()
    if err != nil {
        return cliPrintErr(output.Format(flagOutput), err.Error(), output.ExitError)
    }

    host, err := resources.GetHost(c, args[0])
    if err != nil {
        return cliPrintErr(outFmt, err.Error(), output.ExitNotFound)
    }

    if !confirm(fmt.Sprintf("delete host %q (id: %s)?", host.Host, host.HostID)) {
        fmt.Fprintln(os.Stderr, "cancelled")
        return nil
    }

    _, err = resources.DeleteHost(c, host.HostID)
    if err != nil {
        return cliPrintErr(outFmt, err.Error(), output.ExitError)
    }

    if outFmt == output.FormatTable {
        fmt.Printf("deleted host %s (id: %s)\n", host.Host, host.HostID)
        return nil
    }
    return printWriteResult(outFmt, map[string]any{
        "hostid": host.HostID,
        "host":   host.Host,
        "name":   host.Name,
        "status": hostStatusLabel(host.Status),
    })
}
```

- [ ] **Step 6: Build and smoke test**

```bash
go build ./... && ./zbxctl create host --help && ./zbxctl update host --help && ./zbxctl delete host --help
```
Expected: all three help pages render correctly

- [ ] **Step 7: Commit**

```bash
git add internal/cli/write.go
git commit -m "feat: create host, update host, delete host commands"
```

---

## Task 7: LLM manifest entries

**Files:**
- Modify: `internal/cli/llm.go`

- [ ] **Step 1: Add entries to the `capabilities` slice**

Add before the existing `"version"` entry:

```go
{
    Command:     "get history",
    Description: "Fetch time-series metric history. Resolves items from hosts/groups by key pattern, then fetches history. Always includes host and hostid in output.",
    Flags: append(globalFlags(),
        FlagSchema{Name: "item", Type: "string", Description: "Item key pattern or numeric item ID (required).", Required: true, Default: ""},
        FlagSchema{Name: "host", Type: "string", Description: "Host name or ID — repeatable, combinable with --group.", Required: false, Default: ""},
        FlagSchema{Name: "group", Type: "string", Description: "Host group name or ID — repeatable.", Required: false, Default: ""},
        FlagSchema{Name: "since", Type: "string", Description: "Start time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: false, Default: ""},
        FlagSchema{Name: "until", Type: "string", Description: "End time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: false, Default: ""},
        FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
    ),
    OutputFields: []string{"host", "hostid", "itemid", "key_", "clock", "clock_unix", "value"},
    Examples: []string{
        "zbxctl get history --item cpu.load[1] --host web-01",
        "zbxctl get history --item cpu.load[1] --group 'Linux servers' --since 2026-05-01 --output jsonl",
        "zbxctl get history --item cpu.load[1] --host web-01 --host web-02 --since 2026-05-20T00:00:00Z --until 2026-05-21T00:00:00Z",
    },
},
{
    Command:     "get latestdata",
    Description: "Show the most recently collected metric values for one or more hosts or a host group. Always includes host and hostid. Items with no collected data are excluded.",
    Flags: append(globalFlags(),
        FlagSchema{Name: "host", Type: "string", Description: "Host name or ID — repeatable.", Required: false, Default: ""},
        FlagSchema{Name: "group", Type: "string", Description: "Host group name or ID — repeatable.", Required: false, Default: ""},
        FlagSchema{Name: "item", Type: "string", Description: "Filter by item key substring.", Required: false, Default: ""},
        FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
    ),
    OutputFields: []string{"host", "hostid", "itemid", "key_", "name", "value_type", "lastvalue", "lastclock", "lastclock_unix", "units"},
    Examples: []string{
        "zbxctl get latestdata web-01",
        "zbxctl get latestdata --group 'Linux servers' --item cpu --output jsonl",
        "zbxctl get latestdata --host web-01 --host web-02 --item cpu.load[1]",
    },
},
{
    Command:     "create host",
    Description: "Create a new Zabbix host with at least one interface and one host group.",
    Flags: append(globalFlags(),
        FlagSchema{Name: "name", Type: "string", Description: "Technical host name (required).", Required: true, Default: ""},
        FlagSchema{Name: "group", Type: "string", Description: "Host group name or ID (required, repeatable).", Required: true, Default: ""},
        FlagSchema{Name: "ip", Type: "string", Description: "Interface IP address (use with --ip or --dns).", Required: false, Default: ""},
        FlagSchema{Name: "dns", Type: "string", Description: "Interface DNS name.", Required: false, Default: ""},
        FlagSchema{Name: "port", Type: "string", Description: "Interface port.", Required: false, Default: "10050"},
        FlagSchema{Name: "interface-type", Type: "string", Description: "Interface type: agent|snmp|ipmi|jmx.", Required: false, Default: "agent"},
        FlagSchema{Name: "visible-name", Type: "string", Description: "Visible name (defaults to --name).", Required: false, Default: ""},
        FlagSchema{Name: "description", Type: "string", Description: "Host description.", Required: false, Default: ""},
        FlagSchema{Name: "snmp-version", Type: "string", Description: "SNMP version: 1|2|3 (SNMP interface only).", Required: false, Default: "2"},
        FlagSchema{Name: "snmp-community", Type: "string", Description: "SNMP community string (v1/v2c only).", Required: false, Default: "public"},
        FlagSchema{Name: "yes", Type: "bool", Description: "Skip confirmation prompt.", Required: false, Default: false},
    ),
    OutputFields: []string{"hostid", "host", "name", "status"},
    Examples: []string{
        "zbxctl create host --name web-03 --group 'Linux servers' --ip 10.0.0.3 --yes",
        "zbxctl create host --name sw-01 --group 'Network' --ip 10.0.1.1 --interface-type snmp --snmp-community public --port 161 --yes",
    },
},
{
    Command:     "update host",
    Description: "Update a host's visible name, description, group membership, or linked templates.",
    Flags: append(globalFlags(),
        FlagSchema{Name: "visible-name", Type: "string", Description: "New visible name.", Required: false, Default: ""},
        FlagSchema{Name: "description", Type: "string", Description: "New description.", Required: false, Default: ""},
        FlagSchema{Name: "add-group", Type: "string", Description: "Add host to this group (repeatable).", Required: false, Default: ""},
        FlagSchema{Name: "remove-group", Type: "string", Description: "Remove host from this group (repeatable).", Required: false, Default: ""},
        FlagSchema{Name: "add-template", Type: "string", Description: "Link this template (repeatable).", Required: false, Default: ""},
        FlagSchema{Name: "remove-template", Type: "string", Description: "Unlink this template (repeatable).", Required: false, Default: ""},
        FlagSchema{Name: "yes", Type: "bool", Description: "Skip confirmation prompt.", Required: false, Default: false},
    ),
    OutputFields: []string{"hostid", "host", "name", "status"},
    Examples: []string{
        "zbxctl update host web-03 --visible-name 'Web Server 03' --yes",
        "zbxctl update host web-03 --add-group 'Production' --remove-group 'Staging' --yes",
        "zbxctl update host web-03 --add-template 'Template OS Linux' --yes",
    },
},
{
    Command:     "delete host",
    Description: "Permanently delete a Zabbix host and all its data. Requires confirmation.",
    Flags: append(globalFlags(),
        FlagSchema{Name: "yes", Type: "bool", Description: "Skip confirmation prompt.", Required: false, Default: false},
    ),
    OutputFields: []string{"hostid", "host", "name", "status"},
    Examples: []string{
        "zbxctl delete host web-03",
        "zbxctl delete host 10123 --yes",
    },
},
```

Also update the existing `get latestdata` entry (find and replace it) to reflect the new multi-host signature and output fields. Remove the old single-host entry and replace with the one above.

- [ ] **Step 2: Build and verify manifest**

```bash
go build ./... && ./zbxctl llm capabilities | jq '[.[].command]' | grep -E "history|latestdata|create|update|delete"
```
Expected output includes: `"get history"`, `"get latestdata"`, `"create host"`, `"update host"`, `"delete host"`

- [ ] **Step 3: Commit**

```bash
git add internal/cli/llm.go
git commit -m "feat: LLM manifest entries for get history, latestdata (multi-host), and host CRUD"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|---|---|
| `get history` with `--item`, `--host`, `--group`, `--since`, `--until` | Task 4 |
| Two-step API: item.get → history.get | Task 1 |
| Mixed value_types across items: multi-fetch + merge | Task 1 |
| `host`/`hostid` always in output | Task 1, 4, 5 |
| `get latestdata` multi-host/group | Task 5 |
| `--item` canonical flag, `--key` hidden alias | Task 5 |
| `create host` with interface type, SNMP details | Task 3, 6 |
| `update host` fetch-then-modify for groups/templates | Task 3, 6 |
| `delete host` with pre-fetch for output | Task 3, 6 |
| LLM manifest for all commands | Task 7 |

All spec requirements covered.
