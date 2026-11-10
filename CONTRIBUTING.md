# Contributing to poolwarden

Thanks for helping make pgpool-II operations saner. This file explains how to set up,
build, test, and get a change merged.

## Ground rules

`AGENTS.md` is the authoritative contributor guide — read it first. The short version:

- **Layering**: `internal/cli` parses input and renders output; all logic lives in
  `internal/service`, which never prints and never reads stdin (confirmation goes through
  a `Confirmer` interface). New behavior goes in `service`, wired from `cli`.
- **Fakes**: every external system — pgpool SQL, backend SQL, pcp binaries, SSH, log
  files, the filesystem, the clock — sits behind an interface so tests can fake it.
  Don't call real systems directly from service code.
- **Writes**: atomic. Temp file in the same directory, fsync, chmod/chown to the original
  owner and mode, rename, fsync the directory. Use the existing helpers.
- **Errors**: user-facing failures are `*exitcode.Error` with an exit code and a `next:`
  hint telling the operator what to try.
- **Truthfulness**: never synthesize, simulate or "smooth" live data. If a metric is
  unavailable, show `-` / `n/a` — a wrong number is worse than a missing one.

## Build & test

```bash
make build       # bin/poolwarden — static, CGO_ENABLED=0
make test        # go test -race ./...
make lint        # go vet + gofmt -l + staticcheck
make man docs    # regen man/ + docs/commands/ — must be idempotent (re-run → empty diff)
```

The console/frontend:

```bash
make web         # npm ci + vite build → web/dist → internal/console/dist (embedded)
make console     # bin/poolwconsole
```

Frontend lives in `web/` (React + TypeScript + Vite + Tailwind + ECharts + React Flow +
TanStack Query). `npm run build` in `web/` runs `tsc --noEmit` — keep it clean.

## Real-cluster testing

The project is validated against locally built Docker beds running real pgpool-II +
PostgreSQL streaming replication (not mocks). The bed definition is intentionally not
published (`dev/testbed/` is gitignored — it contains throwaway keys and scenario
results); build your own three-node compose setup or ask in an issue and we'll share the
recipe. Whatever you change, run it against a real cluster — failover, detach/attach,
service stop/start, config apply/rollback — and record what actually happened in the PR.

## Pull requests

- Keep diffs focused; one behavior per PR.
- Update `docs/commands/` (via `make man docs`), `docs/01–04` specs if behavior changed,
  and `CHANGELOG.md` under **Unreleased**.
- Add/adjust unit tests for service-layer logic (scripted fakes) and golden files for
  CLI output changes.
- Pre-existing test quirks: `internal/cli` golden tests are timezone-sensitive and
  `TestSampler` in `internal/history` is flaky — document if you hit them rather than
  masking.
- Use conventional commit style; explain **why**, not just what.

## Reporting bugs

Open an issue with: `poolwarden version`, pgpool-II version, PostgreSQL version, topology
(colocated/split, node count), the command you ran, and the output — including exit code
and the `next:` hint if present. For console issues include the browser console log and
the `/api/stream` snapshot JSON.

## Security

Don't post secrets, keys, or real cluster credentials in issues/PRs. Report security
issues privately to the maintainers before opening a public issue.
