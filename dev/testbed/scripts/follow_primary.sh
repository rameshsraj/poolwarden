#!/bin/bash
exec >> /var/log/pgpool-II/follow_primary.log 2>&1
echo "=== $(date) $@"
# follow_primary.sh — run once per standby that must follow the new primary.
# args: %d node_id %h host %p port %D datadir %m new_id %H new_host %M old_main_id %P old_primary_id %r new_port %R new_datadir
set -u
HOST=$2
DATA=$4
NEW_HOST=$6
NEW_PORT=$9
echo "follow_primary: rewinding $HOST to follow $NEW_HOST:$NEW_PORT"
ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 "postgres@$HOST" "
  set -e
  /usr/lib/postgresql/18/bin/pg_ctl -D '$DATA' -m fast -w -t 60 stop || true
  if /usr/lib/postgresql/18/bin/pg_rewind -D '$DATA' --source-server='host=$NEW_HOST port=$NEW_PORT user=postgres password=pgpool'; then
    printf \"primary_conninfo = 'host=$NEW_HOST port=$NEW_PORT user=repl password=pgpool application_name=%s'\n\" '$HOST' >> '$DATA/postgresql.auto.conf'
    touch '$DATA/standby.signal'
  else
    echo 'pg_rewind failed: falling back to pg_basebackup (pgpool sample-script behaviour)'
    rm -rf '$DATA'
    PGPASSWORD=pgpool /usr/lib/postgresql/18/bin/pg_basebackup -h '$NEW_HOST' -p '$NEW_PORT' -U repl -D '$DATA' -R -X stream --checkpoint=fast
  fi
  /usr/lib/postgresql/18/bin/pg_ctl -D '$DATA' -w -t 90 -l /tmp/pg_start.log start > /dev/null 2>&1 < /dev/null
"
# re-attach the re-pointed standby to pgpool (pgpool sample-script behaviour)
NODE_ID=$1
for i in $(seq 1 30); do
  PCPPASSFILE=/root/.pcppass pcp_attach_node -h 127.0.0.1 -p 9898 -U postgres -w -n "$NODE_ID" && break
  sleep 2
done
