## poolwarden service

start, stop or restart pgpool/PG services on a host

```
poolwarden service <start|stop|restart|status> <pgpool|postgresql|watchdog> <host> [flags]
```

### Options

```
      --confirm string   confirmation token
  -h, --help             help for service
      --ticket string    change ticket
      --yes              skip the confirmation
```

### Options inherited from parent commands

```
  -c, --config string     poolwarden config file (default "/etc/poolwarden.yaml")
      --force             override pre-flight WARN/NO-GO blocks
      --nagios            Nagios-style single-line output and exit codes
      --no-color          disable coloured output
      --operator string   operator name (recorded in the audit log)
  -o, --output string     output format: table|wide|json (default "table")
      --plan              control commands: print preflight+plan+token, do not execute
      --progress          control commands: emit NDJSON progress events on stderr
  -q, --quiet             suppress non-essential output
      --read-only         disable all control commands
```

### SEE ALSO

* [poolwarden](poolwarden.md)	 - pgpool-II command center

