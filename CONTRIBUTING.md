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

## Releasing (maintainers)

Releases are built and signed by `.github/workflows/release.yml` when a `v*` tag is pushed.
One-time repository setup that the workflow relies on:

- A `release` environment holding the `ZBXCTL_SIGNING_KEY` secret (an ed25519 SSH key; the
  public half is compiled in as `internal/cli/release_keys.go` and published as
  `docs/release-signing-key.pub`), restricted to `v*` tags. Keep the secret out of the
  repository-level secrets, so that only a release job can read it. The environment does not
  require a reviewer (the owner chose not to): a `v*` tag on a commit that is on `main` is
  signed and published without anyone approving it.
- A tag ruleset for `v*` that blocks moving and deleting tags (add a `creation` rule with the owner as bypass if only the owner should be able to create them), and a branch ruleset for `main`. The release workflow refuses a tag whose commit is not on `main`, and its `verify` job runs the vet, the tests and the notices check before anything is signed. Those, and the tag ruleset, are the controls: anyone who can push to `main` and create a `v*` tag can cause a signed release, so keep write access to the repository small.
- Cut the final release on its own commit (the one that renames `[Unreleased]` to the version in CHANGELOG.md), not on the commit that already carries a release candidate tag.
- Private vulnerability reporting turned on (see SECURITY.md).

The version is part of every archive name (`zbxctl_<version>_<os>_<arch>`) because the signed
`checksums.txt` has to bind each archive to its version; `zbxctl upgrade` refuses a release whose
signed checksums do not list the archive named after its tag. GoReleaser, syft and govulncheck
are pinned to exact versions in the workflows; bump them on purpose, not by floating.
