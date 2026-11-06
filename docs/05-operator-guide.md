# poolwarden operator guide

## Prerequisites

- root or `sudo -n` for writes; poolwarden runs as root (D10).
- pgpool-II 4.4/4.5, PostgreSQL 14+, pcp binaries on PATH (`pcp.bin_dir`),
  `iproute2` (watchdog VIP), `systemctl`.
- `~postgres/.pgpass` (`*:9999:postgres:*`) and `~postgres/.pcppass`
  (`*:9898:poolwarden:*`), both mode **0600**.
- sshd on every host: `ExposeAuthInfo yes` for key-owner audit (D15/V6, T2
  UNVERIFIED). One deploy key per DBA; get its fingerprint with
  `ssh-keygen -lf key.pub` and map it:
      operators: { alice: "SHA256:abc...", bob: "SHA256:def..." }
- known_hosts: `ssh-keyscan -H <host> >> /root/.ssh/known_hosts` (or the
  `ssh.known_hosts` path from init).
- passphrase-less deploy key for root or postgres on each host; set
  `ssh_user` per host during init.

## First run

`poolwarden init` is audited: the finished audit entry carries the unified
config diff (secrets masked).

## First run

`poolwarden init` walks: pgpool.conf location → ports/passfiles → connection
tests → watchdog peers + per-host roles/ssh_user + SSH test → per-PG log
settings → thresholds/audit/bins → confirm → write (0600, backup kept).
Then `poolwarden config self-check` should show all OK.

## Daily use

`topology discover` diffs the live cluster (watchdog peers + backend_hostnameN)
against your inventory; `--apply` writes the changes (audited, backup kept).
`config export <file>` writes the effective config to a path of your choice;
this is the one user-directed write; review permissions afterwards.

## Daily use

`status`, `nodes`, `lag`, `watchdog`, `diagnose`, `explain <id>`,
`watch status|nodes|lag|pools|watchdog|errors`, `top`. In the shell
(`poolwarden` with no args) commands are identical.

## Change management

`config set <p> <v>` stages; `config diff` shows the unified diff;
`config validate` runs catalogue+cross rules; `config apply` (token `apply`
or `restart` for restart-class params; the plan warns when the VIP moves
last). Failures: exit 5 = rolled back (per-host table), exit 6 = manual
recovery (`cp <backup> <conf> && systemctl restart pgpool-II` per host).
`config history` / `config rollback <version>`.

## Node control

`promote <id>` (real switchover; `--pgpool-only` only changes pgpool's view;
**UNVERIFIED T1**), `detach <id>` (primary requires token `detach-primary-N`),
`attach <id>`, `recover <id>`.

## Audit

`audit`, `audit show <id>`, `audit verify`, `audit sessions`. Entries also go
to syslog (authpriv) when `audit.syslog` is on; `retention_days` rotates
`audit.jsonl` daily; do NOT logrotate it (see /etc/logrotate.d/poolwarden
which only covers tool.log).

## Troubleshooting

Exit codes above; degraded mode = limited commands + `DEGRADED` prompt;
"config file has unsafe permissions" → `chmod 0600 /etc/poolwarden.yaml`;
known_hosts errors give the exact `ssh-keyscan -H` line to run.

## Unverified items (appendix)

- **T1** `promote --pgpool-only` · **T2** `ExposeAuthInfo` availability ·
  **I21** all fixtures are synthetic · **I22** RPM/deb install untested on the
  build machine · catalogue entries flagged Unverified; list them with
  `poolwarden config catalogue --unverified`.
