# poolwarden — Functional Specification

| | |
|---|---|
| Document | 02 — Functional Specification |
| Status | Draft for review |
| Date | 2026-11-05 |
| Related | 01-design-spec.md (architecture, rules, data sources) |

Sample outputs below use illustrative values: hosts db1/db2 and the VIP 10.0.0.100. They are not real data.

---

## 0. Conventions (apply to every command)
- **Modes:** each command works in one-shot mode (`poolwarden <cmd>`) and inside the shell (`pgpool[prod-a]> <cmd>`).
- **Global flags:**
  - `-c/--config <file>` (default `/etc/poolwarden.yaml`)
  - `-o/--output table|wide|json` (default `table`)
  - `--nagios`
  - `--read-only`
  - `--no-color`
  - `--operator <name>`
  - `--ticket <id>`
  - `--yes` (skips the plain yes/no prompt only, never a typed confirmation)
  - `-q/--quiet`
- **Output:**
  - Tables have a header row.
  - Times are shown in local time with the offset. JSON uses UTC RFC 3339.
  - Bytes are shown in human units in table output and as raw integers in JSON.
  - Colour is used only on a TTY.
  - When stdout is not a TTY, no prompts appear and control commands need `--yes` plus `--confirm <node-id|token>`.
- **Severity marks:** `OK`, `WARN`, `CRIT`, `INFO`, shown as text (colour is optional).
- **Header line:** every view command starts with a one-line context header:
  `cluster prod-a · pgpool 4.5.x · local db1 (LEADER) · VIP on db1 · collected 12:03:01 (0.41s)`. If the tool is in degraded mode, `· DEGRADED` is added.
- **Errors** follow this format:
  ```
  error: cannot reach pgpool at 127.0.0.1:9999 (connection refused)
  cause: pgpool-II service not running on db1, or wrong port in /etc/poolwarden.yaml
  next:  systemctl status pgpool-II   |   poolwarden config self-check
  ```
- **Control command flow (C-flow):** every command marked C runs these steps in order:
  1. resolve identity
  2. check `allowed_operators` and `--read-only`
  3. ask for the operator name if this session doesn't have one yet
  4. ask for a ticket (optional; Enter skips)
  5. run the pre-flight
  6. show the plan and the pre-flight result
  7. confirm, by typing the token
  8. write the `requested` audit entry
  9. execute, with each step timed
  10. verify
  11. write the `finished` audit entry
  12. print the result

  Ctrl-C at any prompt stops the command and records it as `aborted`.
- **Exit codes:** listed in design spec §15.

---

## 1. Setup and meta

### 1.1 `init [--host <name>] [--non-interactive --from <file>]` (C, local only)
**Purpose:** create or update `/etc/poolwarden.yaml`. No path is hard-coded.
**Steps:**
1. Find pgpool.conf. Candidates are the `-f` argument of the running pgpool process (`/proc/*/cmdline`), `/etc/pgpool-II/pgpool.conf`, and `/etc/pgpool/pgpool.conf`. The operator chooses one or types a path.
2. Parse it. Show the detected `port`, `pcp_port`, `logdir`/`log_directory`, `log_line_prefix`, `pool_hba` and `pcp.conf` paths, and the `backend_*` entries. Each value is shown with its source line, and the operator can accept or override it.
3. Ask for `PGPASSFILE` (default `~postgres/.pgpass`) and `PCPPASSFILE` (default `~postgres/.pcppass`), then test both connections.
4. Discover peer pgpool hosts (watchdog) and PG hosts (backends). For each host, ask:
   - its roles
   - its `ssh_user` (`root`/`postgres`)
   - then run an SSH test, which checks that the key is in `known_hosts`
5. For each PG host, read `data_directory`, `log_directory`, `log_filename`, `log_destination` and `log_line_prefix` over SQL, and let the operator confirm or override each one. If `log_destination` is not `stderr`, warn that text-log parsing assumes stderr format.
6. Set thresholds (default 30/60 min), the audit file, syslog, `retention_days` (31) and `wal_volume_bytes`.
7. Show the whole config and ask for confirmation. Write it as root with mode 0600, backing up the old file if there is one.

**Output (summary):**
```
Config written: /etc/poolwarden.yaml  (backup: /etc/poolwarden.yaml.20261105T120301)
hosts: db1 [pgpool,pg] ssh=root  ✓   db2 [pgpool,pg] ssh=postgres  ✓
pgpool ✓  pcp ✓  watchdog ✓ (2 nodes)  backends ✓ (2)
```
**Audit:** yes (config file diff).

### 1.2 `config self-check`
Checks that every configured path exists on its host, that each `log_line_prefix` matches the live value, that SSH works to each host, that the pcp binaries are present, and that the config file's permissions are correct.
**Output:** a table with columns `CHECK  HOST  RESULT  DETAIL`. Exits with 10 if any check warns.

### 1.3 `version`, `help [cmd]`
`version` prints the poolwarden version and build info, plus the detected pgpool and PG versions. `help` shows a command's synopsis, flags, an example and its related commands.

### 1.4 Shell (`poolwarden` with no args)
- **Prompt:** `pgpool[<cluster>@<localhost>]>`. It changes to `pgpool[prod-a@db1 DEGRADED]>` in degraded mode.
- **Session start:** shows the header line, any CRIT findings, and asks once for the operator name (pre-filled from the SSH key mapping).
- **Completion:** commands, flags, node ids, host names, param names (from the catalogue) and finding ids.
- **History:** a separate file per operator.
- **Shell-only commands:** `exit`, `quit`, `clear`, `set output json`, `set operator <name>`.
- **Background sampler:** runs every `interval` (default 2s) while the shell is open, so forecasts can be made.

---

## 2. Monitoring views

### 2.1 `status`
A one-screen summary.
```
cluster prod-a · pgpool 4.5.x · local db1 (LEADER) · VIP on db1 · collected 12:03:01 (0.41s)

NODES   0 db1:5432 primary  up   score 100
        1 db2:5432 standby  up   delay 2s     score 95
WATCHDOG quorum: QUORUM EXIST · leader db1 · 2/2 alive · half-votes ON (2-node split risk: see WD_HALF_VOTES_2NODE)
POOLS   children 38/100 busy · clients 112
FINDINGS 0 CRIT · 1 WARN · 2 INFO        → poolwarden diagnose
```
**Exit:** 11 if there are CRIT findings, 10 if there are WARN findings, otherwise 0.

### 2.2 `nodes [id]`
Columns: `ID HOST:PORT STATUS PGPOOL-ROLE REAL-ROLE LB-WEIGHT SELECTS DELAY REPL-STATE SYNC LAST-CHANGE SCORE NOTE`.
NOTE shows `ROLE MISMATCH`, `UNREACHABLE` or `pgpool up / direct down` when they apply. With `id`, it shows a detail block for that node: everything above plus the timeline, the LSNs, conns/max_connections, the oldest transaction, conflicts, and the findings for that node.

### 2.3 `lag [--history 1h]`
```
primary db1:5432  wal 3/A1B2C3D4

STANDBY  STATE      SYNC   LAG-BYTES  WRITE  FLUSH  REPLAY  DELAY   TREND      ETA→CRIT
db2      streaming  async  12.4MB     0.1s   0.1s   0.3s    2s      steady     -
```
DELAY is coloured against the thresholds: ≥30m is WARN and ≥60m is CRIT. TREND is `rising`, `falling`, `steady` or `unstable`. `--history` adds an ASCII sparkline of the delay.

### 2.4 `slots`
Columns: `SLOT TYPE ACTIVE WAL-STATUS RETAINED ETA-DISK-FULL`.

### 2.5 `pools`, `procs`, `conns`
- `pools`: summary of SHOW POOL_POOLS, grouped by database and user.
- `procs`: SHOW POOL_PROCESSES; `procs <pid>` uses `pcp_proc_info`.
- `conns`: client connections by database, user and application on pgpool and on each backend, against the limits.

### 2.6 `watchdog`
Shows the cluster block (quorum, leader, alive/total, consensus params with their live values), a per-node table (`NODE HOST STATUS PRIORITY VIP-PRESENT`), and the VIP check per host. If the VIP is up on two hosts, it shows a CRIT banner.

### 2.7 `health`
POOL_HEALTH_CHECK_STATS per node: total/success/fail/skip/retry, average and maximum duration, and the last failure time, each with its change since the last sample.

### 2.8 `topology show|discover`
- `show`: a host map with roles, ssh_user, watchdog status, VIP and backend role.
- `discover`: prints the differences between the live cluster and the config, and offers to stage them into poolwarden's config. This step is audited.

### 2.9 `watch <view> [--interval 2s]`, `top`
- A full-screen refresh of `status`, `nodes`, `lag`, `pools`, `watchdog` or `errors`.
- Keys: `q` quits, `p` pauses, `+`/`-` change the interval, `1`–`5` switch view.
- `top` is the combined status, lag and findings screen.
- Text only. Uses the alternate screen buffer.

---

## 3. Understanding issues

### 3.1 `diagnose [--severity warn] [--rule ID]`
```
SEV   ID                         SUBJECT   SUMMARY
CRIT  STANDBY_NOT_STREAMING      db2       db2 missing from pg_stat_replication on db1
WARN  WD_HALF_VOTES_2NODE        cluster   2-node watchdog with half-votes on: network split can give both sides quorum
→ poolwarden explain STANDBY_NOT_STREAMING:db2
```
**Exit:** 11 if there are CRIT findings, 10 if there are WARN findings.

### 3.2 `explain <finding-id>`
Shows:
- **What:** the rule description
- **Evidence:** values with their source
- **Likely cause:** ranked list
- **Related errors:** the last 5 matching events
- **Fix:** the exact poolwarden commands
- **Reference:** a docs topic

### 3.3 `forecast`
Columns: `METRIC SUBJECT NOW SLOPE R² ETA THRESHOLD`. If there is less than 5 minutes of history, it prints `insufficient history (have 2m, need 5m)`. It does not guess.

### 3.4 `errors`
`errors [--since 1h] [--source pgpool|pg|pcp|health|replication|conn|db|tool] [--severity warn] [--host h] [--node N] [--grep re] [-f]`
Columns: `TIME SEV SOURCE HOST CODE MESSAGE`. With `-f`, it follows live (Ctrl-C stops).
- `errors summary [--since 24h]`: counts by source and code, with the first and last time seen.
- `errors show <id>`: the raw line with 10 lines of context before and after, related findings, and the explain text.

### 3.5 `logs`
`logs [--host h] [--component pgpool|pg] [--since 1h] [--grep re] [--classify] [-f]` shows raw lines. `--classify` adds a tag column: `FAILOVER`, `HEALTHCHECK`, `CHILD_EXIT`, `AUTH_FAIL`, `WD_ELECTION`, `CONN_FAIL` or `OTHER`.

---

## 4. Node control (all are C-flow commands)

### 4.1 `promote <id> [--gracefully] [--pgpool-only] [--force]`
1. **Pre-flight:** design spec §14.
2. **Plan display:**
```
PROMOTE node 1 (db2) — switchover via pcp_promote_node -s
current primary: node 0 (db1)  → will be marked down; failover_command will promote db2
follow_primary_command will re-point remaining standbys

PRE-FLIGHT                                          RESULT
target up & standby                                 OK
replay delay 2s < 30m                               OK
most advanced LSN among standbys                    OK
watchdog quorum                                     OK (QUORUM EXIST)
failover_command / follow_primary_command set       OK
no split-brain                                      OK
VERDICT: GO

Type the node id to confirm (1), or Ctrl-C to abort:
```
3. **Execute:** `pcp_promote_node -s [-g] -n 1`.
4. **Verify:** the tool polls every 2s, for up to 120s, until:
   - exactly one node is not in recovery and it is the target
   - pgpool's roles match the real roles
   - the remaining standbys are streaming from the new primary
5. **Result:** a timeline of each step with its time.
   - If verification times out, the tool exits with a failure and says `promotion state unclear` and `next: poolwarden nodes; poolwarden logs --classify --since 10m`.
   - It never rolls back automatically.

### 4.2 `detach <id> [--gracefully] [--force]`
Runs `pcp_detach_node [-g] -n <id>`. If the target is the primary, the plan shows `THIS TRIGGERS FAILOVER` and the confirmation token is `detach-primary-<id>`. Verify step: pgpool status changes to down.

### 4.3 `attach <id>`
Runs `pcp_attach_node -n <id>`. Verify step: status up, streaming, and delay below the warning threshold within 60s, otherwise WARN.

### 4.4 `recover <id>`
Runs `pcp_recovery_node -n <id>`. It can run for a long time: progress is shown from the pgpool log, and there is no time limit unless `--timeout` is given. Verify step: attached and streaming.

### 4.5 `reload [--local]`
Runs `pcp_reload_config -s c` (or `-s l` with `--local`).

---

## 5. Configuration management (C-flow for apply, rollback, load and user commands)

| Command | Behaviour |
|---|---|
| `config get <param>` | file value on each pgpool host, live value (`PGPOOL SHOW`), default, and a drift flag |
| `config list [--changed] [--drift] [--grep re]` | all params: `PARAM FILE LIVE DEFAULT CONTEXT DRIFT` |
| `config describe <param>` | type, default, range or enum, reload/restart, related cross-param rules, and a description from the catalogue |
| `config set <param> <value>` | validates the type and range. Stages the change and prints `staged: <param> <old> → <new> (reload)` |
| `config unset <param>` | stages a revert to the default |
| `config diff` | unified diff for each file, plus the operator who staged each change and when |
| `config validate` | catalogue and cross-param rules. Lists each rule as OK/FAIL. Exits 4 on failure |
| `config apply [--yes]` | flow in design spec §9. The plan shows hosts, order, reload or restart, VIP impact and lock state. Confirmation token: `apply`, or `restart` for restart-class changes |
| `config discard` | clears the staged changes; the operator confirms with y/N |
| `config history` | `VERSION TIME OPERATOR TICKET HOSTS SUMMARY` |
| `config rollback <version>` | restores that version on every host through the apply flow |
| `config load <file>` | parses the file, stages the differences from the current config, and shows the diff |
| `config export [file]` | writes the effective config (stdout by default) |
| `backend add\|remove\|set <id> [--host --port --weight --data-dir --flag]` | stages a backend change as one unit. Removing the current primary is refused |
| `hba list\|add\|remove` | `hba add --type host --db all --user all --addr 10.0.0.0/24 --method scram-sha-256 [--position N]` |
| `pcpuser list\|add\|passwd\|remove <user>` | password prompted twice without echo, hashed in-process |
| `pooluser list\|add\|passwd\|remove <user>` | password prompted without echo; uses pgpool's tool to write pool_passwd |

**Apply plan display (example):**
```
APPLY 2 change(s) to pgpool.conf on 2 host(s)
  health_check_period   30 → 10    reload
  num_init_children     100 → 120  RESTART  (CONN_BUDGET: 120×4=480 ≤ 497 OK)
order: db2 (standby) → wait rejoin → db1 (LEADER; VIP will move to db2)
backup: /var/lib/poolwarden/config/20261105T121500/
Type 'restart' to confirm:
```
**Failure output (example):** the per-host step table shows `db2 written ✓ restarted ✓ rejoined ✓ | db1 written ✓ restart ✗ (timeout)`, then `rollback: db1 restored ✓ restarted ✓ | db2 restored ✓ restarted ✓`, and exits with 5.

---

## 6. Ad-hoc SQL
`sql <primary|standby|node N|host h> "<single statement>" [--limit 1000]`
- The statement runs inside a READ ONLY transaction with a 30s timeout. It is sent through the extended protocol, which allows one statement only.
- Write statements are rejected by PostgreSQL. The tool reports the error and records it in the audit log.

---

## 7. Audit
| Command | Output |
|---|---|
| `audit [--since 24h] [--operator] [--action] [--node] [--ticket] [--outcome]` | `TIME OPERATOR ACTION TARGET TICKET OUTCOME DURATION` |
| `audit show <event_id>` | full entry: the identity signals, a timing breakdown, remote steps, the pre-flight result and the diff |
| `audit verify [--since]` | checks the hash chain across rotated files. Reports `OK: N entries` or lists the gaps and edits, and exits 11 if any are found |
| `audit sessions [--since]` | `START END OPERATOR FROM(IP) KEY-FP COMMANDS` |

**Timing breakdown in `audit show` (example):**
```
requested 12:15:00.112  confirmed 12:15:07.840 (+7.7s)  started 12:15:07.851
  db2  ssh root  connect 41ms  write 12:15:07.902-12:15:07.955  restart 12:15:08.010-12:15:11.300  rejoin 12:15:19.020
  db1  ssh -     local         write 12:15:19.050-12:15:19.090  restart 12:15:19.120-12:15:22.410  rejoin 12:15:30.800
finished 12:15:30.812  duration 22.96s  outcome success
```

---

## 8. Acceptance criteria (functional)
- AC1: Every command in this spec works in one-shot mode and in the shell, and `-o json` gives the same data as the table.
- AC2: No control command makes a change without a pre-flight result, a typed confirmation (or `--yes --confirm` when not on a TTY), and audit entries for both `requested` and `finished`.
- AC3: `config apply` never leaves the pgpool hosts with different config files. It either succeeds everywhere or rolls back everywhere, and reports exit 6 with recovery steps if that fails.
- AC4: An unreachable host or backend never makes a view command hang longer than the timeout plus 1s.
- AC5: `audit verify` detects any edited, removed or reordered line.
- AC6: No path or log format is hard-coded. Everything comes from the config written by `init`.
