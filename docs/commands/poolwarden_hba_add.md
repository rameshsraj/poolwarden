## poolwarden hba add



```
poolwarden hba add [flags]
```

### Options

```
      --addr string     address/CIDR
      --db string       database
  -h, --help            help for add
      --method string   auth method
      --position int    insert position
      --type string     local|host|hostssl|hostnossl
      --user string     user
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

* [poolwarden hba](poolwarden_hba.md)	 - manage pool_hba.conf

