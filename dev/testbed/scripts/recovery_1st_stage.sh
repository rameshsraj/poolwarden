#!/bin/bash
exec >> /tmp/recovery_1st_stage.log 2>&1
echo "=== $(date) $@"
# recovery_1st_stage.sh — runs ON the primary via pgpool_recovery().
# args: $1 primary data dir  $2 dest host  $3 dest data dir  $4 primary port  $5 dest node id  $6 dest port
set -u
PRIMARY_DATA=$1
DEST=$2
DEST_DATA=$3
DEST_PORT=${6:-5432}
PRIMARY_HOST=$(hostname)
echo "recovery_1st_stage: re-baselining $DEST from $PRIMARY_HOST"
ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 "postgres@$DEST" "
  set -e
  /usr/lib/postgresql/18/bin/pg_ctl -D '$DEST_DATA' -m fast -w -t 60 stop || true
  rm -rf '$DEST_DATA'
  PGPASSWORD=pgpool /usr/lib/postgresql/18/bin/pg_basebackup -h '$PRIMARY_HOST' -p 5432 -U repl \
    -D '$DEST_DATA' -R -X stream -v --checkpoint=fast
"
