package resources

import (
	"encoding/json"
	"fmt"

	"github.com/mwtrigg/zbxctl/internal/api"
)

// AuditActions and AuditResources name the codes seen in the audit log. Only
// codes confirmed against a live 7.4 server are named; the rest are shown as
// "action N" / "resource N" rather than guessed.
var (
	AuditActions   = map[string]string{"0": "add", "1": "update", "2": "delete", "3": "login", "4": "logout", "5": "execute", "12": "push"}
	AuditResources = map[string]string{"0": "user", "4": "host", "11": "usergroup", "13": "trigger", "14": "hostgroup", "15": "item", "27": "maintenance", "29": "macro", "30": "template"}
)

// AuditEntry is one audit log record.
type AuditEntry struct {
	ID, Clock, User, IP, Action, ResourceType, ResourceID, ResourceName, Details string
}

// AuditQuery narrows the audit log.
type AuditQuery struct {
	Users         []string
	ActionCodes   []string
	ResourceCodes []string
	From, Till    int64
	Search        string // substring of the resource name
	Limit         int
}

// GetAuditLog returns audit entries, newest first.
func GetAuditLog(c *api.Client, q AuditQuery) ([]AuditEntry, error) {
	params := map[string]any{
		"output":    []string{"auditid", "clock", "username", "ip", "action", "resourcetype", "resourceid", "resourcename", "details"},
		"sortfield": "clock", "sortorder": "DESC",
	}
	filter := map[string]any{}
	if len(q.Users) > 0 {
		filter["username"] = q.Users
	}
	if len(q.ActionCodes) > 0 {
		filter["action"] = q.ActionCodes
	}
	if len(q.ResourceCodes) > 0 {
		filter["resourcetype"] = q.ResourceCodes
	}
	if len(filter) > 0 {
		params["filter"] = filter
	}
	if q.Search != "" {
		params["search"] = map[string]any{"resourcename": q.Search}
	}
	if q.From > 0 {
		params["time_from"] = q.From
	}
	if q.Till > 0 {
		params["time_till"] = q.Till
	}
	if q.Limit > 0 {
		params["limit"] = q.Limit
	}
	raw, err := c.CallList("auditlog.get", params)
	if err != nil {
		return nil, fmt.Errorf("auditlog.get: %w", err)
	}
	var tmp []map[string]any
	if err := json.Unmarshal(raw, &tmp); err != nil {
		return nil, fmt.Errorf("parsing audit log: %w", err)
	}
	out := make([]AuditEntry, len(tmp))
	for i, m := range tmp {
		s := func(k string) string { v, _ := m[k].(string); return v }
		out[i] = AuditEntry{ID: s("auditid"), Clock: s("clock"), User: s("username"), IP: s("ip"), Action: s("action"),
			ResourceType: s("resourcetype"), ResourceID: s("resourceid"), ResourceName: s("resourcename"), Details: s("details")}
	}
	return out, nil
}
