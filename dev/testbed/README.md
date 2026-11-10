# poolwarden Docker test beds

Two compose projects built from one image (`Dockerfile`: golang builder → `postgres:18`
+ pgpool-II 4.5.13 compiled from source + sshd + poolwarden). Every container runs
`entrypoint.sh`, which initialises PostgreSQL (primary initdb / standby pg_basebackup),
renders `pgpool.conf` with watchdog + VIP, installs the pgpool reference failover
scripts (`scripts/`), and writes `/etc/poolwarden.yaml` for that node.

| bed | project | containers | layout |
|---|---|---|---|
| A | `pw-colocated` (`compose.colocated.yaml`) | db1 172.31.10.11, db2 172.31.10.12, VIP .100 | pgpool + PG on each host (production layout, D6) |
| B | `pw-split` (`compose.split.yaml`) | pg1 .21, pg2 .22, pgpool1 .31, pgpool2 .32, VIP 172.31.20.100 | 2 PG hosts + 2 pgpool hosts |

```
docker compose -p pw-colocated -f compose.colocated.yaml up -d --build
./run-scenarios.sh colocated          # results/colocated/NN-step.txt + SUMMARY.md
docker compose -p pw-split -f compose.split.yaml up -d --build
./run-scenarios.sh split
```

`run-scenarios.sh` drives poolwarden from inside a pgpool container (non-TTY:
`--yes --confirm <token>`): views → kill the primary (failover by pgpool) → online
recovery of the old primary → switchover back and forth (`promote -s`) → detach/attach
→ watchdog leader stop/start (VIP move) → **service control**
(`poolwarden service status|restart pgpool …` and `service stop|start postgresql …`
via the `svc` systemctl stand-in) → config set/validate/apply/rollback → audit,
sql. Expected exit codes follow docs/02; view commands accept 0/10/11 because the
exit code reflects findings, not failure.

Notes on the bed's service model: `svc` handles `pgpool-II` (pgrep/pkill — the
foreground `-n` mode writes no pidfile, so `pgpool -m stop` can't be used) and
`postgresql` (`su postgres -c pg_ctl -D /var/lib/postgresql/18/main`). Stopping a
service no longer kills the container (PID 1 is `sleep infinity`), so `service start`
can bring it back over SSH. PostgreSQL datadirs live in anonymous volumes — a
clean reset needs `--renew-anon-volumes` on `compose up` or `down -v`.

## Web console

Both beds also start a `console` container running `poolwconsole` on the same
network (same image, different entrypoint). It renders its own
`/etc/poolwarden.yaml` pointing at the first pgpool host, scans ssh host keys,
and serves the SPA + API:

| bed | URL | token |
|---|---|---|
| colocated | http://127.0.0.1:8080 | `console-test` |
| split | http://127.0.0.1:8081 | `console-test` |

Every browser action shells out to `poolwarden` inside the console container
(allowlisted argv, no shell); live data comes from one shared
`poolwarden monitor` NDJSON process fanned out over SSE.

Last run (2026-11-09, pgpool-II 4.5.13, PostgreSQL 18): **64/64 PASS on the
colocated bed** (`results/<bed>/SUMMARY.md`). Captures were taken with `docker
exec` without a TTY, so stdout/stderr order inside a file can differ from a terminal.

Test-bed only shortcuts (never used in production): pgpool runs as root, shared ssh
keys baked into the image, passwords `pgpool`, `svc` stands in for `systemctl`.

The baked key is `dev/testbed/testbed_key` — a committed, deterministic,
passphrase-less keypair. It exists solely so the testbed containers can ssh to
each other on the private compose networks; it is authorized only inside these
containers and must never be reused elsewhere.
