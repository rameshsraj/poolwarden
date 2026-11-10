# Poolwarden Console: requirements and implementation prompts

Status: implementation plan, not implemented.

## Confirmed requirements

- [ ] A visual browser dashboard with mandatory live monitoring and control.
- [ ] A configuration section accepting user-entered PostgreSQL and pgpool
  hosts, VIP, usernames, passwords, PostgreSQL ports, pgpool SQL ports, pgpool
  PCP administration ports, and connection settings.
- [ ] Clear live replication details and data-flow information.
- [ ] Frontend administrative commands execute through the existing CLI.
- [ ] CLI-only installation remains fully supported, without Node, a browser,
  frontend assets, or a running web service.
- [ ] SSE for live events; localhost binding and token authentication.
- [ ] One cluster per instance initially, with multiple pgpool/PostgreSQL hosts.

This plan supersedes the earlier proposal to call service actions directly from
HTTP handlers and to make frontend compilation mandatory for the CLI build.
Existing CLI behavior and service-layer rules remain authoritative for execution.

## Technology and deployment decisions

Use React, TypeScript, Vite, Tailwind CSS, shadcn/ui, TanStack Query, Apache ECharts
and React Flow. Use Go net/http for the optional web gateway, REST for commands,
and SSE for snapshots, findings, logs and operation progress.

Deliver two packages:

1. `poolwarden`: existing standalone static CLI.
2. `poolwarden-console`: optional Go web gateway with embedded frontend assets;
   requires a compatible installed CLI. No Node runtime required on the server.

Proposed command names and API contracts below are new work, not existing features.

```text
Browser --authenticated REST--> Console gateway
                                  |
                                  +--validated argv/private input--> poolwarden CLI
                                                                      |
                                                                existing services
                                                                      |
                                                               SQL / PCP / SSH

Browser <--SSE-- Console event hub <--structured events-- CLI monitoring process
Browser <--SSE-- Console operation supervisor <--progress-- CLI action process
```

The gateway must not execute SQL, PCP binaries, SSH commands or service mutations
directly. It invokes an explicitly configured CLI binary without a shell.
Use one long-running CLI monitoring process per console instance, shared by
subscribers. Add a machine event mode to the CLI rather than spawning a process
per metric, widget or browser. Keep monitoring and operation streams separate.

## Configuration experience

Provide a first-run wizard and an always-accessible Configuration page with
Connection Settings and Cluster Configuration tabs.

| Section | User input |
| --- | --- |
| Cluster | Name, environment label, inventory hosts, host roles |
| PostgreSQL | Host/address, port, database, username, password or existing credential reference, TLS mode and CA/client certificate references |
| pgpool SQL | Each host/address, SQL port, database, username, password/reference, selected monitoring endpoint |
| pgpool administration | PCP host/address, PCP port, PCP username, password/reference, PCP binary directory |
| VIP/watchdog | Expected VIP address, optional interface, watchdog hosts and ports where supported, expected membership |
| SSH | Per-host OS username, port, key reference, known-hosts reference, connection timeout |
| Host paths | pgpool configuration/HBA/password files, service name, PostgreSQL data directory, log locations |
| Monitoring | Collection interval, timeouts, lag thresholds, history retention |

Use clearly labeled defaults, with all applicable values editable. Support
co-located and separate pgpool/PostgreSQL hosts, non-default ports, IPv4, IPv6
and DNS names. User-entered inventory labels must not override observed roles.
Model per-host credentials and endpoint overrides where necessary; do not assume
the existing YAML schema already supports every field.

Connection Settings configure Poolwarden's access and expected topology.
Saving an expected VIP does not move a VIP or modify pgpool delegate_ip.
Cluster Configuration stages actual pgpool changes and applies them through
the CLI's validation, confirmation, backup and rollback workflows.

Wizard steps:

1. Enter cluster identity and host inventory.
2. Enter PostgreSQL, pgpool SQL and PCP endpoints and credentials.
3. Enter VIP/watchdog expectations and SSH/path settings.
4. Test each connection independently through a CLI probe command.
5. Show reachability, authentication, permissions and capability results.
6. Review a redacted configuration diff and save through the CLI.
7. Start monitoring; show explicit partial/degraded results if probes fail.

Passwords are write-only inputs with keep/replace/remove semantics. Never return
stored secrets to the browser or put them in argv, URLs, browser storage, logs,
audit payloads or command previews. Use restricted credential files/references
compatible with CLI-only use. Secret input travels over private stdin or a
restricted descriptor. Follow repository atomic-write and ownership conventions.
Do not describe filesystem permissions as encryption at rest.

## Dashboard and visualization specification

Desktop layout:

```text
Cluster / environment   Live status / last sample   Time window   Operations
Health   Primary   Replication lag   Connections   Quorum   VIP holder

Interactive topology and data flow                 Active findings
Clients -> VIP -> pgpool -> primary / standbys      Evidence and next actions

Replication lag and WAL charts                     Pool/connection utilization

Per-standby replication matrix                     Live activity timeline
```

Navigation: Overview, Topology, Replication, Nodes, Pools & Connections,
Logs & Errors, Operations, Configuration, Audit.

Visual requirements:

- [ ] High information density with clear hierarchy, readable labels and units.
- [ ] Dark/light themes; severity uses icons/text as well as color.
- [ ] Stable topology layout across updates; zoom, fit, legend and detail drawers.
- [ ] Different edges for client routing, WAL replication, watchdog membership
  and administrative access. Management connections are not database traffic.
- [ ] Show configured routes separately from observed activity. Potential
  read/write routing is labeled as configured/inferred unless measured.
- [ ] Edge animation indicates fresh observations only, never invented traffic.
  Respect reduced-motion settings.
- [ ] Clicking a node/edge shows endpoints, ports, evidence, source and timestamp.
- [ ] Source-specific unknown/stale states; unreachable nodes never look healthy.
- [ ] Responsive layout, keyboard navigation and accessible confirmations.

Replication details, where supported by observed telemetry:

- Primary and standby identity, pgpool-reported versus PostgreSQL-reported roles.
- Replication state, sync mode, sender/receiver status and timeline.
- Current/sent/write/flush/replay LSNs with source labels.
- Byte backlog between applicable LSN positions.
- Write/flush/replay lag and replay delay with precise definitions.
- WAL generation/replay rates derived from timestamped samples; invalidate rates
  across role changes, resets, missing samples and incomparable timelines.
- Replication slots, retained WAL, active status and disk-risk estimates.
- Per-standby charts, thresholds, incident annotations and last successful probe.
- Catch-up ETA only when history and model assumptions support it.

Do not equate idle replication with failure because a lag field is null. Never
subtract LSNs across incompatible timelines or show missing values as zero.
Distinguish sampled metrics from continuously followed logs and action events.

Data-flow view:

- Display clients -> VIP -> pgpool -> PostgreSQL routing and primary -> standby
  WAL direction, with legends and explicit evidence levels.
- Display VIP ownership, watchdog leader/quorum and role discrepancies.
- Show measured rates only where counters actually exist, with collection window
  and units. Poolwarden is outside the client traffic path and cannot claim
  packet tracing or per-query routing visibility from topology alone.
- Show failover observations over time, including unknown transition periods.

## Sequenced TODO prompts

Use the shared instructions below with each numbered prompt. Complete each
phase's acceptance checks before proceeding; preserve useful completed work.

### Shared implementation instructions

> Implement the requested phase in this repository. Read AGENTS.md and relevant
> code, tests, packaging and documentation first. Keep CLI parsing/rendering
> separate from service logic, and external dependencies behind interfaces.
> Preserve existing CLI behavior, confirmations, locks, atomic writes and audit.
> All frontend administration must invoke the CLI. Never execute free-form
> browser command strings. Do not use mock values in production live views.
> Keep changes focused; run checks relevant to the behavior changed and report
> what is verified versus unverified. Update the checklist in this plan only
> when acceptance criteria have actually been met.

### 1. Inventory and machine-interface contract

- [ ] Map existing commands, output schemas, configuration fields and capabilities.
- [ ] Identify gaps in JSON output, live progress, credential input and probes.
- [ ] Define a versioned CLI/gateway contract and compatibility handshake.

Prompt:

> Design and implement the initial machine-interface contract for the optional
> console. Inventory existing CLI actions and identify commands that currently
> print text despite JSON selection. Define typed snapshots, operation events,
> errors, capabilities and configuration DTOs. Include schema version, stream
> epoch, event sequence, observed timestamp, collection duration and freshness.
> Retain exitcode.Error codes and next hints. Distinguish warning/critical
> monitoring exit codes from transport or command execution failure. Document
> supported command mappings and missing capabilities. Add only the foundational
> contract changes needed for later phases; preserve ordinary CLI output.

Acceptance: documented mappings cover every planned screen/action; contract
checks reject incompatible CLI versions clearly.

### 2. CLI connection configuration and credentials

- [ ] Extend inventory/configuration types and validation for required user input.
- [ ] Add structured CLI setup/probe/read/update support where absent.
- [ ] Implement credential references and protected secret input.

Prompt:

> Implement user-managed connection configuration for the fields in this plan.
> Inspect existing init --from/--non-interactive behavior before extending it.
> Provide a structured CLI interface for redacted reads, validation, independent
> SQL/PCP/SSH probes and atomic updates. Add backward-compatible schema migration
> where required. Support endpoint overrides and write-only credential changes.
> Protect concurrent edits using a revision checked by the CLI. Return actionable
> per-endpoint probe errors without exposing secrets. Keep monitoring settings
> separate from applying actual pgpool or VIP changes.

Acceptance: existing configurations load; custom ports and distinct credentials
work; secret round trips are redacted; failed writes do not corrupt configuration.

### 3. CLI live monitoring and action progress

- [ ] Add a structured streaming monitoring command/mode.
- [ ] Collect once per cycle and derive views from a consistent snapshot.
- [ ] Add service progress interfaces and CLI event rendering.

Prompt:

> Add a versioned NDJSON machine event stream to the CLI for shared snapshots,
> findings, logs and errors. Reuse existing collector and follow sources.
> Schedule bounded collection without overlapping slow cycles; apply independent
> source timeouts and expose partial failures. Avoid multiplying history writes.
> Add an injected progress sink for actual service action stages; services never
> print. CLI rendering emits preflight, plan, step start/end, verification and
> terminal outcome events. Maintain existing human CLI behavior.

Acceptance: a CLI-only user can consume live structured events; slow/unreachable
sources produce degraded snapshots; operation events correspond to real steps.

### 4. Optional console gateway and operation supervisor

- [ ] Add the separate console executable and authenticated API.
- [ ] Add allowlisted CLI execution and a shared monitoring process.
- [ ] Add durable operation tracking and SSE reconnection.

Prompt:

> Implement the optional Go console gateway. Default to localhost and authenticate
> with a token exchanged for an HttpOnly session compatible with EventSource.
> Configure cookies for the supported localhost SSH-tunnel and HTTPS deployments;
> enforce origin/Host validation and CSRF protection for writes.
> Execute an explicitly configured CLI binary using validated argv, no shell.
> Share one monitoring process across clients; use bounded queues, heartbeat,
> event IDs, replay or explicit resynchronization, and restart backoff.
> Supervise admin subprocesses independently of browser connections. Persist
> operation IDs, redacted input, actor, progress and outcomes. Add idempotency
> keys and preserve CLI cluster locks across web and terminal users. Never retry
> a mutation automatically after a crash; reconcile interrupted operations with
> CLI audit and observed state, otherwise mark the outcome unknown.

Acceptance: unauthenticated requests fail; duplicate POSTs do not repeat actions;
two clients share collection; disconnect does not kill an operation; restart
cannot falsely report success or rerun a mutation.

### 5. Frontend shell and configuration wizard

- [ ] Create the React/TypeScript application and design system.
- [ ] Implement first-run setup and editable connection settings.
- [ ] Implement real CLI-backed connection tests and save flow.

Prompt:

> Build the frontend with the agreed stack and navigation. Implement the complete
> configuration wizard specified above, including dynamic host lists, per-host
> credentials, SQL versus PCP ports, VIP expectations and advanced SSH/path
> settings. Use clear validation, write-only secret fields, per-endpoint connection
> tests, a redacted review screen and revision-aware saving. Display CLI probe
> results and actionable hints. Keep entered passwords in transient form memory
> only and clear them after submission. Add empty, loading, partial-failure and
> first-run states.

Acceptance: a user can configure a cluster without hand-editing YAML, then use
the same configuration from the standalone CLI. No password appears in previews,
browser persistence or returned configuration.

### 6. Live overview and topology

- [ ] Implement dashboard cards, topology, findings and activity feed.
- [ ] Connect all views to real SSE state.
- [ ] Add freshness and reconnect behavior.

Prompt:

> Build the visual dashboard and topology specified in this plan. Use React Flow
> with stable positions and explicit edge legends. Show observed roles, VIP holder,
> watchdog quorum, utilization and findings. Use shared SSE state to update query
> caches; do not create one poller per widget. Show last successful sample and
> per-source freshness. Preserve the last known data with a stale label during
> outages. Provide evidence drawers and reduced-motion behavior.

Acceptance: real node/VIP/role changes reach the browser without refresh; a broken
stream visibly becomes stale; no fabricated metric or healthy fallback is shown.

### 7. Replication and data-flow analysis

- [ ] Expose missing replication telemetry through existing adapter interfaces.
- [ ] Implement per-standby matrix, charts and detailed flow drawers.
- [ ] Define rate, lag and ETA semantics.

Prompt:

> Implement the replication and data-flow requirements in this plan. First map
> each displayed field to existing telemetry, extending SQL adapters/models only
> where needed. Show source and timestamp for every detailed metric. Implement
> ECharts histories with gaps, units, thresholds and transition annotations.
> Calculate rates only from comparable samples. Explain unavailable/null lag,
> role mismatches and insufficient forecast history. Distinguish configured
> routing from observed traffic; never imply packet-level visibility.

Acceptance: tests cover idle standby, lag growth, missing samples, timeline/role
changes and unavailable counters. Topology and replication panels agree on the
same snapshot and show explicit unknowns.

### 8. CLI-backed administrative workflows

- [ ] Implement attach, detach, promote, recover and reload.
- [ ] Implement reviewed plans, confirmations and operation timelines.
- [ ] Show redacted equivalent CLI commands.

Prompt:

> Connect frontend administrative controls to allowlisted CLI commands. Use actual
> CLI preflight/plan data; add a machine plan contract if needed. Bind confirmation
> to the reviewed target, options and configuration revision, with expiry and
> revalidation before execution. Preserve typed confirmation and explicit force
> semantics; do not silently add --yes or --force. Derive audit actor from the
> authenticated session. Show the equivalent redacted CLI command, real progress,
> verification evidence, final exit code and next hint. Reconnect by operation ID.

Acceptance: frontend and terminal actions share safeguards and audit records;
stale plans and mismatched confirmations fail; completion reflects verification,
not merely an accepted HTTP request.

### 9. Cluster configuration, logs and audit

- [ ] Add pgpool configuration staging/diff/validation/apply/rollback.
- [ ] Add live log/error panes with filtering and bounded retention.
- [ ] Add audit list/detail/verification and CLI-originated activity.

Prompt:

> Implement Cluster Configuration through existing CLI configuration commands,
> extending structured output where needed. Show live versus file versus staged
> values, drift, reload/restart requirements, validation and rollback details.
> Isolate or serialize staging so users cannot overwrite each other's changes;
> include concurrent terminal edits in revision checks. Add real log/error follow
> streams with bounded rendering, pause and explicit gap indicators. Add audit
> history/verification including commands run outside the console. Label observed
> CLI audit events accurately; do not invent progress for legacy CLI invocations.

Acceptance: configuration changes follow the same transactional CLI path;
concurrent edits are detected; log bursts remain bounded; terminal actions appear
in the shared audit view.

### 10. Packaging, integration and release checks

- [ ] Preserve current CLI-only build/package paths without frontend prerequisites.
- [ ] Add optional console build/package targets and version compatibility checks.
- [ ] Verify live behavior on a real Linux test cluster.

Prompt:

> Package the CLI and optional console separately for Linux amd64/arm64. Keep
> make build, make build-linux and CLI packages independent of Node and frontend
> assets. Add explicit console frontend/build/package targets with embedded
> assets and no runtime Node dependency. Document CLI-only installation, console
> installation, token bootstrap, SSH forwarding, configuration migration and
> upgrades. Regenerate relevant CLI docs/man pages idempotently.
> Run relevant Go tests, race checks, lint, frontend checks and Playwright flows.
> Exercise custom SQL/PCP/PostgreSQL ports, real credential probes, live replication,
> controlled node actions, SSE reconnect, slow clients, outages and CLI/web
> concurrency on an isolated Linux test cluster. Report unavailable integration
> infrastructure and unverified scenarios explicitly.

Acceptance: CLI builds/installs without web tooling; optional console connects to
the supported CLI; live demonstrations use real observations; release notes
state compatibility and verification limits.

## Completion checklist

- [x] All required connection values are user-configurable (setup wizard → `config install`).
- [x] Monitoring, replication, data flow, logs and operations update live (2 s monitor stream over SSE; rates computed from real LSN/select deltas).
- [x] Every frontend mutation passes through the CLI (allowlisted argv, incl. `service`, `config set/apply/rollback`, `attach/detach/promote/recover/reload`).
- [x] CLI-only operation has no frontend dependency (console is a separate binary/service).
- [x] Unknown, stale, failed and interrupted states are visible and accurate (freshness chip, stale timer → login, `interrupted` ops, `-`/`n/a` for unavailable rates; cumulative counters labelled as such; conn STATE shows active/idle/idle-in-txn).
- [x] Credentials remain protected across setup, execution and audit (token→session cookie + CSRF, no tokens in URLs).
- [x] Confirmations, locking and transactional configuration behavior are preserved (plan→typed token→execute→verify via RunControl).
- [x] Real-cluster verification is recorded separately from fake-adapter tests (`dev/testbed` 64-step scenario run incl. service stop/start; unit tests use scripted fakes).

## Delivered beyond the plan

- `poolwarden service <start|stop|restart|status> <pgpool|postgresql|watchdog> <host>` — admin module in the console's Operations page, backed by a new RunControl action (preflight role/leader/primary checks, typed confirm, remote exec via `bins.systemctl`, status verify, audit).
- Live conn states (`pg_stat_activity.state`), WAL inventory + generation/replay rates, cumulative-vs-live labelling on pool/clients metrics.
