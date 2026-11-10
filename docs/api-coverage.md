# Zabbix API coverage

What zbxctl covers of the Zabbix 7.x API, and what it does not. Coverage was checked against the
command list (`zbxctl llm capabilities`) and, for the behaviours noted, against a live 7.4 server.
Where a command does not exist, `zbxctl api call` reaches the method (reads freely, writes only with
`--apply`).

## Covered

| Object | Commands |
|---|---|
| host | get, create, update (groups, templates, proxy), delete, enable/disable, tag/untag, bulk |
| host group | get, create, rename, delete |
| template | get, delete, export, diff, import |
| item | get, latestdata, create, update, delete |
| trigger | get, create, update, delete, enable/disable |
| problem, event, alert | get (orphans, suppression, grouping), acknowledge/close/suppress/unacknowledge |
| history, trends | get, `--all`, `--step`, `--gaps-over`, `backtest` |
| maintenance | list, create, extend, shorten, delete |
| user, user group | get, create, delete |
| user macro | get, set, delete (host and global) |
| value map | get, create, update, delete |
| global regular expression | get, create, update, delete |
| discovery | rules, lost items and triggers, delete lost |
| proxy, proxy group | get, move a host between them |
| audit log | get |
| script | get, run (allowlist and confirmation token) |
| action | get |
| configuration export/import | template and host |

## Not covered (reach it with `api call` if needed)

- Action and media type create/update/delete, alert media setup.
- Dashboards, graphs, maps, services/SLA (mostly UI-driven; export is the usual need).
- Event correlation, connectors, network discovery rules (`drule`).
- API tokens (`token.*`): deliberately not exposed, since generating one would print a secret.
- Roles and user directories, authentication and other global settings, housekeeping, HA nodes.
- Host interfaces as their own object (they are shown on `get host` and `get interfaces`).

## Notes learned against a live 7.4 server

- `host.get` has no usable `available` field; availability lives on the interfaces.
- `user.get` has no `type` field; use the role.
- `maintenance.create` takes host and group objects, not `hostids`/`groupids`.
- `hostgroup.get` has no `internal` field; Zabbix itself refuses to change internal groups.
- Discovered objects use `itemDiscovery` / `triggerDiscovery` (camel case) with no `lastcheck`.
- `apiinfo.version`, `user.login` and `user.checkAuthentication` must be called without the
  Authorization header; everything else needs the Bearer token.
- Audit log resource codes: user 0, host 4, user group 11, trigger 13, host group 14, item 15,
  maintenance 27, macro 29, template 30. Only those are named in `get auditlog`.
