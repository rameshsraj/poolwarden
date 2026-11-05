# pgpool-II Command Center CLI (`poolwarden`)

## Context
DBAs need one place to watch and control a pgpool-II cluster: backend status and roles, replication lag, pools and connections, and watchdog state. Today this takes many separate `psql`/`pcp_*` commands. The servers are headless Linux, so this is a **100% CLI command center** with no GUI and no mouse. It is one static Go binary used over SSH, covers **one cluster**, and every check and control action goes through it. It also goes beyond manual checks with diagnosis, forecasting and safety pre-flights.

## Name
`poolwarden` (decided, D1). It has not yet been checked for clashes with existing open-source projects or internal tools.

## Interaction model
1. **Interactive shell** (`poolwarden` with no args): a `pgpool[cluster]>` prompt with tab completion, history, and context-aware help. This is the command center.
2. **One-shot subcommands**, the same command tree (`poolwarden nodes`, `poolwarden promote 1`), so it can be scripted and used from cron. `-o table|json|wide` output.
3. **Live watch mode** (`watch nodes`, `watch lag`, `top`): a full-screen text refresh, like `top`. Exit with `q`. Plain text only.

## Command tree
```
init [--host h]                setup wizard: paths, topology, SSH user, thresholds (no hard-coded paths)
status                         one-screen summary: nodes, roles, lag, findings count, health scores
nodes [id]                     SHOW POOL_NODES + direct role check + health score
lag                            per-standby byte/second lag, trend, catch-up ETA
slots                          replication slots, retained WAL, time until pg_wal fills
pools | procs | conns          POOL_POOLS / POOL_PROCESSES / client connections by db/user
watchdog                       leader, quorum, VIP holder (pcp_watchdog_info)
health                         POOL_HEALTH_CHECK_STATS, POOL_BACKEND_STATS
diagnose                       run all diagnosis rules -> findings with cause + next step
forecast                       lag, slot/disk, connection exhaustion projections
attach|detach|promote <id>     PCP actions, each with a pre-flight checklist + typed confirmation
topology show|discover         host inventory, roles, watchdog leader, VIP holder
errors | logs                  unified error view, log tail/classify (see below)
recover <id>                   pcp_recovery_node (online recovery), pre-flight + confirm
reload                         pcp_reload_config
proc <pid>                     pcp_proc_info for one child process
audit ...                      who did what and when (see Audit trail)
watch <cmd> | top              live refresh view
sql <primary|standby N> "..."  read-only ad-hoc query on a backend (read-only transaction, statement_timeout 30s, 1000-row cap)
```

## Configuration management (no manual file editing)
The DBA never opens `pgpool.conf`, `pool_hba.conf`, `pcp.conf` or `pool_passwd` in an editor. All changes go through staged transactions:
```
config get <param>                       current value: file value and live value (`PGPOOL SHOW <param>`)
config list [--changed] [--grep x]       all params, marking non-default and file/live drift
config describe <param>                  type, default, allowed range, and whether it needs reload or restart
config set <param> <value>               stages a change (validated against the param catalogue)
config unset <param>                     stages a revert to the default
config diff                              shows staged changes as a unified diff
config validate                          type and range checks and cross-param rules from the catalogue. A pgpool-native
                                         dry-run parse is used only if one exists (to verify: V1)
config apply [--reload|--restart]        backup -> atomic write (tmp + rename, owner/mode kept) -> reload or restart
                                         as the changed params need -> verify live values -> auto-rollback on failure
config discard                           drops staged changes
config history | config rollback <ver>   versioned backups (/var/lib/poolwarden/config/<ts>/), restore one + reload
config load <file>                       imports a full pgpool.conf as staged changes (diff shown, then apply)
config export [file]                     writes the current effective config
backend add|remove|set <id> ...          manages backend_hostnameN/portN/weightN/data_directoryN/flagN as one unit
hba add|remove|list                      pool_hba.conf rules
pcpuser add|passwd|remove                pcp.conf (md5 hash computed in-process, password prompted with no echo)
pooluser add|passwd|remove               pool_passwd (pg_md5 / AES via pgpool's own tool)
```
- **Parser** (`internal/pgconf/`): a round-trip parser that keeps comments, ordering and blank lines. `set` changes a commented default line in place, or adds a new line in a "managed by poolwarden" block.
- **Param catalogue** (`internal/pgconf/catalog_4x.go`): name, type, default, range and reload/restart context for each param, built from the pgpool-II docs for the installed major version. pgpool's `pgpool_adm` and SHOW output do not give context the way `pg_settings` does, so this catalogue needs to be checked against the version you run.
- **Watchdog peers:** if the cluster has more than one pgpool node, `apply` pushes the same change to each peer over SSH (key-based, hosts set in config). It applies to standby pgpool nodes first and the leader last.
- **Multi-host failure policy:** before any write, every host is checked to be reachable and its current file is backed up. If a later host fails, the hosts already changed are rolled back to their backups (exit 5). If a rollback itself fails, it exits 6 and prints the exact per-host state and recovery commands. The result is all-or-nothing, so peers are never left on different configs.
- **Restart-class changes with watchdog:** the leader is restarted last. poolwarden warns that the VIP will move and can hand over leadership first (to verify how in 4.4/4.5: V5). Waiting for the peer to rejoin is checked before continuing.
- **Locking:** a host-wide lock `/var/lib/poolwarden/lock` (flock), plus a cluster lock taken on every pgpool host over SSH during `apply`, so two DBAs, or one DBA on each host, cannot change config at the same time. Staged changes are kept in `/var/lib/poolwarden/staged.json`, tagged with the operator who staged them.
- **Safety:** every `apply` and `rollback` is audited with the diff. `--read-only` blocks it. A restart-class change warns that client connections will drop and needs typed confirmation.

## Understanding issues
- `diagnose` (the rules below) plus `explain <finding-id>`: the evidence, the likely cause, the pgpool/PG docs topic, and the exact `poolwarden` commands to fix it (for example `config set health_check_period 10` followed by `config apply`).
- `logs [--since 1h] [--grep] [--classify]`: tails pgpool.log and the backend PG logs (paths in config, or remote over SSH), and tags events such as failover, health-check failure, child exit, auth failure and watchdog election. `diagnose` links findings to nearby log events to build a short event timeline.
- `promote <id>` stays the full flow: pre-flight → confirm → `pcp_promote_node` → re-check roles and that the other standbys follow the new primary → report. What `pcp_promote_node` does in 4.4/4.5 (switchover option, whether it runs failover/follow_primary commands or only changes pgpool's view) must be verified before this flow is final (V2).

## Error checking from the CLI
`errors` is the one place to see everything that went wrong, from every source, in one view:
```
errors [--since 1h] [--source pgpool|pg|pcp|health|replication|tool] [--severity] [--node N] [-f]
errors summary                 counts by source and type for the window (for example 12 auth failures, 3 health-check timeouts)
errors show <id>               full context: the raw log lines before and after, related findings, explain text
```
Sources, normalised into one `ErrorEvent{ts, source, node, severity, code, message, raw, ref}`:
| Source | How it is collected |
|---|---|
| pgpool.log | tail and parse ERROR/FATAL/WARNING/PANIC lines, plus known patterns (failover, health check, child exit, `connection to backend failed`, watchdog). `log_line_prefix` is detected from config. |
| PostgreSQL logs on backends | `pg_read_file` on `log_directory` (needs `pg_read_server_files`), or SSH tail. The csvlog/jsonlog formats are parsed when they are enabled. |
| Health checks | `SHOW POOL_HEALTH_CHECK_STATS`: the change in fail/retry/timeout counts between samples |
| Replication | WAL receiver gone, the `pg_stat_replication` row missing, `pg_stat_database_conflicts` changes, slot `wal_status = lost` |
| PCP | non-zero exit plus stderr from each pcp_* call that poolwarden makes |
| Connections / auth | connection refusals, `too many clients`, auth failures (from logs); pgpool child exhaustion (all children busy) |
| Database errors | `pg_stat_database` changes: deadlocks, rollbacks spike, temp-file spills |
| The tool itself | connection, timeout and parse failures, written to `~/.poolwarden/tool.log` |

`errors -f` follows live. `diagnose` reads from the same event stream, so a finding links straight to its evidence.

## Setup: `poolwarden init` (nothing hard-coded)
An interactive wizard creates `/etc/poolwarden.yaml` (root-owned, mode 0600). It can be re-run, and `--host <name>` re-runs it for a single host.
1. **Local pgpool:** asks for the pgpool.conf path. The default comes from the running pgpool process (`/proc/<pid>/cmdline -f`) or common locations. It then reads `port`, `pcp_port`, `logdir`/`log_directory`, `log_line_prefix` and `backend_*` from that file.
2. **Connection test:** connects to pgpool and PCP as `postgres` using `PGPASSFILE`/`PCPPASSFILE`, which are asked for, with `~postgres/.pgpass` as the default.
3. **Topology:** discovers peer pgpool hosts (`pcp_watchdog_info`) and PG backends (`backend_hostnameN`). For each host it asks for the roles, the `ssh_user` (`root` or `postgres`), and confirms the SSH key login with a test.
4. **Per-PG-host paths:** reads `data_directory`, `log_directory`, `log_filename`, `log_line_prefix` and `log_destination` from the backend (`SHOW`), and lets the DBA confirm or override each one.
5. **Thresholds and audit:** lag warn/crit (default 30/60 min), audit file path, syslog on/off, and retention (default 31 days).
6. **Summary:** shows the whole config for confirmation, then writes it.

Each value auto-detected during setup is shown with its source (for example "from pgpool.conf line 212") and can be overridden. `poolwarden config self-check` later re-checks that every configured path still exists and that each `log_line_prefix` still matches the live setting, and warns on drift.

## Audit trail: who did what, and when
**The problem:** DBAs log in with a shared, generic OS account (for example `postgres`), so the OS user alone cannot say who made a change. poolwarden collects several identity signals. None of them is perfect on its own, but together they let an auditor tell people apart.

**Identity signals, strongest first** (each one that is available is recorded):
| Signal | Where it comes from | Why it helps | Needs |
|---|---|---|---|
| SSH key fingerprint | `ExposeAuthInfo yes` in sshd puts the key used for login in `$SSH_USER_AUTH` (OpenSSH 7.6+, to verify for your OS) | Each DBA has their own key, even on a shared account | One-line sshd change + one key per DBA in `authorized_keys` (with a comment naming the person) |
| Login UID | `/proc/self/loginuid` (Linux audit) | Stays the same after `sudo`/`su`, so it shows the original login user if DBAs log in as themselves first | Personal login accounts, auditd/pam_loginuid enabled |
| `SUDO_USER` | environment | Original user when the DBA ran `sudo -u postgres` | sudo-based access |
| Source address | `$SSH_CONNECTION` | Which workstation/jump host the session came from | none |
| Declared operator | prompted once per shell session ("Your name/ID:"), or `--operator` | Readable name. It is self-declared, so it is recorded next to the technical signals, not instead of them | none |
| TTY, PID, hostname | OS | Ties entries to a session | none |

The mapping from key fingerprint to person comes from `operators:` in the poolwarden config (`fingerprint → name`). If the key matches a known operator, that name is used and compared with the declared name. A mismatch is flagged in the entry.

**Recommendation for the platform team:** give each DBA their own SSH key for the generic account, and turn on `ExposeAuthInfo`. That gives strong attribution without changing the shared-account model. This needs to be agreed with Security/Infra (open item O9).

**What every audit entry contains** (one JSON line per event):
- `event_id`, `session_id` (one per shell session or one-shot run, so entries can be grouped), `seq`
- **Timings:** `requested_at` (UTC, RFC 3339, milliseconds), `confirmed_at` (when the DBA typed confirmation), `started_at`, `finished_at`, `duration_ms`. For multi-host operations, also a `steps[]` list with host, action, start/end and result for each step, so you can see exactly when each pgpool host was changed or reloaded.
- **Who:** all identity signals above, plus the `operator` resolved from them
- **What:** command line (secrets masked), cluster, target node/host, `ticket` (optional)
- **Context:** pre-flight result (GO/WARN/NO-GO with each check), whether `--force` was used
- **Change:** for config, the unified diff plus before and after values per parameter. For node actions, the node state before and after.
- **Outcome:** success, failure, or rolled back; exit code; error text; backup version created
- `poolwarden_version`, `host`, and `prev_hash` / `hash` (see below)

**What is audited:** every control command (attach/detach/promote/recover/reload/config apply/rollback/backend/hba/pcpuser/pooluser), including attempts that were refused, aborted at confirmation, or blocked by pre-flight. Read-only commands are not audited by default (`audit.read_commands: true` turns them on). Shell session start and end are recorded.

**Integrity and storage:**
- The local file `/var/lib/poolwarden/audit.jsonl` is append-only and opened with `O_APPEND`. Each entry contains the SHA-256 of the previous entry (a hash chain), so a deleted or edited line is detected by `audit verify`.
- Each entry is also sent to syslog/journald (tag `poolwarden`), so your existing log shipping can copy it off the host. A user with the generic account could delete the local file, but not the copy that has already left the host.
- An entry is written *before* the action ("requested") and updated by a second entry *after* it ("finished"), so a crash in the middle still leaves a record.
- **Retention period and immutability requirements are for Compliance to set**, and are not decided by this design (open item O10). This is not regulatory advice. Check it with Compliance against the regulations that apply to these systems.

**Audit commands:**
```
audit [--since 24h] [--operator x] [--node N] [--action promote] [--ticket CHG123]
audit show <event_id>          full entry incl. steps, diff, preflight
audit verify                   checks the hash chain; reports gaps or edits
audit sessions                 who had sessions open, from where, start/end
```

## Design decisions (to be fixed before coding)
**Phases:** 1. Design (now): close the open items, then write the per-command functional spec. 2. Development. 3. Testing on a separate machine (D18).

**Architecture.** `cli` (cobra commands) → `service` layer (one method per user action, which returns typed results and never prints) → adapters (`pgpool` SQL, `backend` SQL, `pcp` exec, `conffile` local/SSH, `logsrc` local/SSH). Renderers turn results into table or JSON. The shell and one-shot mode call the same service layer, so both behave the same.

**Where it runs and how it reaches things.**
- It runs on the pgpool host as the OS user that owns pgpool's files (usually `postgres`), or uses sudo for writing the config and running restarts (`sudo -n` with a sudoers rule we document).
- pgpool and backends are reached over TCP (pgx), PCP through the local pcp_* binaries, and remote files and logs over SSH (golang.org/x/crypto/ssh, keys only, known_hosts checked).
- Restart and reload run through a configurable command (default `systemctl reload|restart pgpool-II`), with `pcp_reload_config` used for reloads.

**Versions.** pgpool-II 4.4–4.5 and PostgreSQL 14+. The version is detected at startup (`SHOW pool_version`, `server_version_num`). Commands or params a version does not support are hidden or rejected with a clear message.

**State.** No database. Samples are kept in a ring buffer in memory. Backups go to `/var/lib/poolwarden/config/<ts>/` with a manifest (sha256 and diff). Staged config changes go to `~/.poolwarden/staged.json`, which is locked with `flock` so two DBAs cannot apply at once. The audit log is JSONL.

**Error model and exit codes.** 0 ok; 1 usage; 2 connection or auth failure; 3 pre-flight NO-GO; 4 validation failed; 5 apply failed and was rolled back; 6 apply failed and rollback also failed (critical, shows manual recovery steps); 10 = findings at warn, 11 = findings at crit for `diagnose`/`status` (for cron/scripts). `--nagios` mode maps these to the standard 0/1/2/3 and a one-line output. Every error message says what failed, why, and the next command to run.

**Concurrency and timeouts.** The collector queries all backends in parallel with client-side timeouts (`connect_timeout` plus a context deadline, default 3s). pgpool's SHOW commands do not honour `statement_timeout`. An unreachable node shows as `UNREACHABLE` and does not block the others. Ctrl-C cancels the command that is running, not the shell.

**Security.** No passwords in YAML (only .pgpass, PCPPASSFILE, or a no-echo prompt). Secrets are masked in config display and the audit log. `sql` runs only in read-only transactions with a timeout and row cap. Control commands can be limited to listed *operators* (resolved identity, see Audit trail), not OS users, because the OS account is shared.

**Degraded mode (when it matters most).** The tool's own connection to pgpool uses a child process. When all children are busy, the tool may not get in. It then falls back automatically: backend state comes from direct PG connections, and node state from `pcp_node_info` / `pcp_proc_info`, which do not use a pgpool child. The output is marked `DEGRADED: pgpool not answering SQL`, and that is itself a finding.

**Sample history.** Recent samples (default 24h, about 2s resolution, downsampled after 1h) are kept in a small local file `/var/lib/poolwarden/samples/` (rotating, size-capped). One-shot `forecast`, health-check deltas and `errors --since` then work without a running shell. Without history, these commands say so instead of guessing.

**Topology model (works for any layout).** The cluster is an inventory of *hosts*, and each host has roles: `pgpool`, `pg`, or both. This covers the current setup (2 hosts, each running pgpool and PG, watchdog, VIP), a layout where pgpool and PG run on separate hosts, and N pgpool nodes. The inventory comes from config (`hosts:` list). `topology discover` fills it in from `pcp_watchdog_info` (pgpool peers) and `backend_hostnameN` (PG nodes), then `topology show` prints the map: which host runs what, who is the watchdog leader, who holds the VIP, and which backend is primary. All commands target hosts by role, so they never assume pgpool and PG are on the same host.
- **2-node watchdog rules** (your current case, half-votes on, D14): show a standing WARN finding that a network split between the two pgpool hosts could give each side quorum. The finding explains the trade-off; it is a reminder, not something to fix. It also checks that the VIP is never up on both hosts and that `wd_escalation_command`/`wd_de_escalation_command` are set to release the VIP. When half-votes is off, it flags that losing one pgpool node blocks failover. Further checks: when `failover_when_quorum_exists` / `failover_require_consensus` combinations allow split-brain, when the VIP is up on more than one host (`ip addr` check over SSH on each pgpool host; the VIP holder is read from the host, not assumed to be the leader), and when watchdog/heartbeat settings differ between peers.
- **Co-located host rules:** when pgpool and PG share a host, warn that losing that host takes out both, and check that the shared resources (connections, memory) leave enough room for both.
- Config `apply` and `logs` go to every host that has the matching role.

**Assumptions (not yet confirmed):** remote access uses SSH keys as the generic account. Privilege is a scoped sudoers rule, or the generic account already owns the pgpool files (O1).

**Non-goals for v1:** multi-cluster, GUI or web, metrics export or alerting daemon, automatic remediation (the tool suggests fixes and the DBA runs them).

## Project layout (`~/Projects/poolwarden`)
```
cmd/poolwarden/main.go
internal/cli/          cobra command tree; shared by one-shot mode and the shell
internal/shell/        REPL (library to choose at development start): completion, history
internal/render/       table/json/wide output, colours (turned off when output is not a TTY), live watch screen
internal/config/       YAML + env; passwords from .pgpass / PCPPASSFILE only
internal/collector/    Snapshot: SHOW POOL_* via pgx; direct backend checks (pg_stat_replication,
                       pg_last_wal_*_lsn, pg_is_in_recovery, slots, conflicts, long txns)
internal/pcp/          runs the installed pcp_* binaries via os/exec (no shell), parses their output
internal/diagnose/     rule engine -> Findings{severity, evidence, cause, next_command}
internal/forecast/     rolling samples + linear regression; health score 0–100
internal/preflight/    GO / WARN / NO-GO checklists for attach/detach/promote/recover
internal/service/      one method per user action; used by both shell and one-shot mode
internal/topology/     host inventory, discovery, role targeting
internal/pgconf/       round-trip config parser + param catalogue (4.4/4.5)
internal/remote/       SSH exec/file transfer, locking across hosts
internal/logsrc/       log tail/parse for pgpool + PG; feeds errors/
internal/errors/       ErrorEvent normalisation, summary, follow
internal/history/      on-disk sample ring for forecast/deltas
internal/audit/        identity resolution, hash-chained JSONL + syslog, verify
```

## Smart features
- **Diagnosis rules:** split-brain or role mismatch (pgpool's role vs `pg_is_in_recovery()`, more than one primary); pgpool status and direct reachability disagree; health-check failures; long or idle-in-transaction sessions, or an old xmin, holding back standbys; replay paused or WAL receiver missing; replication conflicts going up; inactive slots retaining WAL; `num_init_children × max_pool` larger than backend `max_connections`; `failover_command` not set.
- **Forecasting:** lag trend and catch-up ETA, time until the pg_wal volume fills (configured size or local `df`), time until child processes or connections run out, health score per node. Samples are kept in memory while the shell or a watch is running. One-shot `forecast` takes about 30s of samples.
- **Recover pre-flight** also checks that `recovery_1st_stage_command` (and any 2nd stage) is set and that the script exists on the primary.
- **Pre-flight:** promote checks that the target is a healthy standby with the most advanced LSN and lag under the threshold, and that quorum is OK. Detach checks that a healthy node will remain, shows the LB capacity lost, and warns if the target is the primary. Attach/recover check reachability, the timeline and that the node is in recovery. NO-GO needs `--force` and typing the node id. `--read-only` turns off all control commands. Every attempt is audited.

## Config (`~/.poolwarden.yaml` or `/etc/poolwarden.yaml`)
```yaml
cluster: prod-a
operators:                       # SSH key fingerprint -> person (audit)
  "SHA256:abc...": "Jane DBA"
hosts:                           # all paths per host (D20); written by `poolwarden init`
  - name: db1
    address: 10.0.0.11
    roles: [pgpool, pg]
    ssh_user: root                 # root | postgres (D21)
    pgpool: {conf: /etc/pgpool-II/pgpool.conf, hba: /etc/pgpool-II/pool_hba.conf, pcp_conf: /etc/pgpool-II/pcp.conf,
             log_dir: /var/log/pgpool-II, log_line_prefix: '%m: %a pid %p: '}
    pg:     {port: 5432, data_dir: /var/lib/pgsql/16/data, log_dir: /var/lib/pgsql/16/data/log,
             log_line_prefix: '%m [%p] '}
  - name: db2
    address: 10.0.0.12
    roles: [pgpool, pg]
    ssh_user: postgres
    # ... same keys
pgpool: {host: 127.0.0.1, port: 9999, user: postgres, dbname: postgres}
pcp:    {host: 127.0.0.1, port: 9898, user: pcpadmin, bin_dir: /usr/bin}
interval: 2s
lag: {warn_seconds: 1800, crit_seconds: 3600}   # D13
wal_volume_bytes: 100GB
audit: {file: /var/lib/poolwarden/audit.jsonl, syslog: true, read_commands: false, retention_days: 31}
```

## Verification
- A `dev/docker-compose.yml` test bed that matches production: 2 hosts, each running pgpool-II 4.5 and PG 16, with watchdog, a VIP and PCP. A second profile has separate pgpool and PG hosts (2 pgpool + 2 PG).
- Audit: run a promote and a config apply and check the entry fields and timings. Edit a line by hand and check that `audit verify` flags it. Log in with two different SSH keys on the shared account and check that the two operators are told apart.
- Unit tests: SHOW POOL_* and pcp output parsers (fixtures taken from the test bed), each diagnosis rule, the forecast maths and the pre-flight decisions.
- Manual: in the shell, run `status`, `lag` and `watch lag` while `pgbench` runs. Run `pg_wal_replay_pause()` on a standby and check that `diagnose` reports it and `forecast` shows lag growing. Stop a standby and check the down finding. Run `detach 2` and `attach 2` and check the pre-flight output, the audit line and `nodes`. Try `promote` on a lagging standby and check that it gives NO-GO.
- Config: run `config set num_init_children 40`, then `diff`, `apply` (restart-class, so it warns), then check `SHOW num_init_children`. Run `config set health_check_period 5` and `apply` (reload), and check it is live without a restart. Set an invalid value and check that validation rejects it. Use `rollback` to restore the previous version. Check that the comments in pgpool.conf are kept (golden-file tests too).
- Errors: kill a standby and check that `errors --since 5m` shows the health-check failures and pgpool log lines for it. Try a login with a bad password and check that `errors summary` counts the auth failure. Make a PCP call fail and check that it shows under `--source pcp`.
- Build: `CGO_ENABLED=0 GOOS=linux go build ./cmd/poolwarden`.

## Notes
- Still to write: a functional spec per command (exact output, sample screens, behaviour when output is not a terminal, help text), plus packaging (RPM/deb) and upgrade.
- Needs a monitoring role with `pg_monitor` and a PCP user. Detach, promote and recover cause real failover behaviour, so pre-flight, confirmation and audit logging are always on.

## Decisions log (design pre-checks)
| # | Topic | Decision | Date |
|---|---|---|---|
| D1 | Name | `poolwarden` | 2026-11-04 |
| D2 | UI | 100% CLI: interactive shell, one-shot subcommands, and live text watch views. No GUI or web. | 2026-11-04 |
| D3 | Scope | One cluster per config | 2026-11-04 |
| D4 | Versions | pgpool-II 4.4/4.5 and PostgreSQL 14+ only. The param catalogue and SHOW parsing target 4.4/4.5. | 2026-11-04 |
| D5 | Runs on | Each pgpool host. The DBA SSHes in and runs it locally. PCP and pgpool.conf are local, and peer pgpool hosts and PG hosts are reached over TCP/SSH. | 2026-11-04 |
| D6 | Topology | Any layout: pgpool and PG on the same or separate hosts, with 1–N pgpool nodes. Current production is 2 hosts, each running pgpool and PG, with watchdog and a VIP. | 2026-11-04 |
| D7 | Change control | Ticket/change ID is **optional** (`--ticket`, or prompted and skippable). It is recorded in the audit log when given. | 2026-11-04 |
| D8 | Development | No coding until design and functionality are signed off. The early prototype code in the repo is kept only as reference. | 2026-11-04 |
| D9 | Verification results | V1–V6 accepted as recorded, and their design impact adopted. Promote has two paths only: switchover via `pcp_promote_node -s`, and `--pgpool-only`. No `pg_promote`. | 2026-11-05 |
| D10 | OS / privilege | poolwarden runs as **root**, either directly or via `sudo`. Files it writes keep pgpool's owner and mode (for example `postgres:postgres 0600`). SSH to peer hosts uses key-based login (account to confirm, O15). Service control uses `systemctl` directly. DB/PCP connections still use the `postgres` DB user, with passwords from `PGPASSFILE`/`PCPPASSFILE` (default: `~postgres/.pgpass` and `~postgres/.pcppass`). | 2026-11-05 |
| D11 | DB account | `postgres` for both the pgpool connection and direct backend connections. Passwords come from `~postgres/.pgpass`. | 2026-11-05 |
| D12 | Logs | pgpool: `/var/log/pgpool-II/`, plain text. PG log path set per host in config. | 2026-11-05 |
| D13 | Lag threshold | Warn at 30 min, critical at 60 min replication delay. Time-based only. | 2026-11-05 |
| D14 | Watchdog consensus | Live values (2026-11-05): `enable_consensus_with_half_votes = on`, `failover_when_quorum_exists = on`, `failover_require_consensus = on`. This explains D17: with half-votes on, one surviving pgpool node (1 of 2 votes) has quorum, so failover goes ahead. The pgpool docs warn this setting carries a split-brain risk. | 2026-11-05 |
| D15 | Audit identity | Security agrees to one SSH key per DBA for `postgres`, with `ExposeAuthInfo yes` (closes O9) | 2026-11-05 |
| D16 | Audit retention | 1 month (from Compliance; closes O10). The local audit file is kept for at least 31 days. The syslog copy follows the central log retention. | 2026-11-05 |
| D17 | Observed failover behaviour | The DBA team reports that **failover does happen when one pgpool host is down**. Explained by the live settings in D14 (half-votes on). | 2026-11-05 |
| D18 | Testing | Done later on a separate machine, after design and development are complete. | 2026-11-05 |
| D19 | Module path | Deferred; keep the placeholder. | 2026-11-05 |
| D20 | Paths & log format | Nothing is hard-coded. All paths (pgpool.conf, pool_hba, pcp.conf, pgpool log dir, PG log dir, PCP bin dir, data dirs) and both `log_line_prefix` values are set per host in poolwarden's config. `poolwarden init` asks for them and offers auto-detected values as defaults. | 2026-11-05 |
| D21 | SSH account | Per host and configurable: `root` or `postgres` (`ssh_user`). Each remote step records its SSH account and its timings (connect, command start/end, duration) in the audit log. | 2026-11-05 |
| D22 | Watchdog | `use_watchdog = on` (confirmed). | 2026-11-05 |

## Open items (needed before sign-off)
None. All pre-check items are closed or deferred (D18, D19). Next: the per-command functional spec.

## Review notes on the decisions above
- **D10/D11 (root and superuser):** running as root means a bug could damage any file, so writes are limited to the configured pgpool config paths, the backup dir and poolwarden's state dir, and root refuses to run if the config file is writable by non-root users. Identity still works: `SUDO_USER` and `loginuid` show the person behind `sudo`, and the SSH key fingerprint shows who logged in directly as root.
- **D11 (DB superuser):** using `postgres` gives the tool full rights, so the `sql` command's protection has to come from the tool itself. It must allow one statement only, sent through the extended protocol so `COMMIT; ...` tricks are impossible, inside a read-only transaction with `statement_timeout`. Every `sql` call is audited.
- **D16:** whether 1 month meets your regulatory obligations is for Compliance to confirm. This design only implements the period it is given, and this is not regulatory advice.

## Verification results (checked 2026-11-05 against the pgpool-II 4.5 docs and the OpenBSD sshd_config man page)
| # | Finding | Design impact |
|---|---|---|
| V1 | **No config dry-run.** pgpool's options are `-a -f -F -k -h -d -n -x -C -D -m` and the `stop`/`reload` modes. None of them only parses the config. | `config validate` depends entirely on poolwarden's own param catalogue and cross-param rules. Before a restart, the backup and auto-rollback are the safety net. |
| V2 | **`pcp_promote_node` has two modes.** Without `-s`, it changes *only pgpool's internal view* and does not promote PostgreSQL (follow_primary_command still runs if set, and the docs advise turning it off). With `-s/--switchover`, it marks the current primary down and runs `failover_command` with the target as the new primary. `follow_primary_command` should be set. `-g` waits for clients to disconnect. The node must be `up` or `waiting`. The docs' intro and steps disagree on whether the non-switchover mode triggers failover, so test this on the test bed. | See the "Promote design" below |
| V3 | **`pcp_reload_config -s c|cluster`** reloads every pgpool node in the cluster. The default is `local`. | `config apply` (reload-class) writes the file to every pgpool host first, then runs one `pcp_reload_config -s c`, then checks the live values on each host |
| V4 | **`enable_consensus_with_half_votes`** (default off) only matters for an even number of nodes. With 2 nodes and it on, one live node is enough for quorum, but the docs warn of split-brain risk. With it off, quorum needs more than half the votes. `failover_when_quorum_exists` (default on) quarantines a failed backend when there is no quorum. `failover_require_consensus` (default on) needs votes from peers. | The 2-node diagnosis rule shows the trade-off rather than calling one setting wrong: off means no failover after losing one pgpool node (the backend is quarantined), and on means a split-brain risk |
| V5 | **There is no manual leadership handover command.** The leader is chosen by `wd_priority` and changes when the leader shuts down or resigns (network loss, failed consensus). `wd_de_escalation_command` runs on resignation. | For a restart-class change, restart the non-leader first, check it rejoins, then restart the leader. The VIP moves at that point, and the DBA is warned first. There is no "handover" step. |
| V6 | **`ExposeAuthInfo`** (default no) writes the authentication methods and public key used to a temp file, and its path is in `SSH_USER_AUTH`. The man page doesn't give the version it was added in, so check the OpenSSH version on the hosts. | Identity design stands: read the file pointed to by `$SSH_USER_AUTH`, take the key, compute its fingerprint, and map that to an operator |

## Promote design (updated)
`promote <id>` offers two paths. The pre-flight decides which ones are allowed:
1. **Switchover (default):** `pcp_promote_node -s [-g] -n <id>`. pgpool runs `failover_command`, which promotes the target, and `follow_primary_command` re-points the other standbys. Pre-flight: `failover_command` and `follow_primary_command` are set, the target is up/waiting and a standby, its lag is within the threshold, it has the most advanced LSN, and quorum exists.
2. **View-only:** `promote <id> --pgpool-only` runs `pcp_promote_node` without `-s`, for after the DBA has already promoted outside the tool. Pre-flight checks that the target is no longer in recovery.

After every path, the tool checks again: exactly one node reports not-in-recovery, pgpool's roles match the real roles, and the other standbys are streaming from the new primary. Each step is audited with timings.

## Still to verify on the test bed
- T1: The exact behaviour of `pcp_promote_node` without `-s` (the docs contradict themselves, see V2).
- T2: The OpenSSH version on production hosts supports `ExposeAuthInfo`.
