# Design: Agent Troubleshooting Surface

**Date:** 2026-05-23
**Status:** Approved

---

## Scope

Six additions targeted at agent-driven troubleshooting workflows:

1. `get items --state` — surface broken/unsupported items
2. `get scripts` + `run script` — remote diagnostic execution
3. `get alerts` — notification delivery status
4. `get interfaces <host>` — interface configuration and availability
5. `diagnose host` — single-call composite snapshot
6. `get history --around-event` — event-anchored time-series

Deferred to later: `get audit`, `get proxies`.

---

## Guiding Principle (same as prior spec)

Agents make one call and get a complete, consistently-shaped result. `host` and
`hostid` are always present in output. Cardinality does not change output shape.

---

## 1. `get items --state`

### Command

```
zbxctl get items <host> [--state normal|unsupported] [--key <pattern>]
                        [--status enabled|disabled] [--limit N] [--output fmt]
```

`--state unsupported` returns items where Zabbix cannot collect data (wrong key,
agent offline, permission error, etc.). These indicate broken monitoring rather
than a real problem — critical distinction for an agent doing triage.

### API

`item.get` with `state: 1` (not supported) or `state: 0` (normal). The `error`
field on the item contains the Zabbix error message explaining why collection
failed.

### Output fields (additions to existing)

```
state  state_raw  error
```

`state` is humanised (`normal` / `unsupported`). `state_raw` is the integer.
`error` is empty string when `state=normal`.

---

## 2. `get scripts` + `run script`

### Commands

```
zbxctl get scripts [--host <name|id>] [--output fmt]

zbxctl run script <script-name|id> --host <name|id> [--yes] [--output fmt]
```

`get scripts` without `--host` lists all scripts the authenticated user can see.
With `--host`, filters to scripts applicable to that host (Zabbix scope rules
applied server-side).

`run script` resolves script by name or numeric ID, resolves host, confirms
unless `--yes`, then calls `script.execute`. Output includes the script's stdout
captured by Zabbix.

### API

- `script.get` — list scripts
- `script.execute` — `{ scriptid, hostid }` → `{ response: "success", value: "<stdout>" }`

### Output fields

**`get scripts`:**
```
scriptid  name  type  type_raw  execute_on  execute_on_raw  description
```

`type`: script | ipmi | ssh | telnet | webhook
`execute_on`: agent | server | server-proxy

**`run script`:**
```
scriptid  script_name  hostid  host  response  value
```

`value` is the captured stdout. On failure, `response` is `"failed"` and
`value` contains the error.

---

## 2a. Script Execution Prerequisites

### What must be configured in Zabbix

**User permissions:**
- The API user must have a role with "Execute scripts" checked.
- In Zabbix 6.0+: Administration → User roles → [role] → Actions →
  "Execute scripts" must be enabled.

**Script scope:**
- Each script has a "Host group" scope. Scripts scoped to a specific group are
  only executable against hosts in that group. Scripts with no group restriction
  are global.
- Scripts also have a "User group" restriction. Ensure the API user's group is
  allowed.

**Script type configuration:**
- Type "Script" is the most common. Runs a shell command.
- `Execute on: Zabbix agent` — command runs on the monitored host via the agent.
- `Execute on: Zabbix server` — command runs on the Zabbix server itself
  (useful for server-side network tools like ping/traceroute to the host).
- `Execute on: Zabbix server or proxy` — runs on the proxy if one is used,
  otherwise on the server.

---

### Linux host requirements (agent execution)

**Zabbix agent config** (`/etc/zabbix/zabbix_agentd.conf` or
`/etc/zabbix/zabbix_agent2.conf`):

```ini
# Zabbix agent (legacy):
EnableRemoteCommands=1        # deprecated post-5.0, replaced by AllowKey
AllowKey=system.run[*]        # allow any remote command (or restrict pattern)
LogRemoteCommands=1           # audit trail — recommended

# Zabbix agent 2:
AllowKey=system.run[*]
```

Restart required after config change:
```sh
systemctl restart zabbix-agent    # or zabbix-agent2
```

**OS-level considerations:**
- The agent process runs as the `zabbix` user by default. Commands execute as
  that user. If a script needs elevated permissions, configure `sudo` with a
  NOPASSWD rule for specific commands:
  ```
  zabbix ALL=(ALL) NOPASSWD: /usr/bin/ss, /usr/bin/ip, /bin/ping
  ```
- SELinux: the `zabbix_agent_t` context may block certain syscalls. Test with
  `ausearch -m avc -ts recent` if commands silently fail.
- Scripts run in a minimal shell environment. Do not rely on `.bashrc` or user
  PATH. Use absolute paths in script commands.

---

### Windows host requirements (agent execution)

**Zabbix agent config** (`C:\Program Files\Zabbix Agent\zabbix_agentd.win.conf`
or agent 2 equivalent):

```ini
EnableRemoteCommands=1        # agent v1 (legacy)
AllowKey=system.run[*]        # agent v2 and agent v1 post-5.0
LogRemoteCommands=1
```

Restart the service after changes:
```powershell
Restart-Service "Zabbix Agent"     # or "Zabbix Agent 2"
```

**PowerShell scripts:**
The agent invokes commands via `cmd.exe`. To run PowerShell:
```
powershell.exe -NonInteractive -ExecutionPolicy Bypass -Command "..."
```

The `-ExecutionPolicy Bypass` flag is required unless the machine's policy
already permits script execution. `-NonInteractive` prevents prompts that would
hang the agent's timeout.

**Service account:**
- Default: `LOCAL SYSTEM` (broad permissions, use with caution).
- Recommended: a dedicated low-privilege service account. Grant it `Log on as
  a service` rights and only the specific permissions the scripts require.
- If scripts need network access, ensure the account has appropriate domain
  permissions and that the Windows Firewall does not block outbound connections
  from the agent process.

**WMI and registry queries:**
Work without extra config when running as `LOCAL SYSTEM`. Under a restricted
account, add the account to the `Distributed COM Users` and `Performance
Monitor Users` local groups.

**Execution timeout:**
Zabbix's default script timeout is 30 seconds (`Timeout=30` in agent config,
max 30). Long-running diagnostics must complete within this window or will be
terminated and return an error.

---

## 3. `get alerts`

### Command

```
zbxctl get alerts [--host <name|id>] [--event <id>] [--since <time>]
                  [--status sent|failed|all] [--limit N] [--output fmt]
```

Returns fired notifications: who was paged, via which media type, delivery
status, and the message content. Useful for assessing whether humans are already
aware of an incident and whether notifications delivered successfully.

### API

`alert.get` with `hostids`, `eventids`, `time_from`, `output: extend`.

`status`: 0=not sent, 1=sent, 2=failed, 3=new (Zabbix 6.0+).

### Output fields

```
alertid  eventid  hostid  host  clock  clock_unix  sendto  subject
status   status_raw  error  userid  mediatypeid
```

`sendto` is the destination (email address, phone number, Slack channel, etc.).
`error` contains delivery failure reason when `status=failed`.

---

## 4. `get interfaces <host>`

### Command

```
zbxctl get interfaces <host> [--output fmt]
```

Shows all network interfaces Zabbix has configured for a host, their
availability state, and any connection errors. Answers "is Zabbix trying to
reach the right IP?" and "why is the agent unavailable?"

### API

`host.get` with `hostids` and `selectInterfaces: extend`. No additional call.

### Output fields

```
interfaceid  hostid  host  type  type_raw  main  useip  ip  dns  port
available    available_raw  error
```

`type`: agent | snmp | ipmi | jmx
`main`: 1 = default interface for that type
`available`: unknown | available | unavailable
`error`: non-empty when `available=unavailable`, contains the connection error
from Zabbix's last check attempt.

For SNMP interfaces, include:

```
snmp_version  snmp_community  snmp_securityname
```

(community string surfaced for v1/v2c; security name for v3. No auth credentials
exposed.)

---

## 5. `diagnose host`

### Command

```
zbxctl diagnose host <name|id> [--since <duration>]  # default: --since 24h
                               [--output fmt]
```

Single call that returns a composite snapshot for agent-driven triage. Replaces
4–5 sequential tool calls with one. Always outputs JSON or JSONL (table mode
falls back to JSON — the structure is too nested for a flat table).

### Internal composition

Five parallel-safe API calls (can be issued concurrently):

1. `host.get` → host record + interface availability
2. `problem.get` → open problems for this host
3. `event.get` → events in the `--since` window
4. `item.get` with `lastvalue` → latest metric snapshot
5. `item.get` with `state=1` → unsupported items

Results assembled into a single structured output object.

### Output shape

```json
{
  "host": {
    "hostid": "...", "host": "...", "name": "...",
    "status": "monitored", "available": "available"
  },
  "open_problems": [ { "eventid": "...", "name": "...", "severity": "high", ... } ],
  "recent_events":  [ { "eventid": "...", "value": "problem", "clock": "...", ... } ],
  "latestdata":     [ { "itemid": "...", "key_": "...", "lastvalue": "...", ... } ],
  "broken_items":   [ { "itemid": "...", "key_": "...", "error": "...", ... } ],
  "meta": {
    "host": "...", "since": "...", "generated_at": "..."
  }
}
```

JSONL mode emits one object per section:
```
{"section":"host",          "data":{...}}
{"section":"open_problems", "data":[...]}
{"section":"recent_events", "data":[...]}
{"section":"latestdata",    "data":[...]}
{"section":"broken_items",  "data":[...]}
{"section":"meta",          "data":{...}}
```

---

## 6. `get history --around-event`

### Command

```
zbxctl get history --around-event <event-id>
                   [--item <key-pattern>]   # filter; default: all items on host
                   [--window 30m|1h|2h|6h] # default: 1h (±30m)
                   [--limit N]
                   [--output fmt]
```

Given an event ID, automatically resolves the event timestamp, finds the
affected host, and returns history for the host's items in a window centred on
the event. Answers "what was happening right before and after the alert fired?"
without requiring the agent to chain multiple calls.

### Internal composition

1. `event.get` with `eventids` → get `clock` (event timestamp) and `objectid`
   (triggerid)
2. `trigger.get` with `triggerids` + `selectHosts` → get `hostid`
3. `item.get` with `hostids` (+ optional key filter) → get itemids and
   value_types
4. `history.get` with resolved itemids, `time_from = clock - window/2`,
   `time_till = clock + window/2`

Steps 1–3 can be issued sequentially (each depends on the previous). Step 4
depends on step 3.

### Output fields

Same as `get history`:
```
host  hostid  itemid  key_  clock  clock_unix  value
```

Plus a `meta` field in JSON mode:
```json
"meta": {
  "event_id": "...",
  "event_clock": "...",
  "event_name": "...",
  "window_from": "...",
  "window_till": "..."
}
```

---

## File Organisation

| New/changed file | What goes there |
|---|---|
| `internal/api/resources/items.go` | Add `state`/`error` fields and filter support |
| `internal/api/resources/scripts.go` | `GetScripts`, `RunScript`, `Script` type |
| `internal/api/resources/alerts.go` | `GetAlerts`, `Alert` type |
| `internal/api/resources/interfaces.go` | `GetInterfaces`, `Interface` type |
| `internal/cli/get.go` | `runGetScripts`, `runGetAlerts`, `runGetInterfaces`, extend items |
| `internal/cli/get.go` | `runGetHistory` extension for `--around-event` |
| `internal/cli/write.go` | `runRunScript` |
| `internal/cli/diagnose.go` | `runDiagnoseHost` (new file — composition logic warrants isolation) |
| `internal/cli/llm.go` | Manifest entries for all new commands |

`diagnose.go` gets its own file because it orchestrates multiple resource
packages and has distinct concurrency logic. Keeping it separate prevents
get.go from growing further.

---

## LLM Manifest Entries (summary)

| command | key output_fields |
|---|---|
| `get items` (extended) | add `state`, `state_raw`, `error` |
| `get scripts` | `scriptid`, `name`, `type`, `execute_on`, `description` |
| `run script` | `scriptid`, `script_name`, `hostid`, `host`, `response`, `value` |
| `get alerts` | `alertid`, `eventid`, `host`, `sendto`, `status`, `error`, `clock_unix` |
| `get interfaces` | `interfaceid`, `host`, `type`, `ip`, `dns`, `port`, `available`, `error` |
| `diagnose host` | `host{}`, `open_problems[]`, `recent_events[]`, `latestdata[]`, `broken_items[]` |
| `get history` (extended) | add `--around-event`; output unchanged |
