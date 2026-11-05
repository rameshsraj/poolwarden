#!/bin/bash
exec >> /var/log/pgpool-II/failover.log 2>&1
echo "=== $(date) $@"
# failover.sh — promote the new primary over ssh (pgpool 4.5 docs pattern).
# args: %d failed_id %h failed_host %p failed_port %D failed_datadir %m new_id %H new_host %M old_main_id %P old_primary_id %r new_port %R new_datadir
set -u
NEW_HOST=$6
NEW_DATA=${10}
echo "failover: promoting $NEW_HOST ($NEW_DATA)"
ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 "postgres@$NEW_HOST" \
  "/usr/lib/postgresql/18/bin/pg_ctl -D '$NEW_DATA' -w -t 90 promote"
