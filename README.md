# poolwarden

A terminal command center for one **pgpool-II** cluster. One static Go binary that a
DBA runs over SSH on a pgpool host to see what the cluster is doing, understand what is
wrong, and change it safely, without hand-chaining `psql`, `pcp_*` and config edits.

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
| **Control safely** | `promote`, `detach`, `attach`, `recover`, `reload`: pre-flight checklist → plan → typed confirmation → execute → verify, every step timed |
| **Change config as a transaction** | `config set` → `diff` → `validate` → `apply` across all pgpool hosts, atomic writes, backups, reload or ordered restart, all-or-nothing rollback |
| **Prove it** | A hash-chained audit log that identifies the person (SSH key fingerprint, sudo user, declared operator) even on a shared account, with `audit verify` |

It is a wrapper, not a replacement: promotion only ever goes through `pcp_promote_node`,
pgpool keeps reading its own files, and nothing sits in the client traffic path.

## Status

Implemented against the specs in `docs/01` to `docs/03` (decisions in `docs/04`), targeting
pgpool-II 4.4/4.5 and PostgreSQL 14+. Exercised on two Docker test beds (pgpool-II 4.5.13,
PostgreSQL 18) through full failover, online recovery, switchover, detach/attach, VIP
move and configuration transactions; see `docs/poolwarden-test-report.html`.

## Get started

    make build                 # bin/poolwarden (static, CGO_ENABLED=0)
    sudo poolwarden init       # writes /etc/poolwarden.yaml
    poolwarden status          # one-shot view
    poolwarden                 # interactive shell: pgpool[cluster@host]>

Operator guide: `docs/05-operator-guide.md`. Command reference: `docs/commands/`.
Architecture write-up: `docs/poolwarden.html`. Test report: `docs/poolwarden-test-report.html`.
