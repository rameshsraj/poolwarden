# poolwarden — Implementation Decisions

| | |
|---|---|
| Document | 04 — Implementation Decisions |
| Status | Accepted 2026-11-05 |
| Use | Resolutions of every conflict or gap found while reviewing docs/01, docs/02 and DESIGN.md before coding. Where this file and DESIGN.md disagree, this file and docs/01–02 win. |

## Conflicts between DESIGN.md and the specs (spec wins)

| # | Topic | DESIGN.md said | Decision |
|---|---|---|---|
| I1 | Privilege | run as `postgres`, `sudo -n` for writes | Runs as root (D10, 01 §17). `systemctl` called directly. |
| I2 | staged.json | `~/.poolwarden/staged.json` | `/var/lib/poolwarden/staged.json` (01 §9) |
| I3 | tool.log | `~/.poolwarden/tool.log` | `/var/lib/poolwarden/tool.log`, rotating (03 master prompt) |
| I4 | Config path | `~/.poolwarden.yaml` or `/etc/poolwarden.yaml` | `/etc/poolwarden.yaml` only; `-c` overrides. No home-dir search. |
| I5 | `sql` target | `sql <primary\|standby N>` | `sql <primary\|standby\|node N\|host h>` (02 §6) |
| I6 | proc info | `proc <pid>` | `procs <pid>` (02 §2.5) |
| I7 | `errors --source` | `pgpool\|pg\|pcp\|health\|replication\|tool` | adds `conn\|db` (02 §3.4) |
| I8 | One-shot forecast | "takes about 30s of samples" | Reads on-disk history; needs 5 min / ≥10 points, otherwise prints `insufficient history (have Xm, need 5m)` (01 §13, 02 §3.3) |
| I9 | PG logs | `pg_read_file`, csvlog/jsonlog parsing | Text files only, local or over SSH, stderr format. `init` warns when `log_destination` is not `stderr`. csvlog/jsonlog are out of v1. |
| I10 | Lag thresholds | prototype had byte thresholds and 10s/60s | Time only: `lag: {warn_seconds: 1800, crit_seconds: 3600}` (D13) |
| I11 | Audit file | prototype default `/var/log/poolwarden/audit.jsonl` | `/var/lib/poolwarden/audit.jsonl`, mode 0600 (01 §11) |

## Gaps in docs/01–02 (filled here)

| # | Topic | Decision |
|---|---|---|
| I12 | Packages missing from 01 §5 | Add `internal/service` (01 §4 requires it), `internal/pgpoolsql` and `internal/backendsql` (adapters named in 03 M2), `internal/clock` and `internal/fs` (fakeable interfaces named in the master prompt), and `internal/model` holding the shared domain types of 01 §6 (Snapshot, Backend, Finding, ErrorEvent, PreflightResult) so adapters and consumers never import each other. |
| I23 | Remote file writes | `x/crypto/ssh` has no SFTP. Remote writes run single binaries over SSH exec with every argument shell-quoted: `dd of=<tmp>` (data on stdin) → `sync <tmp>` → `chmod --reference`/`chown --reference` → `mv -f <tmp> <target>` → `sync <dir>`. Each step is timed and audited. Only the local host uses `internal/fs` directly. |
| I24 | Degraded-mode node list | Uses `pcp_node_info --all -v` (4.3+). If the installed pgpool rejects `--all`, fall back to `pcp_node_info -v <id>` for ids 0..N-1 taken from the pgpool.conf `backend_hostnameN` keys. |
| I13 | `internal/errors` shadows stdlib | Directory stays `internal/errors`; Go package name is `errevents`. |
| I14 | Paths the spec never gave a config key for | New keys, all written by `init`: top-level `state_dir` (default `/var/lib/poolwarden`), `bins: {systemctl, ip}`, `ssh: {key, known_hosts, timeout}` (defaults `/root/.ssh/id_ed25519`, `/root/.ssh/known_hosts`, 5s), per-host `pgpool.service` (systemd unit, default `pgpool-II`), `pgpool.pool_passwd`. `pg_md5`/`pg_enc` are resolved from `pcp.bin_dir`. |
| I15 | `--nagios` exit mapping | 0 → OK (0); 10 → WARNING (1); 11 → CRITICAL (2); any other code → UNKNOWN (3). One summary line on stdout. |
| I16 | Shell prompt | `pgpool[<cluster>@<localhost>]>` and `pgpool[<cluster>@<localhost> DEGRADED]>` (02 §1.4). |
| I17 | Per-PG-host config keys | `pg: {port, data_dir, log_dir, log_filename, log_destination, log_line_prefix}` (02 §1.1 step 5). |
| I18 | Unverified behaviour | `promote --pgpool-only` (T1) and `ExposeAuthInfo` support (T2) are implemented per 01 §10 / §11 and labelled UNVERIFIED in help text and summaries. |
| I19 | Config permission check off-root | Production rule stands: owner must be uid 0 and no group/other write bits. When the process itself is not root (developer machine, CI), the owner must be the current uid instead; the write-bit rule still applies. |
| I20 | REPL library | `reeflective/console`: completion is generated from the cobra tree (commands, flags, `ValidArgsFunction`), so shell and one-shot mode cannot drift. `chzyer/readline` would need a second hand-written completer and last shipped in 2022. |
| I21 | Fixtures | No production captures are available yet. All `SHOW POOL_*`, `pcp_*` and log fixtures are synthetic, built from the pgpool-II 4.4/4.5 documentation column lists, and carry a `# SYNTHETIC` header line. Replace them when redacted captures arrive. |
| I22 | Packaging (M11) | nfpm + goreleaser configuration and Makefile. RPM/deb contents are verified by listing the archive; install on RHEL/Debian is UNVERIFIED on the build machine (macOS). |
| I25 | One-shot history writes | Any command that takes a snapshot appends it to the history store best-effort (via `Service.collect`), so cron-driven one-shot runs build history too; errors are swallowed. The background sampler is only started by shell/watch. |
| I26 | pool_passwd writes | md5 entries are computed in-process (`"md5"+md5(password+username)` — `pg_md5 -p` needs a TTY so `-p`+stdin is unusable) and written via `WriteFileAtomic`. AES entries use `pg_enc -m -k <keyfile> -f <conf> -i <tmpfile>` where tmpfile is `<state_dir>/.pooluser-<rand>` (0600, `user:password`) zeroed and removed right after the call; the password never appears on argv or in the audit argv. |
| I27 | Secrets and staging | pcpuser/pooluser password changes are never written to `staged.json`; those commands run their own immediate apply-style transaction instead. |

| I28 | passfile keys | `pgpool.passfile` and `pcp.passfile` (written by `init`) are exported as `PGPASSFILE`/`PCPPASSFILE` into the process env for pgx/pcp when set. |

## Config schema (authoritative; supersedes `poolwarden.example.yaml`)

```yaml
cluster: prod-a
state_dir: /var/lib/poolwarden
operators:                        # SSH key fingerprint -> person
  "SHA256:abc...": "Jane DBA"
allowed_operators: []             # empty = everyone
pgpool: {host: 127.0.0.1, port: 9999, user: postgres, dbname: postgres}
pcp:    {host: 127.0.0.1, port: 9898, user: postgres, bin_dir: /usr/bin}
bins:   {systemctl: /usr/bin/systemctl, ip: /usr/sbin/ip}
ssh:    {key: /root/.ssh/id_ed25519, known_hosts: /root/.ssh/known_hosts, timeout: 5s}
interval: 2s
timeout: 3s
lag: {warn_seconds: 1800, crit_seconds: 3600}
wal_volume_bytes: 100GB
audit: {file: /var/lib/poolwarden/audit.jsonl, syslog: true, read_commands: false, retention_days: 31}
hosts:
  - name: db1
    address: 10.0.0.11
    roles: [pgpool, pg]
    ssh_user: root                # root | postgres
    pgpool:
      conf: /etc/pgpool-II/pgpool.conf
      hba: /etc/pgpool-II/pool_hba.conf
      pcp_conf: /etc/pgpool-II/pcp.conf
      pool_passwd: /etc/pgpool-II/pool_passwd
      log_dir: /var/log/pgpool-II
      log_line_prefix: '%m: %a pid %p: '
      service: pgpool-II
    pg:
      port: 5432
      data_dir: /var/lib/pgsql/16/data
      log_dir: /var/lib/pgsql/16/data/log
      log_filename: postgresql-%a.log
      log_destination: stderr
      log_line_prefix: '%m [%p] '
```
