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
→ watchdog leader stop/start (VIP move) → config set/validate/apply/rollback → audit,
sql. Expected exit codes follow docs/02; view commands accept 0/10/11 because the
exit code reflects findings, not failure.

Last run (2026-11-05, pgpool-II 4.5.13, PostgreSQL 18): **55/55 PASS on both beds**
(`results/<bed>/SUMMARY.md`). Captures were taken with `docker exec` without a TTY, so
stdout/stderr order inside a file can differ from a terminal.

Test-bed only shortcuts (never used in production): pgpool runs as root, shared ssh
keys baked into the image, passwords `pgpool`, `svc` stands in for `systemctl`.
