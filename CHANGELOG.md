# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
zbxctl is `0.x`: the command line and JSON shapes can still change between releases while support for newer
Zabbix versions (8.x) is worked out.

## [Unreleased]

## [0.2.0]

### Security
- MCP: the arguments an agent sends are positional values only. They follow a `--` separator and a value that
  starts with `-` is refused, so they can no longer become flags such as `--yes`, `--id-file`, `--no-state`,
  `--state-dir` or `-c`; the server's environment pins the context (`context` is not offered); the child process
  refuses every flag the server never offers; results are marked as untrusted data; `api call` is not offered.
- Permission tiers and config fail closed: an unknown tier, a context named with `-c` or `ZBXCTL_CONTEXT` that is
  not in the file, and an unknown config key are errors (a typo used to mean full write access).
- `run script` needs the operator tier, its allowlist entries are pinned to the script's command
  (`name@sha256:<hash>`), the confirmation token is read from the terminal, and the printed command shows control
  characters instead of acting on them.
- `api call` lower-cases the method before checking it, refuses admin-class writes (user, token, script, action,
  role, usergroup, authentication, userdirectory, mediatype) unless the context sets `api_call_admin`, and masks
  secrets in results (`--show-secrets` to see them). `get interfaces` masks the SNMP community.
- `upgrade` binds the version into the signed material: archive names carry the version, so an old signed release
  cannot be passed off as a new one.
- Table output shows control characters as visible escapes; exports are written 0600; warnings for a plain `http://`
  URL and for a world-readable config file that holds credentials.

### Fixed
- `update host --remove-template` only unlinks. It used to send `templates_clear`, which also deletes the items,
  triggers, graphs and history the template gave the host. `--clear-template` is the explicit, warned form.
- Every `--apply` write now saves a before-state file (about 17 single-object writes saved none). Files can no longer
  overwrite each other, are written atomically and record per-target outcomes. A write whose answer is lost to a
  timeout or a 502/503/504 keeps its file and exits 7 (`unknown_outcome`) instead of claiming nothing changed.
- Lists say when they reached the limit (`meta.limit_reached`); `--limit -1` returns everything.
- All-digit host and template names are looked up by ID and by name; an ambiguity asks for `id:N` or `name:N`.
- Exit codes: 8 `permission`; 5 for missing URL, ambiguous names and bad flags; 4 for a 404, 5xx or non-API answer.
- `upgrade` asks on stderr and accepts `--apply`; `context use` keeps comments; `delete host` saves full interfaces.

### Added
- The before-state files that `--apply` saves are now pruned automatically: zbxctl keeps the newest **200** and
  deletes older ones as new ones are written, saying so on stderr. Configure it with `state_keep` in the config,
  `ZBXCTL_STATE_KEEP` or `--state-keep`; `0` keeps everything. `state prune --keep N` does the same by hand.

## [0.1.0]

The first release.

### Added
- Reads for hosts, items, triggers, problems, events, history, trends, templates, groups, users, proxies,
  macros, value maps, regular expressions, discovery, the audit log and more; `diagnose host` and
  `diagnose trigger`; `backtest` for replaying an alert condition over history or trends.
- Changes for hosts, groups, items, triggers, templates (export, diff, import), maintenance windows, users and
  user groups, macros, value maps, regular expressions and problem events (`acknowledge`, `unacknowledge`,
  `suppress`, `unsuppress`). Every change is a dry run until `--apply`; bulk changes check all targets first,
  support `--expect` and `--id-file`, save a before-state file, and verify the result.
- `state list|show|prune` for the before-state files, which double as an audit trail.
- `api call` as a guarded escape hatch, `llm capabilities` for agents, and `zbxctl mcp`, an MCP server.
- Detection of the Zabbix version (7.4 and newer are supported) with an optional per-connection pin.
- Signed releases for Linux, macOS and Windows on amd64 and arm64, with checksums, SBOMs and a verifying
  `zbxctl upgrade`.
