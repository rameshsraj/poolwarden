## poolwarden pcpuser

manage pcp.conf users

### Options

```
  -h, --help   help for pcpuser
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
* [poolwarden pcpuser add](poolwarden_pcpuser_add.md)	 - 
* [poolwarden pcpuser list](poolwarden_pcpuser_list.md)	 - 
* [poolwarden pcpuser passwd](poolwarden_pcpuser_passwd.md)	 - 
* [poolwarden pcpuser remove](poolwarden_pcpuser_remove.md)	 - 

