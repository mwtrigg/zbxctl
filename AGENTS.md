# zbxctl: notes for agents

Go CLI for Zabbix 7.x. Read `README.md` for the user-facing contract; this file is for working on the code.

## Design goals

- Tools an LLM agent can use well: predictable subcommands, flat purpose-built JSON, clear structured errors, stable exit codes, no raw API pass-through.
- Safe by default: reads never change anything; every write is a dry run until `--apply` (`--yes` is an alias).
- Idiomatic Go CLI: plain getters and setters (`get`, `create`, `update`, `delete`, `enable`, `disable`, `acknowledge`), flags, one static binary. No pipe-native or PowerShell-style object pipelines; use `jq` for composition.

## Layout

- `cmd/zbxctl`: entry point.
- `internal/cli`: cobra commands. `get.go` (reads), `write.go` (writes, `confirm` gate), `diagnose.go`, `llm.go` (the command manifest, keep it in sync when flags change), `root.go` (global flags, error classification).
- `internal/api`: JSON-RPC client (Bearer token auth). `internal/api/resources`: one file per Zabbix object.
- `internal/output`: json/jsonl/table printing, raw-field stripping, error printing.
- `internal/config`: contexts, env overrides, permission tiers.

## Rules of the road

- Errors go through `cliFail`/`classify` so the exit code and `kind` stay consistent; usage mistakes return `usageError` (exit 5).
- New writes must call `confirm(...)` before changing anything and must be covered by a dry-run test.
- Zabbix API changes between releases: test against 7.x behavior (no `user.type`, availability lives on interfaces, `maintenance.create` takes host/group objects). Put API-shape assertions in fake-server tests (`internal/api/resources/*_test.go`, `internal/cli/write_test.go`).
- Develop and test live only against a sandbox Zabbix, never production. Never print, log or commit tokens or credentials.
- No employer- or environment-specific names, hosts or data in code, docs, examples or tests.

## Build and test

```sh
go vet ./... && go test ./...
make build
```
