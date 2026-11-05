# SUMMARY — colocated
| step | command | exit | result |
|---|---|---|---|
| 00-ready | `bash -c 
  for i in $(seq 1 60); do
    ups=$(docker exec db1 bash -c "PGPASSWORD=pgpool psql -h 127.0.0.1 -p 9999 -U postgres -A -t -c 'SHOW POOL_NODES'" 2>/dev/null | grep -c '|up|')
    wd=$(docker exec db1 bash -c 'PCPPASSFILE=/root/.pcppass pcp_watchdog_info -h 127.0.0.1 -w 2>/dev/null | grep -c QUORUM')
    if [ "$ups" -ge 2 ]; then echo "pool up ($ups), wd=$wd"; exit 0; fi
    sleep 3
  done; echo TIMEOUT; exit 1` | 0 | PASS |
| 01a-status | `bash -c docker exec db1 poolwarden --operator tester status` | 10 | PASS |
| 01b-nodes | `bash -c docker exec db1 poolwarden --operator tester nodes` | 10 | PASS |
| 01c-lag | `bash -c docker exec db1 poolwarden --operator tester lag` | 10 | PASS |
| 01d-watchdog | `bash -c docker exec db1 poolwarden --operator tester watchdog` | 10 | PASS |
| 01e-health | `bash -c docker exec db1 poolwarden --operator tester health` | 10 | PASS |
| 01f-pools | `bash -c docker exec db1 poolwarden --operator tester pools` | 10 | PASS |
| 01g-topology | `bash -c docker exec db1 poolwarden --operator tester topology show` | 10 | PASS |
| 01h-diagnose | `bash -c docker exec db1 poolwarden --operator tester diagnose` | 10 | PASS |
| 01i-forecast | `bash -c docker exec db1 poolwarden --operator tester forecast` | 10 | PASS |
| 01j-selfcheck | `bash -c docker exec db1 poolwarden --operator tester config self-check` | 0 | PASS |
| 01k-errors | `bash -c docker exec db1 poolwarden --operator tester errors --since 10m` | 0 | PASS |
| 01l-logs | `bash -c docker exec db1 poolwarden --operator tester logs --classify --since 10m` | 0 | PASS |
| 02a-kill-pg0 | `docker exec db1 su postgres -c pg_ctl -D /var/lib/postgresql/18/main stop -m immediate -w` | 0 | PASS |
| 02b-wait-flip | `bash -c 
  for i in $(seq 1 40); do
    st=$(docker exec db1 bash -c "PGPASSWORD=pgpool psql -h 127.0.0.1 -p 9999 -U postgres -A -t -c 'SHOW POOL_NODES'" 2>/dev/null)
    echo "$st"
    echo "$st" | grep -q ' down ' || echo "$st" | grep -q quarantine || true
    n1=$(echo "$st" | sed -n '2p')
    echo "$n1" | grep -qi 'primary' && echo FLIPPED && exit 0
    sleep 3
  done; exit 1` | 0 | PASS |
| 02c-nodes | `bash -c docker exec db1 poolwarden --operator tester nodes` | 10 | PASS |
| 02d-diagnose | `bash -c docker exec db1 poolwarden --operator tester diagnose` | 10 | PASS |
| 02e-errors | `bash -c docker exec db1 poolwarden --operator tester errors --since 10m` | 0 | PASS |
| 02f-logs | `bash -c docker exec db1 poolwarden --operator tester logs --classify --since 10m` | 0 | PASS |
| 02g-watchdog | `bash -c docker exec db1 poolwarden --operator tester watchdog` | 10 | PASS |
| 02h-audit | `bash -c docker exec db1 poolwarden --operator tester audit` | 0 | PASS |
| 03pre-force-down | `bash -c 
  docker exec db1 su postgres -c 'pg_ctl -D /var/lib/postgresql/18/main stop -m immediate -w' || true
  for i in $(seq 1 40); do
    st=$(docker exec db1 bash -c "PGPASSWORD=pgpool psql -h 127.0.0.1 -p 9999 -U postgres -A -t -c 'SHOW POOL_NODES'" 2>/dev/null | sed -n '1p')
    echo "$st" | grep -q '|down|' && echo NODE0-DOWN && exit 0
    sleep 3
  done; echo TIMEOUT; exit 1` | 0 | PASS |
| 03a-recover | `bash -c docker exec db1 poolwarden --operator tester recover 0 --yes --confirm 0 --ticket TB-1` | 0 | PASS |
| 03b-nodes | `bash -c docker exec db1 poolwarden --operator tester nodes` | 10 | PASS |
| 03c-lag | `bash -c docker exec db1 poolwarden --operator tester lag` | 10 | PASS |
| 04a-promote0 | `bash -c docker exec db1 poolwarden --operator tester promote 0 --yes --confirm 0 --ticket TB-1` | 0 | PASS |
| 04b-nodes | `bash -c docker exec db1 poolwarden --operator tester nodes` | 10 | PASS |
| 04c-lag | `bash -c docker exec db1 poolwarden --operator tester lag` | 10 | PASS |
| 04d-diagnose | `bash -c docker exec db1 poolwarden --operator tester diagnose` | 10 | PASS |
| 04e-audit-show | `bash -c docker exec db1 poolwarden --operator tester audit` | 0 | PASS |
| 05a-promote1 | `bash -c docker exec db1 poolwarden --operator tester promote 1 --yes --confirm 1 --ticket TB-1` | 0 | PASS |
| 05b-nodes | `bash -c docker exec db1 poolwarden --operator tester nodes` | 10 | PASS |
| 06a-detach | `bash -c docker exec db1 poolwarden --operator tester detach 0 --yes --confirm 0 --ticket TB-1` | 0 | PASS |
| 06b-nodes | `bash -c docker exec db1 poolwarden --operator tester nodes` | 10 | PASS |
| 06c-attach | `bash -c docker exec db1 poolwarden --operator tester attach 0 --yes --confirm 0 --ticket TB-1` | 0 | PASS |
| 06d-nodes | `bash -c docker exec db1 poolwarden --operator tester nodes` | 10 | PASS |
| 07a-stop-wd-leader | `docker exec db1 bash -c pgpool -m fast stop || kill $(pgrep -x watchdog) 2>/dev/null; sleep 8; echo stopped` | 0 | PASS |
| 07b-watchdog | `bash -c docker exec db2 poolwarden --operator tester watchdog` | 10 | PASS |
| 07c-start-wd | `docker exec db1 bash -c pgpool -n >/tmp/pgpool-restart.log 2>&1 & sleep 5; pgrep -x watchdog || pgrep pgpool | head -1` | 0 | PASS |
| 07d-watchdog | `bash -c docker exec db2 poolwarden --operator tester watchdog` | 10 | PASS |
| 08a-set | `bash -c docker exec db1 poolwarden --operator tester config set health_check_period 7` | 0 | PASS |
| 08b-diff | `bash -c docker exec db1 poolwarden --operator tester config diff` | 0 | PASS |
| 08c-validate | `bash -c docker exec db1 poolwarden --operator tester config validate` | 0 | PASS |
| 08d-apply | `bash -c docker exec db1 poolwarden --operator tester config apply --yes --confirm apply --ticket TB-1` | 0 | PASS |
| 08e-get | `bash -c docker exec db1 poolwarden --operator tester config get health_check_period` | 0 | PASS |
| 08f-history | `bash -c docker exec db1 poolwarden --operator tester config history` | 0 | PASS |
| 08g-rollback | `bash -c ver=$(docker exec db1 poolwarden --operator tester config history | tail -1 | awk '{print $1}'); docker exec db1 poolwarden --operator tester config rollback $ver --yes --confirm apply --ticket TB-1` | 0 | PASS |
| 08h-get | `bash -c docker exec db1 poolwarden --operator tester config get health_check_period` | 0 | PASS |
| 09a-audit | `bash -c docker exec db1 poolwarden --operator tester audit` | 0 | PASS |
| 09b-verify | `bash -c docker exec db1 poolwarden --operator tester audit verify` | 0 | PASS |
| 09c-sessions | `bash -c docker exec db1 poolwarden --operator tester audit sessions` | 0 | PASS |
| 09d-sql | `bash -c docker exec -e PGUSER=postgres -e PGDATABASE=postgres db1 poolwarden --operator tester sql primary 'select pg_is_in_recovery()'` | 0 | PASS |
| 09e-sql-multi | `bash -c docker exec -e PGUSER=postgres -e PGDATABASE=postgres db1 poolwarden --operator tester sql standby 'select 1; select 2'` | 1 | PASS |
| 10a-diagnose | `bash -c docker exec db1 poolwarden --operator tester diagnose` | 10 | PASS |
| 10b-status | `bash -c docker exec db1 poolwarden --operator tester status` | 10 | PASS |
