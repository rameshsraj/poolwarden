# poolwarden

100% CLI command center for one pgpool-II cluster, used by DBAs over SSH on
Linux. Specs live in docs/01–04; docs/04 is authoritative on conflicts.

## Commands

- `make build` — static binary into bin/ (CGO_ENABLED=0, -trimpath, version ldflags)
- `make build-linux` — linux/amd64 + linux/arm64 into dist/
- `make test` — `go test -race ./...`
- `make lint` — `go vet` + `gofmt -l` + staticcheck
- `make web` — build the console SPA (web/ → internal/console/dist, Node required)
- `make console` — build bin/poolwconsole (Go only; embeds whatever dist/ holds)

## Web console

`poolwconsole` (cmd/poolwconsole + internal/console) is an OPTIONAL gateway:
it embeds the React SPA (web/) and drives everything through the poolwarden
CLI — one shared `poolwarden monitor` NDJSON process for live data (SSE to
browsers), allowlisted argv subprocesses for reads and mutations. It never
executes SQL/PCP/SSH itself. CLI-only installs need neither Node nor the
console. Control commands gained `--plan` (preflight+token, no execution)
and `--progress` (NDJSON step events on stderr). `poolwarden probe` reports
per-endpoint connectivity; `poolwarden config install <file>` atomically
installs a complete poolwarden.yaml through the audited control flow.

## Layering rules

`cli` parses input and renders output; all logic lives in `service`, which
never prints and never reads stdin (confirmation goes through a `Confirmer`
interface). Every external system — pgpool SQL, backend SQL, pcp binaries,
SSH, log files, the filesystem and the clock — sits behind an interface so it
can be faked in tests. Writes are atomic: temp file in the same directory,
fsync, chmod/chown to the original owner and mode, rename, fsync the
directory. All user-facing errors are `*exitcode.Error` with an exit code and
a `next:` hint.

## Packaging (M11)

- `make build-linux` — static amd64/arm64 into dist/ · `make man docs` —
  regenerates man/ and docs/commands/ (committed artefacts; regeneration
  must be idempotent — `DisableAutoGenTag` is set, so re-run and expect an
  empty diff) · `make package` — needs nfpm (prints install hint otherwise).
- `config catalogue [--unverified]` is a hidden command listing the 4.4/4.5
  param catalogue and unconfirmed entries.
