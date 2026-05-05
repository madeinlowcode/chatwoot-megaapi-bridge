# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->


## Build & Test

This is a Go 1.23 project. The `Makefile` is the canonical entrypoint.

```bash
make tidy      # go mod tidy
make test      # go test ./...
make lint      # golangci-lint run
make build     # go build -o bin/bridge-{api,worker} ./cmd/...
make e2e       # bash scripts/e2e-test.sh against a live local stack
```

Run all four (`tidy`, `test`, `lint`, `build`) before opening a PR — see
`CONTRIBUTING.md` for details.

## Architecture Overview

Two-binary bridge between megaAPI (WhatsApp) and Chatwoot:

- `cmd/bridge-api`   — HTTP edge: webhook handlers (`/v1/wa/{slug}`,
  `/v1/cw/{slug}`), tenant CLI, health endpoints. Persists incoming events to
  Postgres and enqueues an asynq job.
- `cmd/bridge-worker` — asynq worker: consumes the two queues
  (`QueueWAtoCW`, `QueueCWtoWA`), calls the corresponding upstream
  (`internal/chatwoot`, `internal/megaapi`), writes terminal status back to
  Postgres.

Shared internals live under `internal/`:
- `tenant`  — slug → fully-decrypted runtime config, with TTL cache.
- `crypto`  — AES-256-GCM keystore (kid-keyed envelope) + HMAC verify.
- `repo`    — hand-coded pgx queries; mirrors `queries/*.sql` (sqlc-ready).
- `queue`   — asynq enqueue helpers + payload types.
- `httpx`   — shared `*http.Client` with production timeouts.
- `observability` — zerolog with `request_id` and `tenant` context stamps.

See `docs/02-architecture.md` for the full reference.

## Conventions & Patterns

- **Beads first**: every change starts from a `bd` issue (see top of file).
- **Style**: `gofmt`, `goimports`, `golangci.yml` lint config — `make lint`
  before pushing.
- **Errors**: external-API errors implement `Retriable()`; workers route via
  `classify(err, kind)`. Never silently drop status updates — log structured
  `kind` if returning the error would mis-route asynq.
- **Logging**: every log line carries a structured `kind` field (e.g.
  `webhook.inbound.accepted`) for slicing in observability dashboards.
- **Secrets**: never log decrypted tokens. The `tenant.Resolved` struct
  marks decrypted fields with `// decrypted; never log`.
- **Schema/migrations**: `migrations/0001_init.sql` is authoritative;
  `internal/db/migrations/` carries a duplicate `go:embed` copy that must
  stay byte-identical (`diff -q` before commit).
- **Docs**: end-user-facing docs live under `docs/`; deviations from the
  plan are logged in `docs/implementation.md` rather than rewriting plan
  files. See `CONTRIBUTING.md` for the full contributor checklist.
