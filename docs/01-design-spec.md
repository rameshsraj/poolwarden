# poolwarden — Design Specification

| | |
|---|---|
| Document | 01 — Design Specification |
| Status | Draft for review |
| Date | 2026-11-05 |
| Related | 02-functional-spec.md, 03-implementation-prompt-spec.md, DESIGN.md (decision history) |

---

## 1. Purpose
poolwarden is a terminal command center for one pgpool-II cluster. DBAs use it to monitor, diagnose and control the cluster: node state and roles, replication lag, pools and connections, the watchdog and VIP, errors, and pgpool configuration. They never edit config files by hand or chain `psql`/`pcp_*` commands. It is a single static Go binary run on the pgpool hosts over SSH.

## 2. Goals and non-goals
**Goals**
- G1. Every pgpool-II health and control operation is available from one CLI (interactive shell, one-shot commands, and live watch views).
- G2. Go beyond raw output with a diagnosis engine, forecasting, and safety pre-flights before any change.
- G3. Configuration is changed only through staged, validated, backed-up, audited transactions across all pgpool hosts.
- G4. A tamper-evident audit trail that identifies the person and the exact timings, even on a shared OS account.
- G5. Works on any topology: pgpool and PG on the same or separate hosts, with 1–N pgpool nodes.

**Non-goals (v1)**
- No GUI or web UI. No multi-cluster support. No metrics exporter or alerting daemon.
- No automatic remediation: poolwarden suggests fixes and the DBA runs them.
- No `pg_promote`. Promotion only goes through `pcp_promote_node`.

## 3. Constraints (from the decisions log)
| Ref | Constraint |
|---|---|
| D2 | 100% CLI. Text only, keyboard only. |
| D3 | One cluster per config file. |
| D4 | pgpool-II 4.4/4.5 and PostgreSQL 14+. |
| D5 | Runs locally on each pgpool host. |
| D6 | Any topology. Production today: 2 hosts, each running pgpool and PG, with watchdog and a VIP. |
| D7 | Ticket ID is optional and is audited when given. |
| D10 | Runs as root (directly or via sudo). Files it writes keep pgpool's owner and mode. Uses `systemctl` directly. |
| D11 | DB and PCP connections use the `postgres` user. Passwords come from `PGPASSFILE`/`PCPPASSFILE` only. |
| D12, D20 | No hard-coded paths or log formats. Everything is set per host by `poolwarden init`. |
| D13 | Replication delay: warn at 30 min, critical at 60 min. |
| D14 | Live watchdog values: `enable_consensus_with_half_votes=on`, `failover_when_quorum_exists=on`, `failover_require_consensus=on`. `use_watchdog=on` (D22). |
| D15 | One SSH key per DBA, with `ExposeAuthInfo yes` turned on. |
| D16 | Audit retention: at least 31 days locally. Compliance sets the requirement; this design only implements it. |
| D21 | SSH account per host: `root` or `postgres`. Each remote step is timed and audited. |

## 4. Architecture

```
            ┌───────────────────────────────────────────────────────────┐
  DBA ──SSH─▶  poolwarden (root, on a pgpool host)                      │
            │                                                           │
            │  cli (cobra) ◀── shell (REPL) / one-shot / watch          │
            │        │                                                  │
            │        ▼                                                  │
            │  service  (one method per user action; returns typed      │
            │            results, never prints)                         │
            │   │     │        │          │          │         │        │
            │   ▼     ▼        ▼          ▼          ▼         ▼        │
            │ collector diagnose forecast preflight  pgconf   audit     │
            │   │                                     │                 │
            │   ▼                                     ▼                 │
            │ adapters: pgpoolsql  backendsql  pcp  remote(ssh)  logsrc │
            └───┼───────────┼───────────┼─────┼──────────────┼──────────┘
                ▼           ▼           ▼     ▼              ▼
             pgpool      PG nodes    pcp_*  peer hosts    log files
            (:9999)     (:5432)    (local)  (ssh)        (local/ssh)
```

**Layering rules**
- `cli` parses input and renders output. All logic lives in `service`.
- `service` never prints and never reads stdin. Confirmation is requested through a `Confirmer` interface supplied by `cli`.
- Adapters are the only code that does I/O to external systems. Each one sits behind an interface so it can be faked in tests.
- `render` turns typed results into `table`, `wide`, `json` or `nagios` output.

## 5. Components
| Package | Responsibility |
|---|---|
| `cmd/poolwarden` | entry point and exit code |
| `internal/cli` | command tree, flags, rendering calls, confirmation prompts |
| `internal/shell` | REPL: completion, history (`/var/lib/poolwarden/history/<operator>`), Ctrl-C handling |
| `internal/render` | table, wide, json, nagios output; colour only when writing to a TTY; full-screen watch |
| `internal/config` | poolwarden YAML: load, validate, permission checks (root-owned, not world-writable) |
| `internal/topology` | host inventory, role targeting, discovery |
| `internal/collector` | builds a `Snapshot`, in parallel and with timeouts, including degraded mode |
| `internal/history` | on-disk sample ring (24h; downsampled after 1h) |
| `internal/diagnose` | rule engine → `Finding` |
| `internal/forecast` | trend regression, ETAs, health score |
| `internal/preflight` | GO / WARN / NO-GO checklists |
| `internal/pgconf` | round-trip parser for pgpool.conf, pool_hba.conf, pcp.conf and pool_passwd; param catalogue for 4.4/4.5 |
| `internal/remote` | SSH exec and file transfer, known_hosts checking, cluster lock, per-step timing |
| `internal/pcp` | wrappers around the `pcp_*` binaries (os/exec, no shell) and their output parsers |
| `internal/logsrc` | tails and parses text logs using the configured `log_line_prefix` |
| `internal/errors` | `ErrorEvent` normalisation, summary, follow mode |
| `internal/audit` | identity resolution, hash-chained JSONL, syslog copy, verify |
| `internal/exitcode` | exit codes and the `Error{Code, Msg, Hint}` type |

## 6. Data model (core types)
```go
type Snapshot struct {
    Taken, Duration; Degraded bool; DegradedReason string
    Pgpool   PgpoolState   // version, nodes (SHOW POOL_NODES), processes, pools, health/backends stats
    Backends []Backend     // direct PG view per node
    Watchdog Watchdog      // pcp_watchdog_info -v + live consensus params
    VIP      []VIPCheck    // per pgpool host: is VIP present (ip addr over ssh)
    Settings map[string]string // selected PGPOOL SHOW values
}
type Backend struct {
    Host string; Port int; Reachable bool; Err string
    InRecovery bool; Version int; Timeline int
    CurrentLSN, ReceiveLSN string; ReplayPaused, WALReceiver bool
    ReplayDelaySec float64          // -1 = unknown
    Standbys []StandbyRepl          // primary only (pg_stat_replication)
    Slots []Slot; LongTxns int; OldestXactSec float64
    Conflicts int64; MaxConns, Conns int
}
type Finding struct {
    ID string            // stable rule id + subject, e.g. "REPL_LAG_CRIT:node1"
    Rule, Severity string // INFO | WARN | CRIT
    Subject string; Evidence []string; Cause string
    NextCommands []string; DocRef string; FirstSeen time.Time
}
type ErrorEvent struct { ID string; TS time.Time; Source, Host string; Node int
    Severity, Code, Message, Raw string; Ref string }
type PreflightResult struct { Action string; Verdict string /*GO|WARN|NO-GO*/; Checks []Check }
type AuditEntry  // see section 11
```

## 7. Topology model
- The inventory is a list of `Host{name, address, roles[pgpool|pg], ssh_user, pgpool{paths…}, pg{port, paths…}}`.
- Commands target hosts by role. Nothing assumes pgpool and PG share a host.
- Discovery sources: `pcp_watchdog_info -v` (pgpool peers) and `backend_hostnameN`/`backend_portN` from pgpool.conf (PG nodes).
- The local host is identified by matching its addresses to the inventory. Commands that must run locally (PCP) use it.

## 8. Data sources
| Data | Source | Notes |
|---|---|---|
| Node status/roles | `SHOW POOL_NODES` via pgpool, simple protocol | parsed by column name, not position (columns differ by version) |
| Processes/pools | `SHOW POOL_PROCESSES`, `SHOW POOL_POOLS` | |
| Health stats | `SHOW POOL_HEALTH_CHECK_STATS`, `SHOW POOL_BACKEND_STATS` | the change between samples gives the error counts |
| pgpool params (live) | `PGPOOL SHOW <param>` / `PGPOOL SHOW ALL` | syntax confirmed on production |
| Real role | `pg_is_in_recovery()` on each backend, direct | |
| Replication (primary) | `pg_stat_replication`, `pg_wal_lsn_diff`, `pg_replication_slots` | |
| Replication (standby) | `pg_last_wal_receive_lsn/replay_lsn`, `pg_last_xact_replay_timestamp`, `pg_is_wal_replay_paused`, `pg_stat_wal_receiver`, `pg_stat_database_conflicts` | delay = now() − last replayed transaction time |
| Held-back xmin | `pg_stat_activity` (xact_start > 5 min, idle in transaction) | |
| Watchdog | `pcp_watchdog_info -v -w` | parser accepts both "Leader" and "Master" wording |
| Node control | `pcp_attach_node`, `pcp_detach_node`, `pcp_promote_node [-s] [-g]`, `pcp_recovery_node`, `pcp_reload_config -s c`, `pcp_node_info`, `pcp_proc_info` | |
| VIP holder | `ip -o addr show` over SSH on each pgpool host | read from each host, never assumed from the leader |
| Logs | files under the configured dirs, local or over SSH | prefix-driven parser |

## 9. Configuration management design
- **Round-trip parser:** keeps comments, ordering, blank lines and quoting. `set` changes the existing line in place: it uncomments a commented default, or adds the line inside a `# --- managed by poolwarden ---` block.
- **Param catalogue (4.4/4.5):** name, type (bool/int/real/string/enum/list), default, min/max, allowed values, and reload-or-restart context. Cross-param rules (for example `num_init_children × max_pool ≤ backend max_connections − superuser_reserved_connections`).
- **There is no native dry-run in pgpool (V1)**, so validation relies on the catalogue alone.
- **Transaction flow:** stage (`/var/lib/poolwarden/staged.json`, tagged with the operator) → `diff` → `validate` → `apply`.
- **apply** (on all pgpool hosts):
  1. Take the cluster lock: a local flock plus a remote lock file on each peer over SSH.
  2. Check that every host is reachable and that the file on each host matches the expected checksum. If the files differ between hosts, stop and report the drift.
  3. Back up the file on every host (`/var/lib/poolwarden/config/<ts>/<host>/`, with a manifest containing sha256, operator and diff).
  4. Write atomically on every host (a temp file in the same directory, then fsync and rename), keeping the owner and mode.
  5. Reload-class changes: run `pcp_reload_config -s c` once (V3), then check the live value on every host with `PGPOOL SHOW`.
  6. Restart-class changes: restart the non-leader hosts first and wait for each to rejoin the watchdog. Restart the leader last, after warning that the VIP will move (V5: pgpool has no handover command).
  7. If any step fails, roll back every host that was already changed (exit 5). If the rollback also fails, exit 6 and print the state of each host and the recovery commands.
- `pool_hba.conf`, `pcp.conf` and `pool_passwd` use the same flow, each with its own parser. Passwords are prompted without echo and hashed in-process (md5 for pcp.conf). pool_passwd entries use pgpool's own tool (`pg_enc`/`pg_md5`).

## 10. Promote design
Only two paths are allowed:
1. **Switchover (default):** `pcp_promote_node -s [-g] -n <id>`. This triggers `failover_command` to promote the target, and `follow_primary_command` re-points the other standbys.
2. **`--pgpool-only`:** `pcp_promote_node -n <id>` without `-s`. It only changes pgpool's view, for use after a promotion done outside the tool. Its exact behaviour is still to be checked on the test machine (T1).

Afterwards, the tool checks that exactly one node reports not-in-recovery, that pgpool's roles match the real roles, and that the standbys are streaming from the new primary.

## 11. Audit design
**Identity signals**, every available one is recorded:
- SSH key fingerprint, read from the `$SSH_USER_AUTH` file (D15)
- `/proc/self/loginuid` mapped to a user name
- `SUDO_USER`
- `$SSH_CONNECTION` (source IP and port)
- an operator name the DBA declares once per session
- TTY, PID and hostname

The fingerprint is looked up in `operators:` in the config. If it doesn't match the declared name, the entry gets `identity_mismatch: true`.

**Entry fields:**
- `event_id`, `session_id`, `seq`, `phase` (`requested` | `finished`)
- **Timings:** `requested_at`, `confirmed_at`, `started_at`, `finished_at` (UTC, RFC 3339, milliseconds) and `duration_ms`
- **Remote steps:** `steps[]`, each with host, ssh_user, action, connect_ms, start, end and result
- **Identity:** `identity{}` (the signals above) and `operator`
- **What was run:** `command` (secrets masked), `cluster`, `target`, `ticket`
- **Safety:** `preflight{verdict, checks[]}` and `force`
- **Change:** `change{diff, before, after}`
- **Result:** `outcome` (`success` | `failed` | `rolled_back` | `aborted` | `refused`), `exit_code`, `error` and `backup_version`
- **Integrity:** `poolwarden_version`, `host`, `prev_hash` and `hash`

**What is audited:**
- every control command, including attempts that were refused or aborted
- every `sql` call
- shell session start and end
- read-only commands, only when `audit.read_commands` is set

**Storage:**
- Local file `/var/lib/poolwarden/audit.jsonl`, opened with O_APPEND, mode 0600.
- Each entry carries a SHA-256 hash chain linking it to the previous entry.
- A copy goes to syslog (tag `poolwarden`, facility `authpriv`).
- Files rotate daily. Rotated files are kept for at least `retention_days` (31). The hash chain continues across rotations because each new file starts with the hash of the previous file.

## 12. Diagnosis rules (v1 catalogue)
| ID | Severity | Condition |
|---|---|---|
| SPLIT_BRAIN | CRIT | more than one backend reports not-in-recovery |
| ROLE_MISMATCH | CRIT | pgpool's role ≠ the role the backend reports |
| NODE_DOWN | CRIT/WARN | pgpool status is down/quarantine (CRIT if it is the primary) |
| STATUS_DISAGREE | WARN | pgpool says up but the direct connection fails, or the reverse |
| REPL_LAG | WARN/CRIT | replay delay ≥ 30 / 60 min (D13) |
| REPL_LAG_GROWING | WARN | lag slope is positive and ETA to critical < 15 min |
| WAL_RECEIVER_MISSING | CRIT | standby is in recovery but has no WAL receiver |
| REPLAY_PAUSED | WARN | `pg_is_wal_replay_paused()` is true |
| STANDBY_NOT_STREAMING | CRIT | a standby is missing from `pg_stat_replication` |
| SLOT_INACTIVE | WARN | inactive slot retaining WAL; CRIT if `wal_status` is `lost`/`unreserved` |
| WAL_DISK_ETA | WARN/CRIT | forecast: pg_wal fills within 24h / 2h |
| XMIN_HOLDBACK | WARN | transaction > 5 min or idle in transaction on the primary |
| REPL_CONFLICTS | WARN | conflict counter went up since the last sample |
| HEALTHCHECK_FAILING | WARN/CRIT | fail count went up, or no successful check in N intervals |
| CHILD_EXHAUSTION | WARN/CRIT | busy children ≥ 80% / 95% of `num_init_children` |
| CONN_BUDGET | WARN | `num_init_children × max_pool` > backend `max_connections` − reserved |
| WD_QUORUM | CRIT | quorum state is not QUORUM EXIST / IS ON THE EDGE |
| WD_HALF_VOTES_2NODE | WARN (standing) | 2 nodes with half-votes on: a network split can give both sides quorum (D14) |
| VIP_DUPLICATE | CRIT | VIP is present on more than one host |
| VIP_MISSING | CRIT | VIP is present on no host |
| WD_ESCALATION_UNSET | WARN | `delegate_ip` is set but the escalation/de-escalation commands are empty |
| WD_PEER_CONFIG_DRIFT | WARN | watchdog/heartbeat params differ between peers |
| FAILOVER_CMD_UNSET | CRIT | `failover_command` or `follow_primary_command` is empty |
| RECOVERY_CMD_UNSET | INFO | `recovery_1st_stage_command` is empty (`recover` is unavailable) |
| CONFIG_DRIFT | WARN | file value ≠ live value, or the files differ between pgpool hosts |
| DEGRADED | WARN | pgpool is not answering SQL; the tool is using PCP and direct connections |
| SELF_CHECK | WARN | a configured path is missing, or a `log_line_prefix` doesn't match the live setting |
| COLOCATED_HOST | INFO/WARN (standing) | a host carries both the `pgpool` and `pg` roles: losing it removes a watchdog vote and a backend at once. WARN when that host is also the watchdog leader and runs the primary (one failure takes out the primary, the leader and the VIP together) |

Each rule is a pure function `func(Snapshot, History, Config) []Finding`, with table-driven tests.

## 13. Forecasting and health score
- **Method:** linear regression over the last 5 minutes of samples (at least 10 points). The output includes the slope, R², and an ETA to the threshold. The ETA is shown only when R² ≥ 0.6. Otherwise the trend is shown as "unstable".
- **Projections:**
  - replication delay and lag bytes
  - retained WAL against `wal_volume_bytes` (or `df` on a local PG host)
  - busy children and connections
- **Health score per node:** starts at 100, minus 50 if the node is down or unreachable, 30 for crit findings, 10 for warn findings, and 10 if the lag trend is rising. The minimum is 0.

## 14. Pre-flight checks
| Action | Checks (any NO-GO blocks the action unless `--force` and the node id is typed) |
|---|---|
| promote (switchover) | target is up or waiting and is a standby · replay delay < warn · target has the most advanced LSN of all standbys · watchdog quorum exists · `failover_command` and `follow_primary_command` set · no SPLIT_BRAIN · no other failover in progress |
| promote --pgpool-only | target reports not-in-recovery · no other node reports not-in-recovery |
| detach | at least one other healthy node will remain · LB capacity lost (%) · WARN if the target is the primary (this triggers failover) · quorum exists |
| attach | node reachable · in recovery · on the same timeline as the primary · streaming |
| recover | `recovery_1st_stage_command` set · script exists on the primary (SSH) · target is down in pgpool |
| config apply | validation passes · all hosts reachable · no file drift · restart-class changes need an extra typed confirmation |

## 15. Error model and exit codes
`0` ok · `1` usage · `2` connection/auth · `3` pre-flight NO-GO · `4` validation · `5` apply failed, rolled back · `6` apply and rollback both failed · `10` warn findings · `11` crit findings. With `--nagios`, these map to 0/1/2/3 and print one summary line. Every error prints `error:`, then `cause:`, then `next:` (the exact command to run).

## 16. Concurrency, timeouts and degraded mode
- The collector fetches from all sources in parallel. Each source has a timeout (`connect_timeout` plus a context deadline, default 3s). An unreachable source is marked UNREACHABLE and never blocks the others.
- **Degraded mode:** if the pgpool SQL connection fails or hangs (for example, all children are busy), node state comes from `pcp_node_info` and backend state from direct connections. The output is marked `DEGRADED`.
- In the shell, Ctrl-C cancels the command that is running, not the shell. A second Ctrl-C within 1s exits.
- History: a background sampler runs while the shell or a watch is active. One-shot commands read the on-disk history.

## 17. Security
- **Root hardening:**
  - Refuse to run if the config file is not owned by root or can be written by group/others.
  - Writes are allowed only to the configured pgpool paths and to `/var/lib/poolwarden`.
  - No temp files outside the target directory.
- **Secrets:**
  - Never kept in poolwarden's YAML.
  - Masked in output and in the audit log.
  - Password prompts don't echo.
- **SSH:** keys only, `known_hosts` is required (no trust-on-first-use), and the account is set per host (`root`/`postgres`).
- **`sql`:** one statement only (extended protocol, so no multi-statement strings), inside a `BEGIN READ ONLY` transaction, with a 30s `statement_timeout` and at most 1000 rows. Every call is audited.
- **`allowed_operators`:** limits control commands to resolved operator identities.

## 18. Deployment
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build`. Packaged as RPM/deb with these files:
  - `/usr/bin/poolwarden`
  - `/etc/poolwarden.yaml` (created by `init`)
  - `/var/lib/poolwarden/`
  - a logrotate snippet for the audit file
- External tools it needs on the host: the `pcp_*` binaries and `ip` (iproute2). `ssh` is not needed because it uses an in-process client.

## 19. Open and deferred items
- T1 (on the test machine): behaviour of `pcp_promote_node` without `-s`.
- T2: the OpenSSH version on the hosts supports `ExposeAuthInfo`.
- D18: testing is deferred. D19: module path is deferred.
