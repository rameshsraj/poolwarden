## poolwarden logs

tail and classify pgpool/PG logs

```
poolwarden logs [flags]
```

### Options

```
      --classify           add a classification tag column
      --component string   pgpool|pg
  -f, --follow             follow live
      --grep string        filter by regular expression
  -h, --help               help for logs
      --host string        limit to one host
      --since string       look back this far (default 1h)
```

### Options inherited from parent commands

```
  -c, --config string     poolwarden config file (default "/etc/poolwarden.yaml")
      --confirm string    typed confirmation token for non-TTY use
      --force             override pre-flight WARN/NO-GO blocks
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

* [poolwarden](poolwarden.md)	 - pgpool-II command center

