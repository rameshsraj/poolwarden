#!/bin/bash
# run-scenarios.sh <colocated|split> — execute on the server from /root/poolwarden/dev/testbed.
# Writes each step's output to results/<bed>/NN-<name>.txt and a SUMMARY.md.
set -u
BED=$1
cd "$(dirname "$0")"
RES="results/$BED"
mkdir -p "$RES"

if [ "$BED" = colocated ]; then
  PWNODE=db1; PGNODE=db1; OTHER=db2; PGPOOLS="db1 db2"
else
  PWNODE=pgpool1; PGNODE=pg1; OTHER=pg2; PGPOOLS="pgpool1 pgpool2"
fi
PGDATA=/var/lib/postgresql/18/main
PSQL='PGPASSWORD=pgpool psql -h 127.0.0.1 -p 9999 -U postgres -A -t -c'

PW() { docker exec "$PWNODE" poolwarden "$@"; }
STEPN=0
step() {
  local name=$1 want=$2; shift 2
  local f; f=$(printf "%s/%02d-%s.txt" "$RES" "$STEPN" "$name"); STEPN=$((STEPN+1))
  echo "== $ $*" > "$f"
  "$@" >> "$f" 2>&1
  local rc=$?
  echo "== exit: $rc" >> "$f"
  local verdict=FAIL
  for w in ${want//|/ }; do [ "$rc" = "$w" ] && verdict=PASS; done
  echo "| $name | \`$*\` | $rc | $verdict |" >> "$RES/SUMMARY.md"
  echo "--- $name rc=$rc $verdict"
}

{
  echo "# SUMMARY — $BED"
  echo "| step | command | exit | result |"
  echo "|---|---|---|---|"
} > "$RES/SUMMARY.md"

# ---- 00 readiness ------------------------------------------------------------
step 00-ready 0 bash -c "
  for i in \$(seq 1 60); do
    ups=\$(docker exec $PWNODE bash -c \"$PSQL 'SHOW POOL_NODES'\" 2>/dev/null | grep -c '|up|')
    wd=\$(docker exec $PWNODE bash -c 'PCPPASSFILE=/root/.pcppass pcp_watchdog_info -h 127.0.0.1 -w 2>/dev/null | grep -c QUORUM')
    if [ \"\$ups\" -ge 2 ]; then echo \"pool up (\$ups), wd=\$wd\"; exit 0; fi
    sleep 3
  done; echo TIMEOUT; exit 1"

# ---- 01 one-shot views ---------------------------------------------------------
step 01a-status    "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester status"
step 01b-nodes     "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"
step 01c-lag       "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester lag"
step 01d-watchdog  "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester watchdog"
step 01e-health    "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester health"
step 01f-pools     "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester pools"
step 01g-topology  "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester topology show"
step 01h-diagnose  "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester diagnose"
step 01i-forecast  "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester forecast"
step 01j-selfcheck "0|10" bash -c "docker exec $PWNODE poolwarden --operator tester config self-check"
step 01k-errors    0 bash -c "docker exec $PWNODE poolwarden --operator tester errors --since 10m"
step 01l-logs      0 bash -c "docker exec $PWNODE poolwarden --operator tester logs --classify --since 10m"

# ---- 02 failover --------------------------------------------------------------
step 02a-kill-pg0   0 docker exec $PGNODE su postgres -c "pg_ctl -D $PGDATA stop -m immediate -w"
step 02b-wait-flip  0 bash -c "
  for i in \$(seq 1 40); do
    st=\$(docker exec $PWNODE bash -c \"$PSQL 'SHOW POOL_NODES'\" 2>/dev/null)
    echo \"\$st\"
    echo \"\$st\" | grep -q ' down ' || echo \"\$st\" | grep -q quarantine || true
    n1=\$(echo \"\$st\" | sed -n '2p')
    echo \"\$n1\" | grep -qi 'primary' && echo FLIPPED && exit 0
    sleep 3
  done; exit 1"
step 02c-nodes      "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"
step 02d-diagnose   "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester diagnose"
step 02e-errors     0 bash -c "docker exec $PWNODE poolwarden --operator tester errors --since 10m"
step 02f-logs       0 bash -c "docker exec $PWNODE poolwarden --operator tester logs --classify --since 10m"
step 02g-watchdog   "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester watchdog"
step 02h-audit      0 bash -c "docker exec $PWNODE poolwarden --operator tester audit"

# ---- 03 recover old primary as standby ----------------------------------------
# follow_primary_command may already have re-attached node 0; force a genuine
# online recovery: stop its PG again and wait for pgpool to mark it down.
step 03pre-force-down 0 bash -c "
  docker exec $PGNODE su postgres -c 'pg_ctl -D $PGDATA stop -m immediate -w' || true
  for i in \$(seq 1 40); do
    st=\$(docker exec $PWNODE bash -c \"$PSQL 'SHOW POOL_NODES'\" 2>/dev/null | sed -n '1p')
    echo \"\$st\" | grep -q '|down|' && echo NODE0-DOWN && exit 0
    sleep 3
  done; echo TIMEOUT; exit 1"
step 03a-recover    0 bash -c "docker exec $PWNODE poolwarden --operator tester recover 0 --yes --confirm 0 --ticket TB-1"
step 03b-nodes      "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"
step 03c-lag        "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester lag"

# ---- 04 switchover back --------------------------------------------------------
step 04a-promote0   0 bash -c "docker exec $PWNODE poolwarden --operator tester promote 0 --yes --confirm 0 --ticket TB-1"
step 04b-nodes      "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"
step 04c-lag        "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester lag"
step 04d-diagnose   "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester diagnose"
step 04e-audit-show 0 bash -c "docker exec $PWNODE poolwarden --operator tester audit"

# ---- 05 switchover again --------------------------------------------------------
step 05a-promote1   0 bash -c "docker exec $PWNODE poolwarden --operator tester promote 1 --yes --confirm 1 --ticket TB-1"
step 05b-nodes      "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"

# ---- 06 detach/attach -----------------------------------------------------------
step 06a-detach     0 bash -c "docker exec $PWNODE poolwarden --operator tester detach 0 --yes --confirm 0 --ticket TB-1"
step 06b-nodes      "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"
step 06c-attach     0 bash -c "docker exec $PWNODE poolwarden --operator tester attach 0 --yes --confirm 0 --ticket TB-1"
step 06d-nodes      "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"

# ---- 07 watchdog VIP failover ---------------------------------------------------
LEADER=$(docker exec $PWNODE bash -c 'PCPPASSFILE=/root/.pcppass pcp_watchdog_info -h 127.0.0.1 -w -v 2>/dev/null' | awk -F': *' '/Leader Host Name/{print $2}')
WDOFF=${LEADER:-${PGPOOLS%% *}}; WDON=$(for p in $PGPOOLS; do [ "$p" != "$WDOFF" ] && echo "$p"; done | head -1)
echo "watchdog leader: $WDOFF, observer: $WDON"
step 07a-stop-wd-leader 0 docker exec $WDOFF bash -c 'pgpool -m fast stop || kill $(pgrep -x watchdog) 2>/dev/null; sleep 8; echo stopped'
step 07b-watchdog   "0|10|11" bash -c "docker exec $WDON poolwarden --operator tester watchdog"
step 07c-start-wd   0 docker exec $WDOFF bash -c 'pgpool -n >/tmp/pgpool-restart.log 2>&1 & sleep 5; pgrep -x watchdog || pgrep pgpool | head -1'
step 07d-watchdog   "0|10|11" bash -c "docker exec $WDON poolwarden --operator tester watchdog"

# ---- 07e service control (svc on hosts via `poolwarden service`) ----------------
# exercise the admin verbs on the non-leader pgpool node and on postgresql
SVCH=${WDON:-${PGPOOLS%% *}}
step 07e-svc-status   0 bash -c "docker exec $PWNODE poolwarden --operator tester service status pgpool $SVCH"
step 07f-svc-restart  0 bash -c "docker exec $PWNODE poolwarden --operator tester service restart pgpool $SVCH --yes --confirm restart --ticket TB-1"
step 07g-watchdog     "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester watchdog"
step 07h-svc-pg       0 bash -c "docker exec $PWNODE poolwarden --operator tester service status postgresql $OTHER"
step 07i-svc-stop     0 bash -c "docker exec $PWNODE poolwarden --operator tester service stop postgresql $OTHER --yes --confirm stop --ticket TB-1"
step 07j-nodes        "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"
step 07k-svc-start    0 bash -c "docker exec $PWNODE poolwarden --operator tester service start postgresql $OTHER --yes --confirm start --ticket TB-1"
# node may already be re-attached by pgpool health-check (NO-GO=3 is acceptable)
step 07l-svc-attach   "0|3" bash -c "docker exec $PWNODE poolwarden --operator tester attach 1 --yes --confirm 1 --ticket TB-1"
step 07m-nodes        "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester nodes"
step 07n-svc-plan     0 bash -c "docker exec $PWNODE poolwarden --operator tester service restart pgpool $SVCH --plan"

# ---- 08 config apply/rollback ---------------------------------------------------
step 08a-set        0 bash -c "docker exec $PWNODE poolwarden --operator tester config set health_check_period 7"
step 08b-diff       0 bash -c "docker exec $PWNODE poolwarden --operator tester config diff"
step 08c-validate   0 bash -c "docker exec $PWNODE poolwarden --operator tester config validate"
step 08d-apply      0 bash -c "docker exec $PWNODE poolwarden --operator tester config apply --yes --confirm apply --ticket TB-1"
step 08e-get        0 bash -c "docker exec $PWNODE poolwarden --operator tester config get health_check_period"
step 08f-history    0 bash -c "docker exec $PWNODE poolwarden --operator tester config history"
step 08g-rollback   0 bash -c "ver=\$(docker exec $PWNODE poolwarden --operator tester config history | tail -1 | awk '{print \$1}'); docker exec $PWNODE poolwarden --operator tester config rollback \$ver --yes --confirm apply --ticket TB-1"
step 08h-get        0 bash -c "docker exec $PWNODE poolwarden --operator tester config get health_check_period"

# ---- 09 audit + sql -------------------------------------------------------------
step 09a-audit      0 bash -c "docker exec $PWNODE poolwarden --operator tester audit"
step 09b-verify     0 bash -c "docker exec $PWNODE poolwarden --operator tester audit verify"
step 09c-sessions   0 bash -c "docker exec $PWNODE poolwarden --operator tester audit sessions"
step 09d-sql        0 bash -c "docker exec -e PGUSER=postgres -e PGDATABASE=postgres $PWNODE poolwarden --operator tester sql primary 'select pg_is_in_recovery()'"
step 09e-sql-multi  1 bash -c "docker exec -e PGUSER=postgres -e PGDATABASE=postgres $PWNODE poolwarden --operator tester sql standby 'select 1; select 2'"

# ---- 10 final -------------------------------------------------------------------
step 10a-diagnose   "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester diagnose"
step 10b-status     "0|10|11" bash -c "docker exec $PWNODE poolwarden --operator tester status"
