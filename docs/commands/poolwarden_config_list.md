## poolwarden config list



```
poolwarden config list [flags]
```

### Options

```
      --changed       only non-default values
      --drift         only params where file != live
      --grep string   filter by substring
  -h, --help          help for list
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

* [poolwarden config](poolwarden_config.md)	 - manage pgpool configuration without editing files

