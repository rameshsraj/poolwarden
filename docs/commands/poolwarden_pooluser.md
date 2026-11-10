## poolwarden pooluser

manage pool_passwd users

### Synopsis

Manage pool_passwd entries. md5 passwords are computed in-process
(md5 of password+username — pg_md5 -p needs a TTY) and the file is
rewritten atomically; AES mode calls pg_enc -i <tmpfile> with the
password in a 0600 scratch file, never on argv or in the audit argv.
AES is chosen when existing pool_passwd entries are AES-prefixed.

### Options

```
  -h, --help   help for pooluser
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
* [poolwarden pooluser add](poolwarden_pooluser_add.md)	 - 
* [poolwarden pooluser list](poolwarden_pooluser_list.md)	 - 
* [poolwarden pooluser passwd](poolwarden_pooluser_passwd.md)	 - 
* [poolwarden pooluser remove](poolwarden_pooluser_remove.md)	 - 

