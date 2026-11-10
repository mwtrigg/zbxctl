package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwtrigg/zbxctl/internal/version"
)

// zbxctl mcp serves the commands as MCP tools over stdio, so an agent can call
// them without a shell. Each call runs zbxctl itself with the arguments the
// agent gave, so the server's config file and environment supply the Zabbix URL
// and token: they never pass through the agent.

var mcpAllowWrites bool

// Flags the MCP server never offers. File-path flags would let an agent read or
// write files on this machine; --insecure and --allow-unsupported are policy
// for whoever configured the server.
var mcpHiddenFlags = map[string]bool{
	"file": true, "dir": true, "id-file": true, "value-file": true, "password-file": true, "params-file": true, "state-dir": true,
	"insecure": true, "allow-unsupported": true, "yes": true, "output": true, "raw": true, "no-state": true,
}

// Commands never offered as tools.
var mcpHiddenCommands = map[string]string{
	"run script":  "needs a human to confirm a one-time token",
	"upgrade":     "replaces the binary",
	"context use": "changes the config file",
	"version":     "",
	"state prune": "deletes local files",
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`

	command []string // the words of the command, e.g. ["get", "hosts"]
	flags   map[string]FlagSchema
	write   bool
}

func isWriteCommand(c CommandSchema) bool {
	for _, f := range c.Flags {
		if f.Name == "apply" {
			return true
		}
	}
	return false
}

// buildMCPTools turns the manifest into tools. Without allowWrites only
// commands that cannot change anything are offered.
func buildMCPTools(allowWrites bool) []mcpTool {
	var tools []mcpTool
	for _, c := range capabilities {
		if _, hidden := mcpHiddenCommands[c.Command]; hidden || strings.HasPrefix(c.Command, "llm ") || strings.HasPrefix(c.Command, "export ") || strings.HasPrefix(c.Command, "import ") || strings.HasPrefix(c.Command, "diff ") || strings.HasPrefix(c.Command, "state prune") {
			continue
		}
		write := isWriteCommand(c)
		if write && !allowWrites {
			continue
		}
		props := map[string]any{
			"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "Positional arguments of the command: the host, ID or name it works on. Examples: " + strings.Join(c.Examples, " | ")},
		}
		var required []string
		flags := map[string]FlagSchema{}
		for _, f := range c.Flags {
			if mcpHiddenFlags[f.Name] {
				continue
			}
			item := map[string]any{"description": f.Description}
			switch f.Type {
			case "bool":
				item["type"] = "boolean"
			case "int":
				item["type"] = "integer"
			case "stringArray":
				item["type"], item["items"] = "array", map[string]any{"type": "string"}
			default:
				item["type"] = "string"
			}
			props[f.Name] = item
			flags[f.Name] = f
			if f.Required {
				required = append(required, f.Name)
			}
		}
		sort.Strings(required)
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		desc := c.Description
		if write {
			desc += " This changes things: without apply=true it only shows what it would do."
		}
		tools = append(tools, mcpTool{
			Name: "zbxctl_" + strings.ReplaceAll(c.Command, " ", "_"), Description: desc, InputSchema: schema,
			command: strings.Fields(c.Command), flags: flags, write: write,
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// argsFor builds the zbxctl argument list for a tool call.
func (t mcpTool) argsFor(in map[string]any) ([]string, error) {
	args := append([]string{}, t.command...)
	if raw, ok := in["args"]; ok {
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("args must be an array of strings")
		}
		for _, a := range list {
			s, ok := a.(string)
			if !ok {
				return nil, fmt.Errorf("args must be an array of strings")
			}
			args = append(args, s)
		}
	}
	names := make([]string, 0, len(in))
	for k := range in {
		if k != "args" {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		f, ok := t.flags[k]
		if !ok {
			return nil, fmt.Errorf("unknown argument %q for %s", k, t.Name)
		}
		v := in[k]
		switch f.Type {
		case "bool":
			if b, _ := v.(bool); b {
				args = append(args, "--"+k)
			}
		case "stringArray":
			list, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an array of strings", k)
			}
			for _, x := range list {
				args = append(args, "--"+k, fmt.Sprint(x))
			}
		default:
			args = append(args, "--"+k, fmt.Sprint(v))
		}
	}
	return append(args, "--output", "json"), nil
}

// mcpRunner runs zbxctl with args and returns its output; it is replaced in tests.
var mcpRunner = func(ctx context.Context, args []string) (stdout, stderr string, code int) {
	exe, err := os.Executable()
	if err != nil {
		return "", err.Error(), 1
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Env = append(os.Environ(), "ZBXCTL_FROM_MCP=1")
	err = cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), errb.String(), ee.ExitCode()
	}
	if err != nil {
		return out.String(), err.Error(), 1
	}
	return out.String(), errb.String(), 0
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
}

func rpcError(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": msg}}
}

// serveMCP reads newline-delimited JSON-RPC from in and answers on out until in closes.
func serveMCP(in io.Reader, out io.Writer, allowWrites bool) error {
	tools := buildMCPTools(allowWrites)
	byName := map[string]mcpTool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg rpcMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			_ = enc.Encode(rpcError(json.RawMessage("null"), -32700, "parse error"))
			continue
		}
		isNotification := len(msg.ID) == 0
		reply := func(v map[string]any) {
			if !isNotification {
				_ = enc.Encode(v)
			}
		}
		switch msg.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			ver := p.ProtocolVersion
			if ver == "" {
				ver = "2024-11-05"
			}
			reply(rpcResult(msg.ID, map[string]any{
				"protocolVersion": ver,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "zbxctl", "version": version.Version},
				"instructions":    "Zabbix tools. Reads run directly. " + map[bool]string{true: "Writes are enabled but every one is a dry run until you pass apply=true.", false: "This server is read-only: write tools are not offered."}[allowWrites],
			}))
		case "ping":
			reply(rpcResult(msg.ID, map[string]any{}))
		case "tools/list":
			reply(rpcResult(msg.ID, map[string]any{"tools": tools}))
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				reply(rpcError(msg.ID, -32602, "invalid params"))
				continue
			}
			t, ok := byName[p.Name]
			if !ok {
				reply(rpcError(msg.ID, -32602, "unknown tool "+p.Name))
				continue
			}
			args, err := t.argsFor(p.Arguments)
			if err != nil {
				reply(rpcResult(msg.ID, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}))
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			stdout, stderr, code := mcpRunner(ctx, args)
			cancel()
			text := strings.TrimSpace(stdout)
			if code != 0 {
				text = strings.TrimSpace(stderr)
				if text == "" {
					text = fmt.Sprintf("exit code %d", code)
				}
			}
			reply(rpcResult(msg.ID, map[string]any{"isError": code != 0, "content": []map[string]any{{"type": "text", "text": text}}}))
		default:
			if strings.HasPrefix(msg.Method, "notifications/") {
				continue
			}
			reply(rpcError(msg.ID, -32601, "method not found: "+msg.Method))
		}
	}
	return sc.Err()
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve the commands as MCP tools over stdio (read-only unless --allow-writes)",
	Long: `Runs a Model Context Protocol server on stdin/stdout so an agent can call zbxctl as tools.

Every tool call runs zbxctl itself, so the Zabbix URL and token come from the config file or
environment of the process that started this server; the agent never sees them.

By default only commands that cannot change anything are offered. With --allow-writes the write
commands are offered too, and each is still a dry run until the call passes apply=true. Never
offered: run script (needs a human), upgrade, context use, state prune, and anything that works
on a file path (export, import, diff).

Example client entry (Claude Code, Claude Desktop and others use this shape):

  {"mcpServers": {"zabbix": {"command": "zbxctl", "args": ["mcp"], "env": {"ZBXCTL_CONTEXT": "lab"}}}}`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return serveMCP(os.Stdin, os.Stdout, mcpAllowWrites)
	},
}

func init() {
	mcpCmd.Flags().BoolVar(&mcpAllowWrites, "allow-writes", false, "also offer the write commands (each still a dry run unless the call passes apply=true)")
	rootCmd.AddCommand(mcpCmd)
}
