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
- the MCP server (`zbxctl mcp`) offering a tool it should not, or letting an agent reach a local file.

## Supported versions

Fixes go into the latest release.
