#!/bin/bash
exec >> /tmp/pgpool_remote_start.log 2>&1
echo "=== $(date) $@"
# pgpool_remote_start.sh — start PG on the recovered node.
# args: $1 dest host  $2 dest data dir  $3 dest port
ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 "postgres@$1" \
  "/usr/lib/postgresql/18/bin/pg_ctl -D '$2' -w -t 90 -l /tmp/pg_start.log start > /dev/null 2>&1 < /dev/null"
