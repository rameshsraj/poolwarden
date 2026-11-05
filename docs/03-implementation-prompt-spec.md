# poolwarden — Implementation Prompt Specification

| | |
|---|---|
| Document | 03 — Implementation Prompt Specification |
| Status | Draft for review |
| Date | 2026-11-05 |
| Use | Give this to the AI coding agent (or developer) that builds poolwarden. Each milestone prompt is self-contained and can be pasted on its own. |

---

## Part A — Master prompt (send this first, every session)

```
ROLE
You are a senior Go engineer building "poolwarden", a 100% CLI command center for one
pgpool-II cluster, used by DBAs over SSH on Linux. You write production-quality, well-tested
Go. You do not invent pgpool or PostgreSQL behaviour: if a behaviour is not stated in the specs
or the official docs, stop and ask.

SOURCE OF TRUTH (read before writing any code, in this order)
1. docs/01-design-spec.md       architecture, data sources, rules, security, exit codes
2. docs/02-functional-spec.md   every command: flags, flow, output, acceptance criteria
3. DESIGN.md                    decision log D1–D22, verification results V1–V6
If the documents conflict: the functional spec wins for user-visible behaviour, and the design
spec wins for internals. Report every conflict you find instead of picking an answer silently.

HARD CONSTRAINTS
- Go 1.23+ (the local toolchain is 1.27), single static binary: CGO_ENABLED=0, linux/amd64 + arm64.
- Target pgpool-II 4.4/4.5, PostgreSQL 14+. Runs as root on each pgpool host.
- DB/PCP access as user `postgres`. Passwords ONLY from PGPASSFILE / PCPPASSFILE or no-echo prompts.
  Never in YAML, logs, audit entries, command lines or error messages.
- No hard-coded paths or log formats. Everything comes from /etc/poolwarden.yaml (written by `init`).
- Promotion only via pcp_promote_node (-s switchover default, --pgpool-only without -s).
  NEVER use pg_promote().
- Never call a shell. Run external binaries with exec.Command(bin, args...) using absolute paths
  from config.
- Every control action follows the C-flow (functional spec §0) and writes a `requested` and a
  `finished` audit entry. Refused, aborted and failed attempts are audited too.
- The service layer never prints and never reads stdin. Only cli/render/shell do I/O with the user.
- Each external system sits behind an interface (pgpoolsql, backendsql, pcp, remote, logsrc,
  clock, fs) so it can be faked in tests.
- Root safety: refuse to run if /etc/poolwarden.yaml is not root-owned or is group/other-writable.
  Write only to configured pgpool paths and /var/lib/poolwarden. Writes are atomic
  (temp file in the same dir → fsync → rename) and keep the original owner and mode.

LIBRARIES (allowed; ask before adding others)
spf13/cobra, jackc/pgx/v5, gopkg.in/yaml.v3, golang.org/x/crypto/ssh (+ knownhosts),
golang.org/x/term, one REPL library (reeflective/console or chzyer/readline: choose one and
explain why), a table renderer (or text/tabwriter). Standard library for everything else.

CODING STANDARDS
- Package layout exactly as in design spec §5. No package cycles. internal/ only.
- Errors: wrap with %w; user-facing errors are *exitcode.Error{Code, Msg, Cause, Hint}.
- Every context-taking call has a deadline. No goroutine leaks (go test -race must be clean).
- Logging: the tool's own diagnostics go to /var/lib/poolwarden/tool.log (rotating). Never stdout.
- Times: store UTC; display local time with the offset; RFC 3339 with milliseconds in JSON and audit.
- Tests: table-driven; golden files for renderers and the config round-trip; fixtures for every
  parser (SHOW POOL_* in 4.4 and 4.5 column sets, pcp_* outputs, log lines with the configured
  prefix). Target ≥ 80% coverage for diagnose, preflight, pgconf, audit, forecast.
- go vet, staticcheck and gofmt must be clean. No TODOs left in merged code without an issue ref.

WORKING METHOD
- Work milestone by milestone (Part B). At the start of a milestone, restate its scope and
  acceptance criteria in 5 lines or fewer. At the end, show: files changed, tests added,
  `go test ./...` output, and which acceptance criteria are met or not.
- Do not start the next milestone until the current one is accepted.
- Where something is ambiguous, list the question and your proposed default, then wait.
- Mark anything you could not verify (for example pgpool behaviour that has no fixture) as
  UNVERIFIED in the summary.

EXISTING CODE
The repo contains early prototype code (internal/cli, internal/collector, internal/config,
internal/exitcode). Treat it as reference only. Reuse it where it fits the specs, and rewrite
it where it doesn't. Its parsers (ParsePoolNodes, ParseWatchdog) have tests that are worth keeping.
```

---

## Part B — Milestone prompts

Each milestone ends with `go build`, `go vet`, `go test -race ./...` all passing.

### M1 — Foundation
```
Build: module skeleton per design §5; exitcode package (codes §15, Error{Code,Msg,Cause,Hint},
renderer for error/cause/next); config package (schema = functional spec §1.1 + design §7;
load, validate, permission check, defaults: interval 2s, timeout 3s, lag warn 1800s / crit 3600s,
retention_days 31); render package (table/wide/json/nagios, TTY detection, --no-color);
cobra root with all global flags from functional spec §0 and every command registered (stubs
return "not implemented" with exit 1); `version`, `help`.
Accept: `poolwarden --help` lists the full command tree; a config with a bad mode is refused
with exit 1 and a hint; golden tests for each renderer.
```

### M2 — Adapters and collector
```
Build: pgpoolsql (simple protocol; SHOW POOL_NODES/PROCESSES/POOLS/HEALTH_CHECK_STATS/
BACKEND_STATS; PGPOOL SHOW <param> and PGPOOL SHOW ALL), backendsql (queries in design §8),
pcp adapter (node_info, proc_info, watchdog_info -v, attach, detach, promote [-s][-g],
recovery, reload_config -s c|l), each parser with fixtures; remote (ssh exec + file read/write,
strict known_hosts, per-host ssh_user, per-step timing struct); VIP check via `ip -o addr show`;
collector building Snapshot in parallel with deadlines; degraded mode (design §16).
Accept: with fakes, one dead backend does not delay the snapshot beyond timeout+1s; degraded
mode is used when pgpool SQL fails; parsers pass for 4.4 and 4.5 fixtures (ask the user for
real captures, and use clearly-labelled synthetic ones until they arrive).
```

### M3 — Views
```
Build: status, nodes [id], lag, slots, pools, procs, conns, watchdog, health, topology show,
exactly as in functional spec §2 including the header line, column sets, and JSON forms.
Accept: golden tests per view (table and json); exit 10/11 driven by findings (stub diagnose
until M5 returns none).
```

### M4 — History and forecast
```
Build: history store (/var/lib/poolwarden/samples, size-capped ring, 24h, downsample after 1h,
crash-safe append); background sampler for shell/watch; forecast (design §13: regression over
5 min, R² gate 0.6, ETAs); health score; `forecast` command; lag TREND/ETA columns.
Accept: unit tests with a synthetic series (rising, flat, noisy); "insufficient history"
message below 5 min.
```

### M5 — Diagnosis engine
```
Build: every rule in design §12 as a pure function with table-driven tests (positive, negative,
edge); `diagnose`, `explain`. WD_HALF_VOTES_2NODE must fire for the production settings (D14)
as a standing WARN. Next-commands must be real poolwarden commands.
Accept: each rule has at least 3 tests; explain output matches functional spec §3.2.
```

### M6 — Errors and logs
```
Build: logsrc (local + ssh tail, rotation-aware, prefix-driven parser built from the configured
log_line_prefix escapes %m %t %p %a %u %d %r %h %l etc. Unknown escapes are treated as a
wildcard and logged once); classifier tags (functional spec §3.5); ErrorEvent normalisation
from all sources in design §8; `errors`, `errors summary`, `errors show`, `logs`, follow mode.
Accept: fixtures for pgpool and PG text logs with at least 2 different prefixes; follow mode
stops cleanly on Ctrl-C.
```

### M7 — Audit
```
Build: identity resolution (SSH_USER_AUTH → key → SHA256 fingerprint → operators map;
/proc/self/loginuid; SUDO_USER; SSH_CONNECTION; declared operator; tty/pid/host); entry schema
(design §11) with requested/finished phases and steps[] timings; hash chain across daily
rotation; syslog copy; retention cleanup (never below retention_days); `audit`, `audit show`,
`audit verify`, `audit sessions`.
Accept: verify detects edited, deleted and reordered lines in tests; secrets are masked;
identity_mismatch is set when the declared name differs from the key owner.
```

### M8 — Pre-flight and node control
```
Build: preflight per design §14; C-flow runner in service with a Confirmer interface (typed
token, --yes --confirm for non-TTY); promote (both paths), detach, attach, recover, reload,
each with a verify step and timeline output exactly as in functional spec §4. Never auto-rollback
a promote.
Accept: NO-GO blocks without --force; detach of primary needs token detach-primary-<id>;
every path writes requested and finished audit entries (tested with a fake clock).
```

### M9 — Configuration management
```
Build: pgconf round-trip parser for pgpool.conf (byte-identical output when no change),
pool_hba.conf, pcp.conf, pool_passwd; param catalogue for 4.4/4.5. Build it from the official
docs and list every param whose context or range you could not confirm as UNVERIFIED. Then:
cross-param rules; staged.json with operator tagging; cluster lock (local flock + remote lock
files with owner, pid and time; stale-lock detection after 30 min; steal only with --force);
apply flow (design §9 steps 1–7) including the restart order (non-leaders first, wait for
rejoin, leader last, with a VIP warning) and an all-or-nothing rollback; history/rollback/
load/export; backend/hba/pcpuser/pooluser commands.
Accept: golden round-trip on real pgpool.conf samples (comments preserved); simulated failure
on host 2 rolls back host 1 (exit 5); simulated rollback failure gives exit 6 with per-host state.
```

### M10 — Shell, watch, init, SQL
```
Build: shell (prompt, completion for commands/flags/node ids/hosts/params/finding ids,
per-operator history, Ctrl-C semantics); watch/top (alternate screen, keys q p + - 1-5);
init wizard (functional spec §1.1, auto-detect with source shown, override, connection tests);
config self-check; sql command (single statement, extended protocol, READ ONLY, 30s, row cap,
audited).
Accept: init can be re-run without losing manual overrides; sql rejects "select 1; select 2".
```

### M11 — Packaging and docs
```
Build: Makefile/goreleaser for static binaries; RPM and deb (binary, /var/lib/poolwarden 0700
root, logrotate snippet); man page and README generated from the cobra help; an operator guide
(setup prerequisites: ExposeAuthInfo yes, one key per DBA, known_hosts, pgpass/pcppass).
Accept: package installs cleanly on RHEL-family and Debian-family; `poolwarden init` works
from a fresh install.
```

---

## Part C — Review prompt (run at the end of each milestone)

```
Review the diff for this milestone against docs/01, docs/02 and the hard constraints in the
master prompt. Report, ranked by severity:
1. any spec deviation (cite the section),
2. any place a password or secret could leak (logs, audit, argv, errors),
3. any external call without a deadline,
4. any write outside allowed paths or any write that isn't atomic,
5. any control path that skips pre-flight, confirmation or audit,
6. missing tests for parsers/rules.
Do not fix anything; list the findings with file:line.
```

---

## Part D — Questions the agent must ask rather than assume
- Real `SHOW POOL_*`, `pcp_watchdog_info -v` and log samples from production (redacted). These are needed for the fixtures.
- Any catalogue param whose reload/restart context is not clearly documented.
- The REPL library choice (it must be justified).
- Anything about `pcp_promote_node` without `-s` (T1, still unverified).
