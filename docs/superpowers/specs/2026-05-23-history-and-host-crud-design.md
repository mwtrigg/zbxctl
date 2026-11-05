# Design: History Query, Multi-Host Surface, and Host CRUD

**Date:** 2026-05-23
**Status:** Approved

---

## Scope

Four additions to zbxctl:

1. `get history` — time-series metric values, single or multi-host
2. `get latestdata` extension — add multi-host/group support to existing command
3. `create host` / `update host` / `delete host` — host lifecycle write operations

---

## Guiding Principle: Agent-First, One Round-Trip

Agents troubleshooting across multiple hosts should be able to make a single
tool call and receive a complete, consistently-shaped result set. The CLI
handles host resolution, item resolution, and API composition internally.
Agents stay in reasoning mode rather than orchestration mode.

**Corollary:** Output always includes `host` and `hostid` fields, regardless of
whether one or many hosts were queried. Agents must not need to handle different
output shapes based on cardinality.

---

## 1. `get history`

### Command

```
zbxctl get history --item <key|id> [--host <name|id>]... [--group <name|id>]...
                   [--since <time>] [--until <time>] [--limit N] [--output fmt]
```

`--item` is required. `--host` and `--group` are repeatable and combinable.
If neither `--host` nor `--group` is given, the command requires `--item` to
be a numeric item ID (no host resolution needed).

`--since` / `--until` accept Unix timestamp, RFC3339, or YYYY-MM-DD (same as
existing time flags in get problems/events).

### API composition

Two calls, composed internally:

1. `item.get` — resolves item IDs for the target hosts/groups and key pattern.
   Params: `hostids`/`groupids`, `search: {key_: <item>}` or `itemids` if
   numeric, `output: [itemid, hostid, key_, value_type]`.

2. `history.get` — fetches history for resolved item IDs. Requires `history`
   param matching item `value_type` (0=float, 1=string, 2=log, 3=uint, 4=text).
   If items across hosts have differing value_types, issue one `history.get`
   per type and merge results sorted by clock.

### Output fields

```
host  hostid  itemid  key_  clock  clock_unix  value
```

`host` and `hostid` are always present. Results sorted by `clock` ascending
within each item.

### Error cases

- `--item` key matches no items on the target hosts: exit 2 (not found)
- Mixed value_types across items: handled transparently (multi-fetch + merge)
- `--item` used without `--host`/`--group` and is not numeric: exit 1 with
  message explaining resolution requires a host or group

---

## 2. `get latestdata` — Multi-Host Extension

### Current behaviour

```
zbxctl get latestdata <host>          # single host, positional arg
```

### Extended behaviour

```
zbxctl get latestdata [<host>] [--host <name|id>]... [--group <name|id>]...
                      [--item <key-pattern>] [--limit N] [--output fmt]
```

Positional `<host>` is preserved for backwards compatibility. `--host` flags
and `--group` flags add to the host set. `--item` filters by key substring
(reuses existing `--key` logic — consider aliasing `--item` → `--key` or
renaming for consistency; default to `--item` as the new canonical flag, keep
`--key` as a hidden alias).

### Output fields

Add `host` and `hostid` to existing output. Fields:

```
host  hostid  itemid  key_  name  value_type  lastvalue  lastclock  lastclock_unix  units
```

`host`/`hostid` were already available from the API; they just weren't surfaced
in the output. No additional API calls required for the multi-host case —
`item.get` with `hostids[]`/`groupids[]` handles it in one call.

---

## 3. `create host`

### Command

```
zbxctl create host --name <tech-name> --group <name|id> [--group ...]
                   (--ip <addr> | --dns <hostname>)
                   [--port <N>]                  # default 10050
                   [--interface-type agent|snmp|ipmi|jmx]  # default agent
                   [--visible-name <name>]
                   [--description <text>]
                   [--snmp-version 1|2|3]        # SNMP only
                   [--snmp-community <string>]   # SNMP v1/v2c only
                   [--yes]
```

`--name`, `--group`, and one of `--ip`/`--dns` are required.

### API call

`host.create` with:
- `host`: technical name
- `name`: visible name (defaults to technical name if not given)
- `groups`: resolved `[{groupid}]` array — groups resolved by name via
  `hostgroup.get` if not numeric
- `interfaces`: single interface object built from flags
- `description`: optional

SNMP interface requires an additional `details` subobject (`version`,
`community`, `securitylevel`, etc.). For v1/v2c, `--snmp-community` maps to
`details.community`. SNMP v3 auth fields are out of scope for this pass.

### Output fields

```
hostid  host  name  status
```

Prints confirmation line in table mode: `created host <name> (id: <hostid>)`.

---

## 4. `update host`

### Command

```
zbxctl update host <id|name>
                   [--visible-name <name>]
                   [--description <text>]
                   [--add-group <name|id>]      # repeatable
                   [--remove-group <name|id>]   # repeatable
                   [--add-template <name|id>]   # repeatable
                   [--remove-template <name|id>] # repeatable
                   [--yes]
```

At least one update flag required; no-op invocation is an error.

### API composition

Groups and templates require fetch-then-modify:

1. `host.get` with `selectGroups` and `selectParentTemplates` to get current
   sets.
2. Apply `--add-*` / `--remove-*` diffs.
3. `host.update` with full updated `groups[]` and `templates[]` arrays.
   Zabbix requires the full set, not a delta.

Simple field updates (`visible-name`, `description`) are included in the same
`host.update` call.

### Output fields

```
hostid  host  name  status
```

---

## 5. `delete host`

### Command

```
zbxctl delete host <id|name> [--yes]
```

Resolves name to ID via `GetHost` (existing helper). Confirms before
proceeding unless `--yes`.

### API call

`host.delete` with `[hostid]`.

### Output fields

```
hostid  host  name  status
```

Prints confirmation in table mode: `deleted host <name> (id: <hostid>)`.

---

## File Organisation

| New/changed file | What goes there |
|---|---|
| `internal/api/resources/history.go` | `GetHistory`, `HistoryRecord`, `HistoryFilter` |
| `internal/api/resources/hosts.go` | Add `CreateHost`, `UpdateHost`, `DeleteHost`, extend `GetHosts` for multi-group |
| `internal/cli/get.go` | Add `runGetHistory`; extend `runGetLatestdata` for multi-host |
| `internal/cli/write.go` | Add `runCreateHost`, `runUpdateHost`, `runDeleteHost` |
| `internal/cli/llm.go` | Add manifest entries for all new commands |

No new CLI files needed. Existing split (read in get.go, write in write.go) is
maintained.

---

## LLM Manifest Entries (summary)

| command | key output_fields |
|---|---|
| `get history` | `host`, `hostid`, `itemid`, `key_`, `clock`, `clock_unix`, `value` |
| `get latestdata` | extend existing with `host`, `hostid` |
| `create host` | `hostid`, `host`, `name`, `status` |
| `update host` | `hostid`, `host`, `name`, `status` |
| `delete host` | `hostid`, `host`, `name`, `status` |
