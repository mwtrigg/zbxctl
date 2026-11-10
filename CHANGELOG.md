# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
zbxctl is `0.x`: the command line and JSON shapes can still change between releases while support for newer
Zabbix versions (8.x) is worked out.

## [Unreleased]

## [0.2.0]

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
