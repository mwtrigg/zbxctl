# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
zbxctl is `0.x`: the command line and JSON shapes can still change between releases while support for newer
Zabbix versions (8.x) is worked out.

0.1.0 is the first release. `v0.1.0-rc.1` was its release candidate; the release notes are taken from this
file (`scripts/release-notes.sh`).

## [Unreleased]

## [0.1.1] - 2026-11-10

### Fixed
- `update host` prints the host as it is after the change (it showed the old visible name after `--visible-name`).
- zbxctl refuses to keep before-state files in a directory that another user owns or that other users can write to
  (they could delete or replace the records); the directory zbxctl creates itself is private and passes. A symlink is
  judged by its target.
- Table output also escapes bidirectional-text controls and invisible characters (right-to-left override, isolates,
  zero-width space and joiners, byte order mark, line and paragraph separators), not only control characters.

## [0.1.0] - 2026-11-10

### Added
- Reads for hosts, items, triggers, problems, events, history, trends, templates, groups, users, proxies,
  macros, value maps, regular expressions, discovery, the audit log and more; `diagnose host` and
  `diagnose trigger`; `backtest` for replaying an alert condition over history or trends.
- Changes for hosts, groups, items, triggers, templates (export, diff, import), maintenance windows, users and
  user groups, macros, value maps, regular expressions and problem events (`acknowledge`, `unacknowledge`,
  `suppress`, `unsuppress`). Every change is a dry run until `--apply`; bulk changes check all targets first,
  support `--expect` and `--id-file`, save a before-state file, and verify the result.
- `state list|show|prune` for the before-state files, which double as an audit trail; every target records its
  outcome (applied, failed, or unknown when a timeout or 502/503/504 hid the answer, exit 7).
- The before-state files are pruned automatically: zbxctl keeps the newest **200** (`state_keep`, `ZBXCTL_STATE_KEEP`,
  `--state-keep`; `0` keeps everything), after a write's outcome is known, never the record of an unresolved one.
- A connection can apply changes by default: `mode: apply` in the config or `ZBXCTL_MODE=apply`. `--dry-run`
  previews a single command. The MCP server ignores this setting.
- `api call` as an escape hatch (reads with secrets masked; admin-class writes need `api_call_admin`),
  `llm capabilities` for agents, and `zbxctl mcp`, an MCP server.
- `zbxctl auth check` compares the permission tier you declared with the role and rules of the token's user in
  Zabbix (no change is made) and exits 8 with the reasons when the tier is stronger than what the server enforces
  (configuration changes, script execution, problem actions, API methods beyond reads for a readonly tier).
- Detection of the Zabbix version (7.4 and newer are supported) with an optional per-connection pin.
- Signed releases for Linux, macOS and Windows on amd64 and arm64, with checksums, SBOMs and a verifying
  `zbxctl upgrade` (the version is part of the signed archive name, so an old release cannot pass as a new one).

### Guardrails
zbxctl is a guardrail, not a security boundary: real enforcement is a least-privilege Zabbix token (see the README).
- Permission tiers fail closed: an unknown tier, a context that is not in the file, and an unknown config key are
  errors; a connection with no `permissions` set is `readonly` (`permissions: write` or `ZBXCTL_PERMISSIONS=write`
  allows changes).
- MCP: an agent's arguments are positional values only (they cannot turn into flags), the server pins its context
  when it starts, offers no file-path, `api call`, `run script` or `state prune` tools, never prunes the audit trail (an MCP-only installation keeps adding state files until a person runs a CLI write or `state prune`)
  and marks results as untrusted data.
- `run script` is opt-in per context, needs the operator tier, and its allowlist entries are pinned to the script's name, type, scope, execution
  target, command, parameters, user, auth type, port and timeout (`name@sha256:<hash>`); the typed confirmation stops accidents, not a program running as you.
- Table output shows control characters as visible escapes; exports are written 0600; warnings for a plain `http://`
  URL and for a world-readable config file that holds credentials.

### Behaviour worth knowing
- `update host --remove-template` only unlinks; `--clear-template` also deletes the items, triggers, graphs and
  history the template gave the host, and says so.
- Lists say when they reached the limit (`meta.limit_reached`); `--limit -1` returns everything.
- All-digit host and template names are looked up by ID and by name; an ambiguity asks for `id:N` or `name:N`.
- Exit codes: 4 connection (including a 404, a 5xx or a non-API answer), 5 usage, 7 unknown outcome, 8 permission.
