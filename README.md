# zbxctl

Zabbix 7.0 CLI — machine-readable output, agent-friendly, single static binary.

```
zbxctl get problems --severity high --output jsonl | jq '.name'
zbxctl get latestdata web-server-01
zbxctl auth status
```

## Install

Download the latest binary from [releases](https://github.com/mwtrigg/zbxctl/releases), or build from source:

```sh
CGO_ENABLED=0 go build -o zbxctl ./cmd/zbxctl
```

No runtime dependencies. Copy and run.

## Configuration

File: `~/.config/zbxctl/config.yaml`

```yaml
current_context: prod

contexts:
  prod:
    url: https://zabbix.example.com
    api_token: your-token-here
    tls_verify: true
    default_limit: 100
```

See [`docs/config.example.yaml`](docs/config.example.yaml) for the full annotated reference.

**Priority chain:** CLI flag > `ZBXCTL_*` env var > config file > built-in default.

Key env vars: `ZBXCTL_URL`, `ZBXCTL_API_TOKEN`, `ZBXCTL_USERNAME`, `ZBXCTL_PASSWORD`, `ZBXCTL_CONTEXT`.

## Output formats

Select with `--output` / `-o`:

| Format | Default | Description |
|---|---|---|
| `table` | TTY | Human-readable table with headers |
| `json` | | `{"data": [...], "meta": {"count": N}}` |
| `jsonl` | non-TTY | One JSON object per line — agent and pipeline default |

Errors always go to stderr. When `--output json` or `jsonl` is set, errors are structured: `{"error": "...", "code": N}`.

## Commands

### Read

```
zbxctl get hosts          [--group <name>] [--status monitored|unmonitored] [--search <text>] [--limit N] [--sort-by host|name]
zbxctl get host <id|name>

zbxctl get items <host>   [--key <pattern>] [--status enabled|disabled] [--limit N]
zbxctl get latestdata <host>

zbxctl get triggers       [--host <id|name>] [--severity <level>] [--status enabled|disabled] [--limit N] [--sort-by priority|lastchange]
zbxctl get problems       [--host <id|name>] [--severity <level>] [--since <time>] [--acknowledged true|false] [--limit N]
zbxctl get events         [--host <id|name>] [--since <time>] [--until <time>] [--limit N]

zbxctl get templates      [--search <text>] [--limit N]
zbxctl get template <id|name>

zbxctl get groups
zbxctl get users
zbxctl get user <id|name>
zbxctl get usergroups
```

`--host` and `--group` accept either a numeric ID or name. `--since` / `--until` accept a Unix timestamp, RFC3339, or `YYYY-MM-DD`.

`--severity` accepts `0`–`5` or `not_classified` | `information` | `warning` | `average` | `high` | `disaster`.

### Write

Coming in Phase 2: `enable`/`disable` host and trigger, `acknowledge`, `maintenance create`/`delete`.

### Supporting

```
zbxctl auth status
zbxctl context list
zbxctl context use <name>
zbxctl completion bash|zsh|fish|powershell
```

### Agent introspection

```
zbxctl llm capabilities          # full command manifest as JSON
zbxctl llm schema <command>      # schema for one command
```

These require no authentication and are safe to call without a configured context.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Internal error |
| `2` | Not found |
| `3` | Auth failure |

## TLS

Pass `--insecure` to skip TLS verification. A warning is always printed to stderr — it cannot be suppressed.

Use `ca_file` in the context config to trust a custom CA bundle.

## Source

Primary: [codeberg.org/mwtrigg/zbxctl](https://codeberg.org/mwtrigg/zbxctl)
GitHub is a mirror used for CI and release artifact hosting only.

## License

MIT — see [LICENSE](LICENSE).
