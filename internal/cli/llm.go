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
		Command:     "auth status",
		Description: "Show current authentication state and API connectivity.",
		Flags:       globalFlags(),
		OutputFields: []string{"url", "context", "auth_via", "connected", "api_version"},
		Examples:    []string{"zbxctl auth status", "zbxctl auth status --output json"},
	},
	{
		Command:     "context list",
		Description: "List all contexts defined in the config file.",
		Flags:       globalFlags(),
		OutputFields: []string{"name", "url", "current"},
		Examples:    []string{"zbxctl context list"},
	},
	{
		Command:     "context use",
		Description: "Switch the active context.",
		Flags:       globalFlags(),
		OutputFields: []string{},
		Examples:    []string{"zbxctl context use homelab"},
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
		Command:     "get host",
		Description: "Show full detail for a single host by numeric ID or technical name.",
		Flags:       globalFlags(),
		OutputFields: []string{"hostid", "host", "name", "status", "status_raw", "available", "available_raw", "description"},
		Examples: []string{
			"zbxctl get host 10001",
			"zbxctl get host web-server-01 --output json",
		},
	},
	{
		Command:     "get items",
		Description: "List items (metrics) for a host. Host argument accepts ID or technical name.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "key", Type: "string", Description: "Substring search on item key.", Required: false, Default: ""},
			FlagSchema{Name: "status", Type: "string", Description: "Filter by status: enabled|disabled.", Required: false, Default: ""},
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"itemid", "hostid", "name", "key_", "type", "value_type", "status", "status_raw", "lastvalue", "lastclock", "lastclock_unix", "units", "delay"},
		Examples: []string{
			"zbxctl get items web-server-01",
			"zbxctl get items 10001 --key cpu --status enabled --output jsonl",
		},
	},
	{
		Command:     "get latestdata",
		Description: "Show the most recently collected metric values for a host. Items with no collected data are excluded.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"itemid", "hostid", "name", "key_", "value_type", "lastvalue", "lastclock", "lastclock_unix", "units"},
		Examples: []string{
			"zbxctl get latestdata web-server-01",
			"zbxctl get latestdata 10001 --output jsonl",
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
		Command:     "get template",
		Description: "Show detail for a single template including item, trigger, and graph counts.",
		Flags:       globalFlags(),
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
		Examples:    []string{"zbxctl get groups", "zbxctl get groups --output jsonl"},
	},
	{
		Command:     "get users",
		Description: "List Zabbix users.",
		Flags: append(globalFlags(),
			FlagSchema{Name: "limit", Type: "int", Description: "Maximum results.", Required: false, Default: 0},
		),
		OutputFields: []string{"userid", "username", "name", "surname", "roleid", "type", "type_raw"},
		Examples:    []string{"zbxctl get users", "zbxctl get users --output jsonl"},
	},
	{
		Command:     "get user",
		Description: "Show detail for a single user by numeric ID or username.",
		Flags:       globalFlags(),
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
		Examples:    []string{"zbxctl get usergroups", "zbxctl get usergroups --output jsonl"},
	},
	{
		Command:     "llm capabilities",
		Description: "Print the full command manifest as a JSON array. No auth required.",
		Flags:       []FlagSchema{},
		OutputFields: []string{},
		Examples:    []string{"zbxctl llm capabilities"},
	},
	{
		Command:     "llm schema",
		Description: "Print the schema for a specific command. No auth required.",
		Flags:       []FlagSchema{},
		OutputFields: []string{},
		Examples:    []string{"zbxctl llm schema 'get hosts'"},
	},
}

// globalFlags returns the flags that appear on every command.
func globalFlags() []FlagSchema {
	return []FlagSchema{
		{Name: "output", Type: "string", Description: "Output format: table|json|jsonl. Defaults to table on TTY, jsonl otherwise.", Required: false, Default: "table"},
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
