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

Before-state files (see below) can be turned off per context with `before_state: false` in the config, or with `ZBXCTL_BEFORE_STATE=false`; `state_dir` / `ZBXCTL_STATE_DIR` moves them. The flags `--no-state` and `--state-dir` win over both.

`permissions` caps what a context may do: `readonly` blocks all writes, `operator` allows enable/disable/acknowledge/maintenance, `write` allows everything. `run script` additionally needs the script listed in the context's `allowed_scripts` and a one-time confirmation token.

## The audit trail: before-state files

Every change made with `--apply` saves a before-state file: for each target, what was done and what the target looked like before. Together they are an audit trail of what zbxctl changed. Turn this off with `before_state: false` in the config (per context), `ZBXCTL_BEFORE_STATE=false`, or `--no-state`; `state_dir` / `ZBXCTL_STATE_DIR` / `--state-dir` choose the directory (default `~/.local/state/zbxctl`).

```
zbxctl state list [--command delete-host] [--older-than 30d]   # newest first
zbxctl state show <file>                                       # what one file recorded
zbxctl state prune --older-than 90d [--apply]                  # a dry run until --apply
```

Nothing is deleted automatically, and `state prune` only ever touches files zbxctl wrote. These commands read local files and need no Zabbix connection.

### Undoing a change by hand

There is deliberately no automatic `restore`: too much can go subtly wrong. The file tells you (or an agent) what to do instead. Find the file with `state list`, read it with `state show`, then run the inverse command:

| Change | What the file holds | Undo |
|---|---|---|
| `enable` / `disable host`, `trigger` | the status before | the opposite command on the same target |
| `acknowledge` / `unacknowledge` | whether it was acknowledged and closed | the opposite verb (a closed problem cannot be reopened) |
| `suppress` / `unsuppress` | the event | the opposite verb (an earlier suppression time is not recorded) |
| `maintenance extend` / `shorten` | the end time before | `maintenance extend\|shorten --until <the old end>` |
| `maintenance delete` | name, start, end, hosts, groups | `maintenance create` with those values |
| `delete host` | groups, templates, tags, interfaces, macros (secret values are never saved) | `create host`, then `update host` and `set macro`; history is gone |
| `delete template` | the full yaml export | `import template <file> --apply` after saving `export_yaml` to a file |
| `import template\|host` | the previous yaml export of every object it changed | import that yaml again (objects the import created are not recorded) |
| `delete group`, `item`, `trigger`, `macro`, `user` | the object's fields | the matching `create`/`set` command with those fields |
| `delete lost items\|triggers` | what was deleted | nothing to restore: discovery recreates them if the object comes back |

Always do the undo as a dry run first and check it against the file.

## Escape hatch

`zbxctl api call <method> --params '{...}'` calls any Zabbix API method and prints the raw result, for the long tail that has no command. Reads (`*.get`, `configuration.export`, ...) run directly; every other method is a dry run until `--apply`, needs the write tier and saves an audit record with secrets masked. `user.login`, `token.generate`, `script.execute` and similar are refused. Prefer a dedicated command whenever one exists: it gives flat results, dry runs and checks.

See [docs/api-coverage.md](docs/api-coverage.md) for what is and is not covered of the Zabbix API.

## Output and connection options

`--timeout` (default 180s) bounds each API request, `--retries` (default 2) repeats read-only calls after a connection error or a 502/503/504 (writes are never retried), and `--tz America/New_York` (or `ZBXCTL_TZ`, or `local`) renders times in that zone with an explicit offset instead of UTC.

## Output

JSON is the default. `-o jsonl` gives one object per line, `-o csv` a header plus rows, `-o table` is for humans.

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
get hosts | host | items | latestdata | history | trends | interfaces
get triggers | problems | events | alerts
get discovery | lost items | lost triggers
get proxies | proxy-groups | auditlog | valuemaps | regexps
get templates | template | groups | users | user | usergroups | scripts | macros | actions
backtest --item k --when 'avg(5m)>90'   # replay an alert idea over history/trends; judge it against real problems
diagnose host <host>            # open problems, recent events, latest data, broken items in one call
diagnose trigger <id>|<event> --event   # why a problem is open: disabled trigger/host/items, unsupported items, suppression, manual close off
auth status | context list | context use <name>
```

Write (dry run unless `--apply`; `--yes` is an alias):

```
enable | disable  host <host> | trigger <id>
acknowledge <event-id> [--message ...] [--close] [--severity s]   /   unacknowledge <event-id>
suppress <event-id> [--until 7d]   /   unsuppress <event-id>      # hide a problem for a while; not the same as acknowledging
maintenance list | create | extend | shorten | delete
create host | update host | delete host
tag host <host> --tag k [--value v]   untag host <host> --tag k [--value v]
create user | delete user | create usergroup | delete usergroup
create|update|delete group        # host groups; delete refuses groups that still have hosts
create|update|delete item         # agent, agent-active, trapper, simple, calculated, dependent
create|update|delete trigger      # from an expression; discovered and template objects are refused
create|update|delete valuemap    # --map 0=Down --map 1=Up --default Unknown (also --map-ge/-le/-range/-regexp)
create|update|delete regexp      # --expr 'matches:^(ext4|xfs)$'  (types: contains, any, not-contains, matches, not-matches)
set macro | delete macro          # host or --global; secret values only from --value-file/--value-stdin
update host --proxy <name|none> | --proxy-group <name>
delete template <name> [--allow-linked]   # before-state keeps a full export
delete lost items|triggers [--host h] [--close-problems]   # only objects that discovery lost
import template|host <file>       # without --apply: shows the diff only
run script <name> --host <host>   # dangerous: executes code on a monitored host
```

Bulk writes: `enable|disable host|trigger`, `acknowledge`, `maintenance extend|shorten|delete` and `delete host` take several targets (arguments and/or `--id-file`, one per line). All targets are resolved first, so a typo changes nothing; `--expect N` refuses to run unless exactly N distinct targets resolve; with `--apply` a before-state file is saved under `~/.local/state/zbxctl/` (`--state-dir`, `--no-state`) and every change is re-read to verify it (`--no-verify` skips that). Acknowledging and suppressing are different things: acknowledge says someone has seen the problem, suppress hides it from the problem list (for a while, or indefinitely). `acknowledge --close` only closes problems whose trigger allows manual close and acknowledges the others instead, then re-checks after about 15 seconds since closing is asynchronous.

`delete user` refuses `Admin` and the user that owns the credentials in use. `create user` takes the password from `--password-file` or `--password-stdin`, never from a flag value.

Export, diff and import of templates and hosts:

```
zbxctl export template web-base --file web-base.yaml     # yaml | json | xml
zbxctl diff template web-base.yaml                       # what an import would change, read-only
zbxctl diff template --dir ./templates                   # the same for every export file in a directory
zbxctl import template web-base.yaml --apply             # never deletes anything missing from the file; saves the previous export first
zbxctl export template --dir ./templates --search Linux  # one file per template (hosts: --group <name> or --all)
zbxctl import template --dir ./templates --expect 12 --apply
```

Housekeeping: `version`, `upgrade [--check]`, `completion bash|zsh|fish|powershell`.

`--since`/`--until` accept unix seconds, RFC3339, `YYYY-MM-DD`, or relative `30m`, `24h`, `7d`, `2w`. `--severity` accepts `0`-`5` or `not_classified|information|warning|average|high|disaster`. `get problems` triage flags: `--exclude-orphans` drops problems that can never recover (trigger, host or all items disabled), `--suppressed true|false` and `--maintenance` filter on suppression, `--group-by name` collapses the same alert across hosts into one row with a count, the hosts and the event IDs (handy for `acknowledge`).

Backtesting an alert idea: `zbxctl backtest --host web-01 --item system.cpu.util --when 'avg(5m)>90' --since 7d` lists when the condition would have fired (conditions: `last>N`, `avg(5m)>N`, `min(10m)>=N`, `max(1h)<N`, `nodata(10m)`). Add `--real-problem "CPU is high"` (or `--real-event ID`) to judge each fire against real problems on the same host: true alert with its lead time in minutes, false alarm, and missed problems; `--summary` gives one row per host. Use `--source trends` for year-long windows. Conditions can also compare items and shares of them, for example a memory-drop rule: `--when '(max(15m) - last) > 10% of last("vm.memory.size[total]") AND last("vm.memory.size[pavailable]") < 50'` (a call with a quoted key reads that other item on the same host; unquoted calls read `--item`; `10% of X` is 0.10 * X; `+ - * /`, `AND`, `OR` and parentheses work).

History and trends: `get history --all` pages through a whole window (no 100-row stop), `--step 5m` keeps one value per bucket, `--gaps-over 10m` lists the silences instead of the samples; `get trends` returns hourly min/avg/max of numeric items for baselines and long look-backs; `--rollup day|week|month` folds them into days, weeks (Monday start) or months in the `--tz` zone.

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
