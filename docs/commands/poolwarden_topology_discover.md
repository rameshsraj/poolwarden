## poolwarden topology discover

diff live cluster vs inventory, optionally stage

```
poolwarden topology discover [flags]
```

### Options

```
      --apply            write the discovered changes into the config
      --confirm string   confirmation token
  -h, --help             help for discover
      --ticket string    change ticket
      --yes              skip the y/N confirmation
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

* [poolwarden topology](poolwarden_topology.md)	 - cluster host inventory and roles

