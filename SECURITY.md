# Security policy

## Reporting a vulnerability

Please report security problems privately, using GitHub's **Report a vulnerability** button on this
repository's Security tab. Do not open a public issue for a security problem.

Include what you found, how to reproduce it, and which version (`zbxctl version`). You will get an
acknowledgement, and a fix or a decision as soon as it can be looked at.

## What matters most for this tool

zbxctl holds a Zabbix API token and can change a Zabbix server, so these are the areas where a report is
most valuable:

- anything that prints, logs or stores an API token, password or secret macro value;
- a write that happens without `--apply`, or a guard that can be bypassed (`--expect`, the `Admin` and
  token-owner protection on `delete user`, the permission tiers, the `run script` allowlist);
- `zbxctl upgrade` installing something that was not verified against the release's `checksums.txt`;
- the MCP server (`zbxctl mcp`) offering a tool it should not (it is a guardrail for honest agent use, see the threat model below).

## Threat model

zbxctl runs as your user, reads your config and token, and calls the Zabbix API. Its dry run, permission
tiers, audit trail, `run script` allowlist and the MCP server's tool list are **guardrails against
mistakes**, not a boundary against code that runs as the same user: that code can skip zbxctl and use the
token directly (or read the config file and the state directory). Real enforcement is the Zabbix side, the
role and user group rights of the token's user. Recommended:

- a separate token per use, and for agents the **User** role with read-only group rights;
- `permissions: readonly` (the default when nothing is set) on contexts an agent uses, and
  `zbxctl auth check` to see whether that tier is stronger than what the token's role enforces;
- no `allowed_scripts` and no `api_call_admin` on contexts an agent can use;
- the config file mode 0600 and the state directory owned by you.

So a report is most valuable when zbxctl does something its own documentation says it will not: prints a
secret, writes without `--apply`, installs something unverified, or lets one of its guardrails fail open
for an honest caller. "A program running as the user can bypass the guardrails" is known and by design.

## Supported versions

Fixes go into the latest release.
