## poolwarden audit list

recent audit entries

```
poolwarden audit list [flags]
```

### Options

```
      --action string     
  -h, --help              help for list
      --operator string   
      --outcome string    
      --since string      look back this far (default 24h)
      --ticket string     
```

### Options inherited from parent commands

```
  -c, --config string    poolwarden config file (default "/etc/poolwarden.yaml")
      --confirm string   typed confirmation token for non-TTY use
      --force            override pre-flight WARN/NO-GO blocks
      --nagios           Nagios-style single-line output and exit codes
      --no-color         disable coloured output
  -o, --output string    output format: table|wide|json (default "table")
      --plan             control commands: print preflight+plan+token, do not execute
      --progress         control commands: emit NDJSON progress events on stderr
  -q, --quiet            suppress non-essential output
      --read-only        disable all control commands
      --yes              skip the plain yes/no prompt (never a typed confirmation)
```

### SEE ALSO

* [poolwarden audit](poolwarden_audit.md)	 - view the audit log

