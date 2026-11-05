# poolwarden

poolwarden is a terminal command center for a single pgpool-II cluster:
monitoring, diagnosis, node control and configuration management, with a
tamper-evident audit trail.

## Install

- **rpm/deb**: `rpm -i dist/poolwarden-*.rpm` or `dpkg -i dist/poolwarden-*.deb`
  (installs `/usr/bin/poolwarden`, man pages, `/var/lib/poolwarden`).
  *RPM/deb installation is UNVERIFIED on the build machine (docs/04 I22).*
- **binary**: `go build -o poolwarden ./cmd/poolwarden`, copy to `/usr/bin`.

Prerequisite: root (or sudo), pgpool-II 4.4/4.5, pcp binaries, `iproute2`.

## Quick start

    sudo poolwarden init      # writes /etc/poolwarden.yaml
    poolwarden status         # one-shot view
    poolwarden                # interactive shell (pgpool[cluster@host]>)

## Commands

See `docs/commands/` (generated from the command tree) and `man poolwarden`.
Exit codes are in docs/01 §15: 0 ok · 1 usage · 2 connection · 3 preflight NO-GO ·
4 validation/drift · 5 apply rolled back · 6 apply rollback failed ·
10 warnings · 11 critical findings.

## Security model (01 §17)

Root-only file perms (0600 config/audit), PGPASSFILE/PCPPASSFILE (0600, never
on argv), SSH ExecAuthInfo-based operator identity, hash-chained audit log,
masked secrets in audit/commands, `sql` runs `BEGIN READ ONLY` + 30s timeout.

See `docs/05-operator-guide.md` for the full setup and operations manual.
