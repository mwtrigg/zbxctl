# Contributing

Thanks for looking. A few things keep this tool dependable for both people and agents.

## Build and test

```sh
go vet ./... && go test ./...
make build
```

Tests run against a fake Zabbix API server; no Zabbix instance is needed. If you have a sandbox
instance, try your change there too, but never against production.

## Ground rules

- **Safe by default.** Reads change nothing. Every write is a dry run until `--apply`, goes through the
  shared write flow (`internal/cli/bulk.go`) so it gets `--expect`, the before-state file and
  verification, and has a test that proves the dry run changes nothing.
- **Flat, predictable output.** Results are flat objects with readable values; errors are
  `{error, kind, code}` with a stable exit code. No raw API pass-through (except the guarded `api call`).
- **Keep the manifest in sync.** `zbxctl llm capabilities` is built from `internal/cli/llm.go`; the MCP server
  reads it. Add or change flags there when you change a command.
- **No secrets, no environment-specific data.** Never put a token, a hostname or an internal name in code,
  docs, tests or examples.
- **Zabbix 7.4 and newer.** Assert request shapes in fake-server tests, not just results.

## Dependencies and licenses

zbxctl is MIT-licensed. Keep the dependency list small and permissive (MIT, BSD, Apache-2.0). After
changing `go.mod`, regenerate the notices and commit them:

```sh
go run scripts/notices/main.go > THIRD_PARTY_NOTICES.md
```

CI fails if `THIRD_PARTY_NOTICES.md` or `go.mod`/`go.sum` are out of date.

By contributing you agree that your contribution is licensed under the MIT License, the same as the
rest of the project.
