## poolwarden pooluser

manage pool_passwd users

### Synopsis

Manage pool_passwd entries. Passwords are written via pg_md5 -m -p
(md5 hashing) or pg_enc -m -k <keyfile> -p (AES) with the password fed on
stdin — never on argv. pg_enc is chosen when existing pool_passwd entries
are AES-prefixed; otherwise pg_md5 is used.

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

