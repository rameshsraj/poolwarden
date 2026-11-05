## poolwarden audit

view the audit log

```
poolwarden audit [flags]
```

### Options

```
      --action string     
  -h, --help              help for audit
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
  -q, --quiet            suppress non-essential output
      --read-only        disable all control commands
      --yes              skip the plain yes/no prompt (never a typed confirmation)
```

### SEE ALSO

* [poolwarden](poolwarden.md)	 - pgpool-II command center
* [poolwarden audit list](poolwarden_audit_list.md)	 - recent audit entries
* [poolwarden audit sessions](poolwarden_audit_sessions.md)	 - shell sessions grouped by session id
* [poolwarden audit show](poolwarden_audit_show.md)	 - full entry with timing breakdown
* [poolwarden audit verify](poolwarden_audit_verify.md)	 - recompute the hash chain

