# Changelog

All notable changes to poolwarden are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **Web console** (`poolwconsole`): optional second binary serving an embedded React SPA
  and REST/SSE API on top of the CLI. Token → session-cookie auth with CSRF protection;
  every read and mutation executes through validated `poolwarden` argv — nothing
  bypasses preflight, confirmation, verification or the audit log.
- **`poolwarden monitor`**: shared NDJSON snapshot/error/log stream that fans a single
  collection out to every console client.
- **Live rates in monitor snapshots**: WAL generation B/s, per-standby replay B/s and
  per-node select qps computed from real LSN / select-count deltas between ticks;
  `interval_ms` is reported and unavailable values stay `-` rather than being guessed.
- **WAL inventory**: WAL directory size and retained file count via `pg_ls_waldir()`,
  replication slot–retained bytes.
- **`poolwarden service <start|stop|restart|status> <pgpool|postgresql|watchdog> <host>`**:
  remote service administration through the configured service manager — preflight
  (host role, SSH, last-pgpool and leader/primary warnings), typed confirmation,
  progress events, status verification, audit. Unit names configurable
  (`pgpool.service`, `pg.service`).
- **`poolwarden probe`** — per-endpoint connectivity checks for the setup wizard.
- **`poolwarden config install`** — atomic, audited configuration install (wizard path).
- **`--plan`** on mutating commands: run preflight + emit the confirmation token without
  executing.
- **`--progress`**: NDJSON step events for preflight/execute/verify.
- **Frontend** (`web/`): React + TypeScript + Vite + Tailwind SPA — Overview, two-tier
  data-flow topology (watchdog arcs above pgpool tier, routing edges in the gap, WAL
  arcs below the PG tier, per-edge rates and lag), Replication, Nodes, Pools, live
  Logs/Errors, Operations with plan→confirm→progress dialogs, service control panel,
  Configuration wizard + pgpool config transactions, Audit.
- **Connection state column**: `STATE` in `conns` view and Pools page —
  active / idle / idle in transaction from `pg_stat_activity`; pgpool-side pooled
  connections shown as `-`.
- **`GET /api/me`** session probe; SPA routes to login on expired/missing session.
- Docs/man regenerated for the new commands; `AGENTS.md`, `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `CHANGELOG.md`, `LICENSE` (MIT).

### Fixed

- **Blank-page crash**: Go `nil` slices marshalled as `null`; a null `watchdog.nodes`
  during leader election crashed the SPA permanently. All result slices now emit `[]`,
  and the frontend guards every array access.
- **`remote.Exec` exit-code handling**: nonzero remote exits arrive in `ExecResult.Exit`
  with a nil error — service verification no longer misreports stopped services as
  active.
- **Truthful labels**: cumulative `client_connection_count` is now labelled "conns
  served" (lifetime), and live capacity uses busy children / `num_init_children` plus
  `max_pool` — the old label implied thousands of live clients.
- **Topology edge routing**: edges between non-adjacent nodes rendered behind cards;
  replaced the stacked-column layout with the two-tier design and per-edge handle
  slots (scales to any node count).
- **Topology now shows PG role and watchdog role separately** per host (PG PRIMARY /
  STANDBY next to WD LEADER / STANDBY), plus the actual `pg_stat_replication` state —
  pgpool's health-check verdict and real PG state are no longer conflated.

### Changed

- `dev/testbed/` is no longer tracked (gitignored) — the Docker test bed stays local;
  see CONTRIBUTING.md for how to build your own.

### Repository

- `README.md` rewritten for the console + data flow; MIT `LICENSE` added;
  `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `CHANGELOG.md` added.
