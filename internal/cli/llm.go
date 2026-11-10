package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// CommandSchema describes a single zbxctl command for agent consumption.
type CommandSchema struct {
	Command      string       `json:"command"`
	Description  string       `json:"description"`
	Flags        []FlagSchema `json:"flags"`
	OutputFields []string     `json:"output_fields"`
	Examples     []string     `json:"examples"`
	Dangerous    bool         `json:"dangerous,omitempty"`
}

// FlagSchema describes a single flag.
type FlagSchema struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     any    `json:"default"`
}

// capabilities is the compiled-in manifest of all commands.
// Must be kept in sync whenever commands or flags change.
var capabilities = []CommandSchema{
	{
		Command:      "auth status",
		Description:  "Show current authentication state and API connectivity.",
		Flags:        globalFlags(),
		OutputFields: []string{"url", "context", "auth_via", "connected", "api_version"},
		Examples:     []string{"zbxctl auth status", "zbxctl auth status --output json"},
	},
	{
		Command:      "context list",
		Description:  "List all contexts defined in the config file.",
		Flags:        globalFlags(),
		OutputFields: []string{"name", "url", "current"},
		Examples:     []string{"zbxctl context list"},
	},
	{
		Command:      "context use",
		Description:  "Switch the active context.",
		Flags:        globalFlags(),
		OutputFields: []string{},
		Examples:     []string{"zbxctl context use homelab"},
	},
	{
		Command:     "get hosts",
		Description: "List Zabbix hosts. Supports filtering by group, status, and substring search.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "group", Type: "string", Description: "Filter by host group name (exact).", Required: false, Default: ""},
			FlagSchema{Name: "status", Type: "string", Description: "Filter by status: monitored|unmonitored.", Required: false, Default: ""},
			FlagSchema{Name: "search", Type: "string", Description: "Substring search on technical name and visible name.", Required: false, Default: ""},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results. Defaults to context default_limit (100).", Required: false, Default: 0},
			FlagSchema{Name: "sort-by", Type: "string", Description: "Sort field: host|name|hostid.", Required: false, Default: "host"},
		),
		OutputFields: []string{"hostid", "host", "name", "status", "status_raw", "available", "available_raw", "description"},
		Examples: []string{
			"zbxctl get hosts",
			"zbxctl get hosts --group 'Linux servers' --status monitored",
			"zbxctl get hosts --search web --output jsonl",
		},
	},
	{
		Command:      "get host",
		Description:  "Show full detail for a single host by numeric ID or technical name.",
		Flags:        globalFlags(),
		OutputFields: []string{"hostid", "host", "name", "status", "status_raw", "available", "available_raw", "description"},
		Examples: []string{
			"zbxctl get host 10001",
			"zbxctl get host web-server-01 --output json",
		},
	},
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
			"zbxctl get items 10001 --key cpu --status enabled --output jsonl",
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
		Command:     "get triggers",
		Description: "List triggers. Excludes template triggers by default. Filter by host, minimum severity, status, and problem state.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "host", Type: "string", Description: "Filter by host name or ID.", Required: false, Default: ""},
			FlagSchema{Name: "severity", Type: "string", Description: "Minimum severity: 0-5 or not_classified|information|warning|average|high|disaster.", Required: false, Default: ""},
			FlagSchema{Name: "status", Type: "string", Description: "Filter by status: enabled|disabled.", Required: false, Default: ""},
			FlagSchema{Name: "problem", Type: "bool", Description: "Only show triggers currently in PROBLEM state.", Required: false, Default: false},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
			FlagSchema{Name: "sort-by", Type: "string", Description: "Sort field: priority|lastchange|description.", Required: false, Default: "priority"},
		),
		OutputFields: []string{"triggerid", "host", "description", "priority", "priority_raw", "value", "value_raw", "status", "status_raw", "lastchange", "lastchange_unix", "hosts"},
		Examples: []string{
			"zbxctl get triggers",
			"zbxctl get triggers --problem",
			"zbxctl get triggers --host web-server-01 --severity high",
			"zbxctl get triggers --status enabled --output jsonl",
		},
	},
	{
		Command:     "get problems",
		Description: "List problems. Defaults to open (unresolved) problems only. Use --resolved to include recently-resolved problems.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "host", Type: "string", Description: "Filter by host name or ID.", Required: false, Default: ""},
			FlagSchema{Name: "severity", Type: "string", Description: "Minimum severity: 0-5 or not_classified|information|warning|average|high|disaster.", Required: false, Default: ""},
			FlagSchema{Name: "since", Type: "string", Description: "Only problems after this time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: false, Default: ""},
			FlagSchema{Name: "acknowledged", Type: "string", Description: "Filter by acknowledgement: true=acknowledged only, false=unacknowledged only.", Required: false, Default: ""},
			FlagSchema{Name: "suppressed", Type: "string", Description: "true = only suppressed problems, false = only unsuppressed (default both).", Required: false, Default: ""},
			FlagSchema{Name: "maintenance", Type: "bool", Description: "Only problems suppressed by a maintenance window.", Required: false, Default: false},
			FlagSchema{Name: "exclude-orphans", Type: "bool", Description: "Drop problems that can never recover: trigger disabled, all hosts disabled, or all items disabled.", Required: false, Default: false},
			FlagSchema{Name: "group-by", Type: "string", Description: "name: one row per alert name (host names normalized) with count, hosts, first/last time and the event IDs.", Required: false, Default: ""},
			FlagSchema{Name: "resolved", Type: "bool", Description: "Include recently-resolved problems (default: open only).", Required: false, Default: false},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"eventid", "objectid", "name", "severity", "severity_raw", "status", "acknowledged", "acknowledged_raw", "clock", "clock_unix", "r_eventid", "r_clock", "r_clock_unix"},
		Examples: []string{
			"zbxctl get problems",
			"zbxctl get problems --severity high --acknowledged false",
			"zbxctl get problems --resolved --limit 20",
			"zbxctl get problems --host web-server-01 --since 2026-01-01 --output jsonl",
		},
	},
	{
		Command:     "get events",
		Description: "List trigger events (both problem and recovery). Filter by host and time range.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "host", Type: "string", Description: "Filter by host name or ID.", Required: false, Default: ""},
			FlagSchema{Name: "since", Type: "string", Description: "Events after this time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: false, Default: ""},
			FlagSchema{Name: "until", Type: "string", Description: "Events before this time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: false, Default: ""},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"eventid", "objectid", "host", "name", "value", "value_raw", "acknowledged", "acknowledged_raw", "clock", "clock_unix", "hosts"},
		Examples: []string{
			"zbxctl get events --host web-server-01",
			"zbxctl get events --since 2026-01-01 --until 2026-01-02 --output jsonl",
		},
	},
	{
		Command:     "get templates",
		Description: "List templates. Supports substring search.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "search", Type: "string", Description: "Substring search on template name.", Required: false, Default: ""},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"templateid", "host", "name", "description", "items", "triggers", "graphs"},
		Examples: []string{
			"zbxctl get templates",
			"zbxctl get templates --search Linux --output jsonl",
		},
	},
	{
		Command:      "get template",
		Description:  "Show detail for a single template including item, trigger, and graph counts.",
		Flags:        globalFlags(),
		OutputFields: []string{"templateid", "host", "name", "description", "items", "triggers", "graphs"},
		Examples: []string{
			"zbxctl get template 'Template OS Linux'",
			"zbxctl get template 10050 --output json",
		},
	},
	{
		Command:     "get groups",
		Description: "List host groups.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"groupid", "name"},
		Examples:     []string{"zbxctl get groups", "zbxctl get groups --output jsonl"},
	},
	{
		Command:     "get users",
		Description: "List Zabbix users.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"userid", "username", "name", "surname", "roleid", "type", "type_raw"},
		Examples:     []string{"zbxctl get users", "zbxctl get users --output jsonl"},
	},
	{
		Command:      "get user",
		Description:  "Show detail for a single user by numeric ID or username.",
		Flags:        globalFlags(),
		OutputFields: []string{"userid", "username", "name", "surname", "roleid", "type", "type_raw"},
		Examples: []string{
			"zbxctl get user Admin",
			"zbxctl get user 1 --output json",
		},
	},
	{
		Command:     "get usergroups",
		Description: "List user groups.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"usrgrpid", "name", "gui_access", "gui_access_raw", "users_status", "users_status_raw"},
		Examples:     []string{"zbxctl get usergroups", "zbxctl get usergroups --output jsonl"},
	},
	{
		Command:     "enable host",
		Description: "Enable a host (set status to monitored). Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"hostid", "host", "name", "status"},
		Examples: []string{
			"zbxctl enable host web-server-01",
			"zbxctl enable host 10001 --apply",
		},
	},
	{
		Command:     "disable host",
		Description: "Disable a host (set status to unmonitored). Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"hostid", "host", "name", "status"},
		Examples: []string{
			"zbxctl disable host web-server-01",
			"zbxctl disable host 10001 --apply",
		},
	},
	{
		Command:     "enable trigger",
		Description: "Enable a trigger by numeric ID. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"triggerid", "host", "description", "status"},
		Examples: []string{
			"zbxctl enable trigger 12345",
			"zbxctl enable trigger 12345 --apply",
		},
	},
	{
		Command:     "disable trigger",
		Description: "Disable a trigger by numeric ID. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"triggerid", "host", "description", "status"},
		Examples: []string{
			"zbxctl disable trigger 12345",
			"zbxctl disable trigger 12345 --apply",
		},
	},
	{
		Command:     "acknowledge",
		Description: "Acknowledge a Zabbix event by event ID. Optionally add a message, close the problem, change its severity, or instead unacknowledge, suppress or unsuppress it.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "message", Type: "string", Description: "Acknowledgement message.", Required: false, Default: ""},
			FlagSchema{Name: "close", Type: "bool", Description: "Also close the problem.", Required: false, Default: false},
			FlagSchema{Name: "unacknowledge", Type: "bool", Description: "Remove the acknowledgement instead.", Required: false, Default: false},
			FlagSchema{Name: "suppress", Type: "bool", Description: "Suppress the problem (hide it from the problem list).", Required: false, Default: false},
			FlagSchema{Name: "until", Type: "string", Description: "With --suppress: suppress until this time (unix, RFC3339, YYYY-MM-DD, 24h, 7d). Default: indefinitely.", Required: false, Default: ""},
			FlagSchema{Name: "unsuppress", Type: "bool", Description: "Lift a suppression.", Required: false, Default: false},
			FlagSchema{Name: "severity", Type: "string", Description: "Also change the problem severity (0-5 or name).", Required: false, Default: ""},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"eventid", "acknowledged", "closed", "message"},
		Examples: []string{
			"zbxctl acknowledge 208317877",
			"zbxctl acknowledge 208317877 --message 'investigating' --apply",
			"zbxctl acknowledge 208317877 --close --apply",
		},
	},
	{
		Command:     "maintenance list",
		Description: "List maintenance windows.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"maintenanceid", "name", "active_since", "active_since_unix", "active_till", "active_till_unix", "description", "maintenance_type"},
		Examples:     []string{"zbxctl maintenance list", "zbxctl maintenance list --output jsonl"},
	},
	{
		Command:     "maintenance create",
		Description: "Create a maintenance window. Requires --name, --start, and --duration. Provide --host or --group (or both).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "name", Type: "string", Description: "Maintenance window name.", Required: true, Default: ""},
			FlagSchema{Name: "host", Type: "string", Description: "Host name or ID.", Required: false, Default: ""},
			FlagSchema{Name: "group", Type: "string", Description: "Host group name.", Required: false, Default: ""},
			FlagSchema{Name: "start", Type: "string", Description: "Start time: unix timestamp, RFC3339, or YYYY-MM-DD.", Required: true, Default: ""},
			FlagSchema{Name: "duration", Type: "int", Description: "Duration in minutes.", Required: true, Default: 0},
			FlagSchema{Name: "description", Type: "string", Description: "Optional description.", Required: false, Default: ""},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"maintenanceid", "name", "active_since", "active_till", "active_since_unix", "active_till_unix"},
		Examples: []string{
			"zbxctl maintenance create --name 'Patch window' --host web-server-01 --start 2026-06-01T02:00:00Z --duration 120 --apply",
			"zbxctl maintenance create --name 'Downtime' --group 'Linux servers' --start 1748736000 --duration 60 --apply",
		},
	},
	{
		Command:     "maintenance delete",
		Description: "Delete a maintenance window by numeric ID or exact name.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"maintenanceid", "name", "status"},
		Examples: []string{
			"zbxctl maintenance delete 'Patch window'",
			"zbxctl maintenance delete 42 --apply",
		},
	},
	{
		Command:     "get history",
		Description: "Fetch time-series metric history. Resolves items from hosts/groups by key pattern, then fetches history. Use --around-event to automatically anchor the time window to a specific event. Always includes host and hostid in output.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "item", Type: "string", Description: "Item key pattern or numeric item ID (required unless using --around-event).", Required: false, Default: ""},
			FlagSchema{Name: "host", Type: "string", Description: "Host name or ID — repeatable, combinable with --group.", Required: false, Default: ""},
			FlagSchema{Name: "group", Type: "string", Description: "Host group name or ID — repeatable.", Required: false, Default: ""},
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
	{
		Command:     "create host",
		Description: "Create a new Zabbix host with at least one interface and one host group.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "name", Type: "string", Description: "Technical host name (required).", Required: true, Default: ""},
			FlagSchema{Name: "group", Type: "string", Description: "Host group name or ID (required, repeatable).", Required: true, Default: ""},
			FlagSchema{Name: "ip", Type: "string", Description: "Interface IP address (provide --ip or --dns).", Required: false, Default: ""},
			FlagSchema{Name: "dns", Type: "string", Description: "Interface DNS name.", Required: false, Default: ""},
			FlagSchema{Name: "port", Type: "string", Description: "Interface port.", Required: false, Default: "10050"},
			FlagSchema{Name: "interface-type", Type: "string", Description: "Interface type: agent|snmp|ipmi|jmx.", Required: false, Default: "agent"},
			FlagSchema{Name: "visible-name", Type: "string", Description: "Visible name (defaults to --name).", Required: false, Default: ""},
			FlagSchema{Name: "description", Type: "string", Description: "Host description.", Required: false, Default: ""},
			FlagSchema{Name: "snmp-version", Type: "string", Description: "SNMP version: 1|2|3 (SNMP interface only).", Required: false, Default: "2"},
			FlagSchema{Name: "snmp-community", Type: "string", Description: "SNMP community string (v1/v2c only).", Required: false, Default: "public"},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"hostid", "host", "name", "status"},
		Examples: []string{
			"zbxctl create host --name web-03 --group 'Linux servers' --ip 10.0.0.3 --apply",
			"zbxctl create host --name sw-01 --group 'Network' --ip 10.0.1.1 --interface-type snmp --snmp-community public --port 161 --apply",
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
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"hostid", "host", "name", "status"},
		Examples: []string{
			"zbxctl update host web-03 --visible-name 'Web Server 03' --apply",
			"zbxctl update host web-03 --add-group 'Production' --remove-group 'Staging' --apply",
			"zbxctl update host web-03 --add-template 'Template OS Linux' --apply",
		},
	},
	{
		Command:     "tag host",
		Description: "Add a tag to a host; existing tags are kept and an identical tag=value pair is a no-op. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "tag", Type: "string", Description: "Tag name.", Required: true, Default: ""},
			FlagSchema{Name: "value", Type: "string", Description: "Tag value (default empty).", Required: false, Default: ""},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"hostid", "host", "changed", "tags"},
		Examples:     []string{"zbxctl tag host web-01 --tag env --value prod --apply"},
	},
	{
		Command:     "untag host",
		Description: "Remove tags from a host: every tag with --tag's name, or only the exact pair when --value is given. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "tag", Type: "string", Description: "Tag name to remove.", Required: true, Default: ""},
			FlagSchema{Name: "value", Type: "string", Description: "Remove only the pair with this value.", Required: false, Default: ""},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"hostid", "host", "changed", "tags"},
		Examples:     []string{"zbxctl untag host web-01 --tag env --apply"},
	},
	{
		Command:     "create user",
		Description: "Create a user. The initial password comes from --password-file or --password-stdin, never from a flag value. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "username", Type: "string", Description: "Login name.", Required: true, Default: ""},
			FlagSchema{Name: "password-file", Type: "string", Description: "File holding the initial password.", Required: false, Default: ""},
			FlagSchema{Name: "password-stdin", Type: "bool", Description: "Read the initial password from stdin.", Required: false, Default: false},
			FlagSchema{Name: "role", Type: "string", Description: "Role name or ID.", Required: true, Default: ""},
			FlagSchema{Name: "group", Type: "stringArray", Description: "User group name or ID (repeatable).", Required: true, Default: []string{}},
			FlagSchema{Name: "name", Type: "string", Description: "First name.", Required: false, Default: ""},
			FlagSchema{Name: "surname", Type: "string", Description: "Surname.", Required: false, Default: ""},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"userid", "username", "role", "groups"},
		Examples:     []string{"zbxctl create user --username bob --password-file ./pw --role 'User role' --group Operators --apply"},
	},
	{
		Command:     "delete user",
		Description: "Delete a user. Refuses the Admin user and the user that owns the credentials in use. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"userid", "username", "status"},
		Examples:     []string{"zbxctl delete user bob --apply"},
	},
	{
		Command:     "create usergroup",
		Description: "Create a user group. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "name", Type: "string", Description: "Group name.", Required: true, Default: ""},
			FlagSchema{Name: "gui-access", Type: "string", Description: "Frontend access: default, internal, ldap or disabled.", Required: false, Default: "default"},
			FlagSchema{Name: "disabled", Type: "bool", Description: "Create the group disabled.", Required: false, Default: false},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"usrgrpid", "name", "gui_access", "disabled"},
		Examples:     []string{"zbxctl create usergroup --name Operators --apply"},
	},
	{
		Command:     "delete usergroup",
		Description: "Delete a user group. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"usrgrpid", "name", "status"},
		Examples:     []string{"zbxctl delete usergroup Operators --apply"},
	},
	{
		Command:     "export template",
		Description: "Export a template with configuration.export. Prints the document to stdout (not JSON-wrapped) unless --file is given.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "format", Type: "string", Description: "yaml, json or xml.", Required: false, Default: "yaml"},
			FlagSchema{Name: "file", Type: "string", Description: "Write to this file instead of stdout.", Required: false, Default: ""},
		),
		OutputFields: []string{"name", "file", "format", "bytes"},
		Examples:     []string{"zbxctl export template web-01 --file web-01.yaml"},
	},
	{
		Command:     "diff template",
		Description: "Show what importing an export file of a template would change, without changing anything (configuration.importcompare), as a flat list of created/updated/deleted objects.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "format", Type: "string", Description: "yaml, json or xml (default: from the file extension).", Required: false, Default: ""},
		),
		OutputFields: []string{"action", "path", "fields"},
		Examples:     []string{"zbxctl diff template web-01.yaml"},
	},
	{
		Command:     "import template",
		Description: "Import a template export file. Without --apply it only shows the diff, like diff template. Never deletes objects missing from the file. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "format", Type: "string", Description: "yaml, json or xml (default: from the file extension).", Required: false, Default: ""},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"action", "path", "fields", "applied"},
		Examples:     []string{"zbxctl import template web-01.yaml --apply"},
	},
	{
		Command:     "export host",
		Description: "Export a host with configuration.export. Prints the document to stdout (not JSON-wrapped) unless --file is given.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "format", Type: "string", Description: "yaml, json or xml.", Required: false, Default: "yaml"},
			FlagSchema{Name: "file", Type: "string", Description: "Write to this file instead of stdout.", Required: false, Default: ""},
		),
		OutputFields: []string{"name", "file", "format", "bytes"},
		Examples:     []string{"zbxctl export host web-01 --file web-01.yaml"},
	},
	{
		Command:     "diff host",
		Description: "Show what importing an export file of a host would change, without changing anything (configuration.importcompare), as a flat list of created/updated/deleted objects.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "format", Type: "string", Description: "yaml, json or xml (default: from the file extension).", Required: false, Default: ""},
		),
		OutputFields: []string{"action", "path", "fields"},
		Examples:     []string{"zbxctl diff host web-01.yaml"},
	},
	{
		Command:     "import host",
		Description: "Import a host export file. Without --apply it only shows the diff, like diff host. Never deletes objects missing from the file. Dry run unless --apply is given (--yes is an alias).",
		Flags: append(globalFlags(),
			FlagSchema{Name: "format", Type: "string", Description: "yaml, json or xml (default: from the file extension).", Required: false, Default: ""},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"action", "path", "fields", "applied"},
		Examples:     []string{"zbxctl import host web-01.yaml --apply"},
	},
	{
		Command:     "delete host",
		Description: "Permanently delete a Zabbix host and all its data. Requires confirmation.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"hostid", "host", "name", "status"},
		Examples: []string{
			"zbxctl delete host web-03",
			"zbxctl delete host 10123 --apply",
		},
	},
	{
		Command:      "version",
		Description:  "Print the current version, OS, and architecture. No auth required.",
		Flags:        globalFlags(),
		OutputFields: []string{"version", "os", "arch"},
		Examples:     []string{"zbxctl version", "zbxctl version --output json"},
	},
	{
		Command:     "upgrade",
		Description: "Self-update zbxctl to the latest GitHub release. Fetches release metadata, compares versions, and atomically replaces the binary.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "check", Type: "bool", Description: "Report available version without installing.", Required: false, Default: false},
			FlagSchema{Name: "apply", Type: "bool", Description: "Execute the change. Without it the command only prints what it would do and changes nothing (--yes is an alias).", Required: false, Default: false},
		),
		OutputFields: []string{"current_version", "latest_version", "updated"},
		Examples: []string{
			"zbxctl upgrade",
			"zbxctl upgrade --check --output json",
			"zbxctl upgrade --apply",
		},
	},
	{
		Command:      "llm capabilities",
		Description:  "Print the full command manifest as a JSON array. No auth required.",
		Flags:        []FlagSchema{},
		OutputFields: []string{},
		Examples:     []string{"zbxctl llm capabilities"},
	},
	{
		Command:      "llm schema",
		Description:  "Print the schema for a specific command. No auth required.",
		Flags:        []FlagSchema{},
		OutputFields: []string{},
		Examples:     []string{"zbxctl llm schema 'get hosts'"},
	},
	{
		Command:     "get scripts",
		Description: "List Zabbix scripts visible to the authenticated user. Use --host to filter to scripts applicable to a specific host.",
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
		Command:     "diagnose trigger",
		Description: "Explain why a problem is open and whether it can recover: disabled trigger, host or items, unsupported items, suppression or maintenance, manual close off. Pass a trigger ID, or an event ID with --event. Always JSON or JSONL.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "event", Type: "bool", Description: "The argument is an event ID; diagnose that event's trigger.", Required: false, Default: false},
		),
		OutputFields: []string{"trigger{triggerid,description,severity,hosts,enabled,state,manual_close}", "open_problems[]", "items[]", "reasons[]", "recoverable", "meta{generated_at}"},
		Examples:     []string{"zbxctl diagnose trigger 13472", "zbxctl diagnose trigger 126 --event"},
	},
	{
		Command:     "diagnose host",
		Description: "Single-call composite snapshot for agent-driven triage. Returns open problems, recent events, latest metric values, broken items, and interface availability. Always JSON or JSONL.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "since", Type: "string", Description: "Event window before now (e.g. 1h, 6h, 24h). Default: 24h.", Required: false, Default: "24h"},
		),
		OutputFields: []string{"host{hostid,host,name,status,available,iface_available}", "open_problems[]", "recent_events[]", "latestdata[]", "broken_items[]", "meta{host,since,generated_at}"},
		Examples: []string{
			"zbxctl diagnose host web-server-01 --output json",
			"zbxctl diagnose host 10001 --since 6h --output jsonl",
		},
	},
}

// globalFlags returns the flags that appear on every command.
func globalFlags() []FlagSchema {
	return []FlagSchema{
		{Name: "output", Type: "string", Description: "Output format: json|jsonl|table. Default json.", Required: false, Default: "json"},
		{Name: "context", Type: "string", Description: "Override the active context.", Required: false, Default: ""},
		{Name: "insecure", Type: "bool", Description: "Disable TLS verification (emits a warning).", Required: false, Default: false},
	}
}

// ── commands ──────────────────────────────────────────────────────────────────

var llmCmd = &cobra.Command{
	Use:   "llm",
	Short: "Agent discoverability commands",
}

var llmCapabilitiesCmd = &cobra.Command{
	Use:   "capabilities",
	Short: "Print full command manifest (JSON)",
	RunE:  runLLMCapabilities,
}

var llmSchemaCmd = &cobra.Command{
	Use:   "schema <command>",
	Short: "Print schema for a specific command",
	Args:  cobra.ExactArgs(1),
	RunE:  runLLMSchema,
}

func init() {
	llmCmd.AddCommand(llmCapabilitiesCmd)
	llmCmd.AddCommand(llmSchemaCmd)
}

func runLLMCapabilities(_ *cobra.Command, _ []string) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(capabilities)
}

func runLLMSchema(_ *cobra.Command, args []string) error {
	query := args[0]
	for _, cap := range capabilities {
		if cap.Command == query {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(cap)
		}
	}
	fmt.Fprintf(os.Stderr, "error: command %q not found in capabilities manifest\n", query)
	return &cliErr{code: 2}
}
