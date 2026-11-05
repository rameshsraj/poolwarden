## poolwarden errors

unified error view across all sources

```
poolwarden errors [flags]
```

### Options

```
  -f, --follow            follow live
      --grep string       filter by regular expression
  -h, --help              help for errors
      --host string       limit to one host
      --node string       limit to one node id
      --severity string   minimum severity
      --since string      look back this far (default 1h)
      --source string     pgpool|pg|conn|health|replication|pcp|db|tool (comma list)
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
  -q, --quiet             suppress non-essential output
      --read-only         disable all control commands
      --ticket string     change ticket id (audited when given)
      --yes               skip the plain yes/no prompt (never a typed confirmation)
```

### SEE ALSO

* [poolwarden](poolwarden.md)	 - pgpool-II command center
* [poolwarden errors show](poolwarden_errors_show.md)	 - show one event with context
* [poolwarden errors summary](poolwarden_errors_summary.md)	 - aggregate error counts by source

