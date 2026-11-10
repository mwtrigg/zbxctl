# zbxctl

A Zabbix CLI built for LLM agents and operators: predictable subcommands, flat JSON output, structured errors, read-only by default, and a dry run for every write. Single static Go binary, Zabbix 7.0+ (developed against 7.4).

```
zbxctl get problems --severity high --since 24h
zbxctl get latestdata web-01 --item cpu
zbxctl diagnose host web-01
zbxctl disable host web-01            # dry run: prints what would happen
zbxctl disable host web-01 --apply    # does it
```

## Install

```sh
make build            # CGO_ENABLED=0 go build, version stamped from git
```

Or download a release from [GitHub releases](https://github.com/mwtrigg/zbxctl/releases) and run `zbxctl upgrade` later to self-update.

## Configuration

File: `~/.config/zbxctl/config.yaml`; see [`docs/config.example.yaml`](docs/config.example.yaml).

```yaml
current_context: lab
contexts:
  lab:
    url: https://zabbix.example.com
    api_token: ""          # preferred over username/password
    tls_verify: true
    default_limit: 100
    permissions: readonly  # readonly | operator | write (default write)
```

Priority: CLI flag > `ZBXCTL_*` env var (`ZBXCTL_URL`, `ZBXCTL_API_TOKEN`, `ZBXCTL_CONTEXT`, ...) > config file > default. Never put a token in a command line or commit one.

`permissions` caps what a context may do: `readonly` blocks all writes, `operator` allows enable/disable/acknowledge/maintenance, `write` allows everything. `run script` additionally needs the script listed in the context's `allowed_scripts` and a one-time confirmation token.

## Output

JSON is the default. `-o jsonl` gives one object per line, `-o table` is for humans.

```json
{"data": [ {...}, {...} ], "meta": {"count": 2, "command": "get hosts"}}
```

Results are flat objects with readable values (`"status": "monitored"`, times as RFC3339 UTC). `--raw` adds the raw API values (`*_raw`, `*_unix`).

Errors go to stderr as `{"error": "...", "kind": "...", "code": N}`.

| Code | Kind | Meaning |
|---|---|---|
| 0 | | success (also a dry run) |
| 1 | `error` | API or internal error |
| 2 | `not_found` | host, template, ... not found |
| 3 | `auth` | token or login refused |
| 4 | `connection` | cannot reach Zabbix |
| 5 | `usage` | invalid flag or argument |

## Commands

Read (never change anything):

```
get hosts | host | items | latestdata | history | interfaces
get triggers | problems | events | alerts
get templates | template | groups | users | user | usergroups | scripts
diagnose host <host>            # open problems, recent events, latest data, broken items in one call
auth status | context list | context use <name>
```

Write (dry run unless `--apply`; `--yes` is an alias):

```
enable | disable  host <host> | trigger <id>
acknowledge <event-id> [--message ...] [--close | --unacknowledge | --suppress [--until t] | --unsuppress] [--severity s]
maintenance list | create | delete
create host | update host | delete host
tag host <host> --tag k [--value v]   untag host <host> --tag k [--value v]
create user | delete user | create usergroup | delete usergroup
import template|host <file>       # without --apply: shows the diff only
run script <name> --host <host>   # dangerous: executes code on a monitored host
```

Bulk writes: `enable|disable host|trigger`, `acknowledge`, `maintenance delete` and `delete host` take several targets (arguments and/or `--id-file`, one per line). All targets are resolved first, so a typo changes nothing; `--expect N` refuses to run unless exactly N distinct targets resolve; with `--apply` a before-state file is saved under `~/.local/state/zbxctl/` (`--state-dir`, `--no-state`) and every change is re-read to verify it (`--no-verify` skips that). `acknowledge --close` only closes problems whose trigger allows manual close and acknowledges the others instead, then re-checks after about 15 seconds since closing is asynchronous.

`delete user` refuses `Admin` and the user that owns the credentials in use. `create user` takes the password from `--password-file` or `--password-stdin`, never from a flag value.

Export, diff and import of templates and hosts:

```
zbxctl export template web-base --file web-base.yaml     # yaml | json | xml
zbxctl diff template web-base.yaml                       # what an import would change, read-only
zbxctl import template web-base.yaml --apply             # never deletes anything missing from the file
```

Housekeeping: `version`, `upgrade [--check]`, `completion bash|zsh|fish|powershell`.

`--since`/`--until` accept unix seconds, RFC3339, `YYYY-MM-DD`, or relative `30m`, `24h`, `7d`, `2w`. `--severity` accepts `0`-`5` or `not_classified|information|warning|average|high|disaster`. `get problems` triage flags: `--exclude-orphans` drops problems that can never recover (trigger, host or all items disabled), `--suppressed true|false` and `--maintenance` filter on suppression, `--group-by name` collapses the same alert across hosts into one row with a count, the hosts and the event IDs (handy for `acknowledge`).

`--host` and `--group` accept an ID or a name.

On list commands `--filter KEY=VALUE` (repeatable) adds an exact-match API filter and `--field NAME` prints just that field, one value per line, for shell use.

## For agents

`zbxctl llm capabilities` prints the full command manifest as JSON (flags, examples, output fields, a `dangerous` marker); `zbxctl llm schema <command>` prints one entry. Neither needs authentication or network.

## TLS

`--insecure` (or `tls_verify: false`) skips verification and always prints a warning to stderr. Use `ca_file` to trust a custom CA.

## Development

```sh
go vet ./... && go test ./...
```

Tests run against a fake Zabbix API server; no live instance is needed.

## License

MIT, see [LICENSE](LICENSE).
