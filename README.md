# poolwarden

A command center for one **pgpool-II** cluster. One static Go binary that a
DBA runs over SSH on a pgpool host to see what the cluster is doing, understand what is
wrong, and change it safely, without hand-chaining `psql`, `pcp_*` and config edits —
plus an optional browser console that renders the same data live.

## Why

Operating pgpool-II with streaming-replication PostgreSQL means juggling many separate
tools and files:

- `SHOW POOL_NODES`, `pcp_watchdog_info`, `pg_stat_replication`, `pg_is_in_recovery()` on
  every backend, and they do not always agree with each other.
- `pcp_promote_node`, `pcp_detach_node`, `pcp_recovery_node`: powerful commands with no
  pre-flight checks, no confirmation and no record of who ran them.
- `pgpool.conf`, `pool_hba.conf`, `pcp.conf`, `pool_passwd` edited by hand on every pgpool
  host, with drift between hosts discovered the hard way.
- Logs on several machines in several formats, and a shared `postgres` OS account that
  makes "who did this?" unanswerable.

poolwarden puts all of that behind one CLI (interactive shell, one-shot commands, live
`watch` views) and adds what the raw tools lack:

| | |
|---|---|
| **See** | `status`, `nodes`, `lag`, `watchdog`, `slots`, `pools`, `health`: pgpool's view cross-checked against what each PostgreSQL node says about itself |
| **Understand** | `diagnose` / `explain`: 29 rules (split-brain, role mismatch, lag, VIP, quorum, connection budget…) with evidence, likely cause and the exact next command; `errors` and `logs` from every host in one stream; `forecast` with ETAs |
| **Control safely** | `promote`, `detach`, `attach`, `recover`, `reload`, `service`: pre-flight checklist → plan → typed confirmation → execute → verify, every step timed |
| **Change config as a transaction** | `config set` → `diff` → `validate` → `apply` across all pgpool hosts, atomic writes, backups, reload or ordered restart, all-or-nothing rollback |
| **Prove it** | A hash-chained audit log that identifies the person (SSH key fingerprint, sudo user, declared operator) even on a shared account, with `audit verify` |

It is a wrapper, not a replacement: promotion only ever goes through `pcp_promote_node`,
pgpool keeps reading its own files, and nothing sits in the client traffic path.

## Web console (optional)

`poolwconsole` is a second static binary that serves an embedded React SPA plus a REST/SSE
API. It shells out to `poolwarden` for every read and every mutation — the browser never
bypasses preflight, confirmation, verification or the audit log. CLI-only installs never
need it (no Node, no frontend assets).

```
browser ── SSE live stream ──▶ poolwconsole ── allowlisted argv ──▶ poolwarden CLI ──▶ pgpool/PG/SSH
        ◀── plan / confirm / progress / verify ──
```

- **Live monitor stream** — one shared `poolwarden monitor` process fans out a snapshot
  every ~2 s over SSE: nodes, roles, watchdog quorum, pools, lag, findings, logs and errors.
  Rates are computed from real deltas between snapshots (WAL bytes/s, replay bytes/s,
  select qps) — nothing is simulated; unavailable values render as `-`, never as guesses.
- **Two-tier topology** — the data flow drawn the way the cluster actually works:

```
clients ──▶ VIP ──▶ [db1·pgpool] ⇠⇢ [db2·pgpool] ⇠⇢ [db3·pgpool]     ← watchdog
                            │ writes  (to primary)                        heartbeats
                            └ reads   (load-balanced)
                    [db1·pg]      [db2·pg]      [db3·pg]             ← WAL arcs:
                                                                primary → standbys
                                                                B/s + lag per edge
```

- **Administration** — detach/attach/promote/recover/reload, pgpool config
  set→diff→validate→apply→rollback, and `service start|stop|restart|status` for the
  pgpool/PostgreSQL/watchdog units on each host. Every action runs plan → typed
  confirmation → live progress stream → verification, journaled so a browser disconnect
  doesn't lose the operation.
- **Auth** — access token → session cookie, CSRF-protected mutations. Operator name is
  recorded in the poolwarden audit log for every action.

## Status

Targets pgpool-II 4.4/4.5 and PostgreSQL 14+. Exercised on Docker test beds (pgpool-II
4.5.13, PostgreSQL 18) through full failover, online recovery, switchover, detach/attach,
VIP move, service stop/start and configuration transactions — including leader loss,
quorum collapse and pgpool-driven auto-recovery.

## Get started

    make build                 # bin/poolwarden (static, CGO_ENABLED=0)
    sudo poolwarden init       # writes /etc/poolwarden.yaml
    poolwarden status          # one-shot view
    poolwarden                 # interactive shell: pgpool[cluster@host]>

Optional console:

    make web console           # web/dist + bin/poolwconsole
    poolwconsole --addr 127.0.0.1:8080 --token <secret> --cli /usr/bin/poolwarden

Selected additions to the classic command set: `monitor` (NDJSON snapshot stream feeding
the console), `probe` (per-endpoint connection checks for the setup wizard),
`config install` (atomic audited config write), `service <verb> <unit> <host>` (remote
start/stop/restart/status via the configured service manager), `--plan` and `--progress`
on every mutating command.

Command reference: `docs/commands/`.
