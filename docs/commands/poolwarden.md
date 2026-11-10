## poolwarden

pgpool-II command center

### Synopsis

poolwarden is a terminal command center for one pgpool-II cluster:
monitoring, diagnosis, node control and configuration management.

```
poolwarden [flags]
```

### Options

```
  -c, --config string     poolwarden config file (default "/etc/poolwarden.yaml")
      --confirm string    typed confirmation token for non-TTY use
      --force             override pre-flight WARN/NO-GO blocks
  -h, --help              help for poolwarden
      --nagios            Nagios-style single-line output and exit codes
      --no-color          disable coloured output
      --operator string   operator name (recorded in the audit log)
  -o, --output string     output format: table|wide|json (default "table")
      --plan              control commands: print preflight+plan+token, do not execute
      --progress          control commands: emit NDJSON progress events on stderr
  -q, --quiet             suppress non-essential output
      --read-only         disable all control commands
      --ticket string     change ticket id (audited when given)
      --yes               skip the plain yes/no prompt (never a typed confirmation)
```

### SEE ALSO

* [poolwarden attach](poolwarden_attach.md)	 - attach a node (pre-flight + confirm)
* [poolwarden audit](poolwarden_audit.md)	 - view the audit log
* [poolwarden backend](poolwarden_backend.md)	 - add, remove or change backend nodes
* [poolwarden config](poolwarden_config.md)	 - manage pgpool configuration without editing files
* [poolwarden conns](poolwarden_conns.md)	 - client connections by db, user and application
* [poolwarden detach](poolwarden_detach.md)	 - detach a node (pre-flight + confirm)
* [poolwarden diagnose](poolwarden_diagnose.md)	 - run the diagnosis rules
* [poolwarden errors](poolwarden_errors.md)	 - unified error view across all sources
* [poolwarden explain](poolwarden_explain.md)	 - explain a finding and how to fix it
* [poolwarden forecast](poolwarden_forecast.md)	 - lag, WAL disk and connection projections
* [poolwarden hba](poolwarden_hba.md)	 - manage pool_hba.conf
* [poolwarden health](poolwarden_health.md)	 - health-check and backend stats
* [poolwarden init](poolwarden_init.md)	 - create or update /etc/poolwarden.yaml
* [poolwarden lag](poolwarden_lag.md)	 - replication lag per standby
* [poolwarden logs](poolwarden_logs.md)	 - tail and classify pgpool/PG logs
* [poolwarden monitor](poolwarden_monitor.md)	 - stream cluster state as NDJSON events (for the console)
* [poolwarden nodes](poolwarden_nodes.md)	 - backend nodes: pgpool view vs real role
* [poolwarden pcpuser](poolwarden_pcpuser.md)	 - manage pcp.conf users
* [poolwarden pools](poolwarden_pools.md)	 - connection pools by database and user
* [poolwarden pooluser](poolwarden_pooluser.md)	 - manage pool_passwd users
* [poolwarden probe](poolwarden_probe.md)	 - test every configured connection (pgpool SQL, PG, SSH, watchdog)
* [poolwarden procs](poolwarden_procs.md)	 - pgpool child processes
* [poolwarden promote](poolwarden_promote.md)	 - promote a standby (pre-flight + confirm)
* [poolwarden recover](poolwarden_recover.md)	 - online recovery of a node
* [poolwarden reload](poolwarden_reload.md)	 - reload pgpool configuration
* [poolwarden service](poolwarden_service.md)	 - start, stop or restart pgpool/PG services on a host
* [poolwarden slots](poolwarden_slots.md)	 - replication slots and retained WAL
* [poolwarden sql](poolwarden_sql.md)	 - read-only query on a backend
* [poolwarden status](poolwarden_status.md)	 - one-screen cluster summary
* [poolwarden top](poolwarden_top.md)	 - live overview: status, lag and findings
* [poolwarden topology](poolwarden_topology.md)	 - cluster host inventory and roles
* [poolwarden version](poolwarden_version.md)	 - print poolwarden version and build info
* [poolwarden watch](poolwarden_watch.md)	 - full-screen refresh of a view
* [poolwarden watchdog](poolwarden_watchdog.md)	 - watchdog leader, quorum and VIP

