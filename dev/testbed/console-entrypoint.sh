#!/bin/bash
# poolwarden console container entrypoint (testbed only).
# Renders /etc/poolwarden.yaml pointing at the first pgpool host over the
# docker network, scans ssh host keys, then runs poolwconsole.
# env: PGPOOL_EP (pgpool host for SQL+PCP, e.g. db1), INVENTORY (same format as
# the node entrypoint), CLUSTER, CONSOLE_TOKEN.
set -u

PGPOOL_EP="${PGPOOL_EP:?set PGPOOL_EP}"
CLUSTER="${CLUSTER:-testbed}"
TOKEN="${CONSOLE_TOKEN:-console-test}"

mkdir -p /var/lib/poolwarden /root/.ssh
cp -f /etc/poolwarden_test_key /root/.ssh/id_ed25519
cp -f /etc/poolwarden_test_key.pub /root/.ssh/id_ed25519.pub
chmod 600 /root/.ssh/id_ed25519

# passfiles for remote pgpool SQL + pcp (testbed passwords)
cat > /root/.pgpass <<'PE'
*:9999:postgres:postgres:pgpool
*:9999:*:pgpool:pgpool
*:5432:*:postgres:pgpool
*:5432:*:pgpool:pgpool
PE
cat > /root/.pcppass <<'PE'
*:9898:postgres:pgpool
PE
chmod 600 /root/.pgpass /root/.pcppass

# known_hosts for every inventory host
: > /root/.ssh/known_hosts
IFS=';' read -ra HOSTSPEC <<< "${INVENTORY:-}"
for spec in "${HOSTSPEC[@]}"; do
  IFS=':' read -r nm ip _ <<< "$spec"
  ssh-keyscan -T 4 "$nm" "$ip" >> /root/.ssh/known_hosts 2>/dev/null || true
done

# poolwarden.yaml: pgpool/pcp endpoints point at $PGPOOL_EP; ssh_user root
# uses the baked test key.
cat > /etc/poolwarden.yaml <<EOF
cluster: $CLUSTER
state_dir: /var/lib/poolwarden
pgpool: {host: $PGPOOL_EP, port: 9999, user: postgres, dbname: postgres, passfile: /root/.pgpass}
pcp:    {host: $PGPOOL_EP, port: 9898, user: postgres, bin_dir: /usr/bin, passfile: /root/.pcppass}
bins:   {systemctl: /usr/local/bin/svc, ip: /usr/sbin/ip}
ssh:    {key: /root/.ssh/id_ed25519, known_hosts: /root/.ssh/known_hosts, timeout: 10s}
interval: 2s
timeout: 5s
lag: {warn_seconds: 1800, crit_seconds: 3600}
wal_volume_bytes: "0"
audit: {file: /var/lib/poolwarden/audit.jsonl, syslog: false, read_commands: false, retention_days: 31}
hosts:
EOF
for spec in "${HOSTSPEC[@]}"; do
  IFS=':' read -r nm ip roles <<< "$spec"
  printf "  - {name: %s, address: %s, ssh_user: root, roles: [%s]" "$nm" "$ip" "$(echo "$roles" | sed 's/,/, /g')" >> /etc/poolwarden.yaml
  case ",$roles," in
    *pgpool*) printf ", pgpool: {conf: /etc/pgpool-II/pgpool.conf, hba: /etc/pgpool-II/pool_hba.conf, pcp_conf: /etc/pgpool-II/pcp.conf, pool_passwd: /etc/pgpool-II/pool_passwd, log_dir: /var/log/pgpool-II, log_line_prefix: '%%m: %%a pid %%p: ', service: pgpool-II}" >> /etc/poolwarden.yaml ;;
  esac
  case ",$roles," in
    *,pg,*) printf ", pg: {port: 5432, data_dir: /var/lib/postgresql/18/main, log_dir: /var/lib/postgresql/18/main/log, log_filename: 'postgresql-%%Y-%%m-%%d_%%H%%M%%S.log', log_destination: stderr, log_line_prefix: '%%m [%%p] '}" >> /etc/poolwarden.yaml ;;
  esac
  printf "}\n" >> /etc/poolwarden.yaml
done
chmod 600 /etc/poolwarden.yaml

echo "poolwarden console config:"
grep -E 'cluster|pgpool:|pcp:|name:' /etc/poolwarden.yaml | head -8
exec /usr/bin/poolwconsole --addr 0.0.0.0:8080 --token "$TOKEN" \
  --cli /usr/bin/poolwarden --config /etc/poolwarden.yaml --state /var/lib/poolwarden
