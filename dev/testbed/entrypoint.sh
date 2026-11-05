#!/bin/bash
# poolwarden test-bed entrypoint.
# env: ROLE (csv of pg,pgpool), NODE_ID, PRIMARY_HOST, PEERS (all pgpool nodes),
#      PG_HOSTS (backend hosts csv/space), VIP, CLUSTER, SUBNET, INVENTORY (pre-rendered YAML lines file? no—rendered here)
set -u
PGDATA=/var/lib/postgresql/18/main
PGBIN=/usr/lib/postgresql/18/bin
export PATH=$PGBIN:$PATH
CONF=/etc/pgpool-II
PGPOOLPW=pgpool

has_role() { case ",$ROLE," in *",$1,"*) return 0;; *) return 1;; esac; }

# --- 1. sshd ---------------------------------------------------------------
mkdir -p /run/sshd
ssh-keygen -A >/dev/null
/usr/sbin/sshd

# --- 2. known_hosts (poolwarden verifies keys) ------------------------------
ALL_NODES=$(echo "${PEERS:-} ${PG_HOSTS:-}" | tr ' ' '\n' | sort -u | tr '\n' ' ')
: > /root/.ssh/known_hosts
deadline=$((SECONDS+120))
for n in $ALL_NODES; do
  ip=$(getent hosts "$n" | awk '{print $1}')
  until ssh-keyscan -T 4 "${n}${ip:+,$ip}" >> /root/.ssh/known_hosts 2>/dev/null; do
    [ $SECONDS -gt $deadline ] && { echo "keyscan timeout on $n"; break; }
    sleep 2
  done
done
cp /root/.ssh/known_hosts /var/lib/postgresql/.ssh/known_hosts
# pgpool runs as root here, so failover scripts ssh as root -> postgres@peer
grep -qxF "$(cat /root/.ssh/id_ed25519.pub)" /var/lib/postgresql/.ssh/authorized_keys || \
  cat /root/.ssh/id_ed25519.pub >> /var/lib/postgresql/.ssh/authorized_keys
chown postgres:postgres /var/lib/postgresql/.ssh/known_hosts
chmod 600 /root/.ssh/known_hosts

# --- 3. passfiles ------------------------------------------------------------
cat > /root/.pgpass <<'PE'
*:9999:postgres:postgres:pgpool
*:9999:*:pgpool:pgpool
*:5432:*:postgres:pgpool
*:5432:*:pgpool:pgpool
PE
cat > /root/.pcppass <<'PE'
*:9898:postgres:pgpool
PE
cat > /var/lib/postgresql/.pgpass <<'PE'
*:5432:*:repl:pgpool
*:5432:*:postgres:pgpool
PE
chmod 600 /root/.pgpass /root/.pcppass /var/lib/postgresql/.pgpass
chown postgres:postgres /var/lib/postgresql/.pgpass

# --- 4. PostgreSQL -----------------------------------------------------------
if has_role pg; then
  mkdir -p "$PGDATA" && chown -R postgres:postgres "$PGDATA" && chmod 700 "$PGDATA"
  if [ ! -s "$PGDATA/PG_VERSION" ]; then
    if [ "$(hostname)" = "$PRIMARY_HOST" ]; then
      echo pgpool > /tmp/pw && chmod 644 /tmp/pw
      su postgres -c "initdb -D '$PGDATA' -A md5 --pwfile=/tmp/pw"
      rm -f /tmp/pw
    else
      until su postgres -c "PGPASSWORD=pgpool pg_basebackup -h '$PRIMARY_HOST' -p 5432 -U repl \
        -D '$PGDATA' -R -X stream -v --checkpoint=fast"; do
        rm -rf "$PGDATA"; install -d -m 700 -o postgres "$PGDATA"; sleep 3
      done
    fi
  fi
  # common postgresql.conf settings (idempotent)
  if [ -s "$PGDATA/postgresql.conf" ] && ! grep -q '# testbed' "$PGDATA/postgresql.conf"; then
    cat >> "$PGDATA/postgresql.conf" <<'PC'
# testbed
listen_addresses = '*'
wal_level = replica
hot_standby = on
wal_keep_size = 512MB
max_wal_senders = 10
wal_log_hints = on
password_encryption = md5
logging_collector = on
log_directory = 'log'
log_filename = 'postgresql-%Y-%m-%d_%H%M%S.log'
log_destination = 'stderr'
log_line_prefix = '%m [%p] '
PC
    echo "host all all $SUBNET md5" >> "$PGDATA/pg_hba.conf"
    echo "host replication repl $SUBNET md5" >> "$PGDATA/pg_hba.conf"
  fi
  if ! su postgres -c "pg_ctl -D '$PGDATA' status" >/dev/null 2>&1; then
    su postgres -c "pg_ctl -D '$PGDATA' -w -t 90 -l /tmp/pg.log start"
  fi
  # first-boot users + extension on the primary
  if [ "$(hostname)" = "$PRIMARY_HOST" ] && [ ! -f "$PGDATA/../users.done" ]; then
    cat > /tmp/init.sql <<'SQL'
CREATE USER repl WITH REPLICATION LOGIN PASSWORD 'pgpool';
CREATE USER pgpool WITH SUPERUSER LOGIN PASSWORD 'pgpool';
ALTER USER postgres WITH PASSWORD 'pgpool';
CREATE EXTENSION IF NOT EXISTS pgpool_recovery;
SQL
    su postgres -c "psql -v ON_ERROR_STOP=1 -f /tmp/init.sql postgres" && \
    su postgres -c "psql -v ON_ERROR_STOP=1 -c 'CREATE EXTENSION IF NOT EXISTS pgpool_recovery' template1" && \
    touch "$PGDATA/../users.done"
  fi
  # online recovery scripts run from PGDATA on the primary (pgpool_recovery extension)
  install -o postgres -g postgres -m 0755 /opt/testbed/scripts/recovery_1st_stage.sh "$PGDATA/recovery_1st_stage.sh"
  # pgpool_remote_start() executes $PGDATA/pgpool_remote_start (no extension)
  install -o postgres -g postgres -m 0755 /opt/testbed/scripts/pgpool_remote_start.sh "$PGDATA/pgpool_remote_start"
fi

# --- 5. pgpool ---------------------------------------------------------------
if has_role pgpool; then
  ARPING="/usr/bin/arping -U $VIP -I eth0 -w 1"
  [ -x /usr/bin/arping ] || ARPING="/bin/true"
  {
    sed -e "s|@HOSTNAME@|$(hostname)|" -e "s|@VIP@|$VIP|g" \
        -e "s|@ARPING@|$ARPING|" /opt/testbed/pgpool.conf.template.head
    sed -e "s|@VIP@|$VIP|g" -e "s|@ARPING@|$ARPING|" /opt/testbed/pgpool.conf.template.mid > /tmp/mid.rendered
    i=0
    for h in $PG_HOSTS; do
      cat <<B
backend_hostname$i = '$h'
backend_port$i = 5432
backend_weight$i = 1
backend_data_directory$i = '/var/lib/postgresql/18/main'
backend_flag$i = 'ALLOW_TO_FAILOVER'
backend_application_name$i = '$h'
B
      i=$((i+1))
    done
    cat /tmp/mid.rendered
    j=0
    for p in $PEERS; do
      cat <<W
hostname$j = '$p'
wd_port$j = 9000
pgpool_port$j = 9999
heartbeat_hostname$j = '$p'
heartbeat_port$j = 9694
heartbeat_device$j = ''
W
      j=$((j+1))
    done
    echo "$NODE_ID" > "$CONF/pgpool_node_id"
    cat /opt/testbed/pgpool.conf.template.tail
  } > "$CONF/pgpool.conf"
  cp /opt/testbed/pool_hba.conf "$CONF/pool_hba.conf"
  echo "postgres:$(echo -n "$PGPOOLPW" | md5sum | cut -d' ' -f1)" > "$CONF/pcp.conf"
  # pool_passwd: user:md5(md5(password||username)) — pg_md5 -p needs a TTY, compute directly
  : > "$CONF/pool_passwd"
  for u in postgres pgpool repl; do
    echo "$u:md5$(echo -n "${PGPOOLPW}${u}" | md5sum | cut -d' ' -f1)" >> "$CONF/pool_passwd"
  done
  chmod 600 "$CONF/pool_passwd" "$CONF/pcp.conf"
  cp /opt/testbed/scripts/failover.sh /opt/testbed/scripts/follow_primary.sh \
     /opt/testbed/scripts/recovery_1st_stage.sh /opt/testbed/scripts/pgpool_remote_start.sh \
     "$CONF/"
  # compiled sysconfdir is /usr/etc — pcp tools + pgpool read confs there
  mkdir -p /usr/etc
  ln -sf "$CONF/pgpool.conf" "$CONF/pool_hba.conf" "$CONF/pcp.conf" \
     "$CONF/pool_passwd" /usr/etc/
  chmod +x "$CONF"/*.sh
  # wait for all backends to accept connections before starting pgpool
  for h in $PG_HOSTS; do
    until PGPASSWORD=pgpool pg_isready -h "$h" -p 5432 -q; do sleep 2; done
  done
  mkdir -p /var/run/pgpool
  pgpool -n -f "$CONF/pgpool.conf" &
  PGPOOL_PID=$!
fi

# --- 6. poolwarden.yaml -------------------------------------------------------
# render poolwarden.yaml directly (multi-line inventory can't go through sed)
{
  sed "s|@CLUSTER@|${CLUSTER:-testbed}|" /opt/testbed/poolwarden.yaml.tmpl | grep -v '^@INVENTORY@$'
  IFS=';' read -ra HOSTSPEC <<< "${INVENTORY:-}"
  for spec in "${HOSTSPEC[@]}"; do
    IFS=':' read -r nm ip roles <<< "$spec"
    printf "  - {name: %s, address: %s, ssh_user: root, roles: [%s]" "$nm" "$ip" "$(echo "$roles" | sed 's/,/, /g')"
    case ",$roles," in
      *pgpool*) printf ", pgpool: {conf: /etc/pgpool-II/pgpool.conf, hba: /etc/pgpool-II/pool_hba.conf, pcp_conf: /etc/pgpool-II/pcp.conf, pool_passwd: /etc/pgpool-II/pool_passwd, log_dir: /var/log/pgpool-II, log_line_prefix: '%%m: %%a pid %%p: ', service: pgpool-II}" ;;
    esac
    case ",$roles," in
      *,pg,*) printf ", pg: {data_dir: /var/lib/postgresql/18/main, log_dir: /var/lib/postgresql/18/main/log, log_filename: 'postgresql-%%Y-%%m-%%d_%%H%%M%%S.log', log_destination: stderr, log_line_prefix: '%%m [%%p] '}" ;;
    esac
    printf "}\n"
  done
} > /etc/poolwarden.yaml
chmod 600 /etc/poolwarden.yaml

# --- 7. stay alive -------------------------------------------------------------
if has_role pgpool; then wait $PGPOOL_PID; else exec sleep infinity; fi
