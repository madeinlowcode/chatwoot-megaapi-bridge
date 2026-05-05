# Contributing

Thanks for considering a contribution!

## Workflow

1. **Pick an issue.** This project tracks work in `bd` (beads).
   ```bash
   bd ready          # list unblocked work
   bd show <id>      # read full context
   bd update <id> --claim
   ```

2. **Branch** off `master`:
   ```bash
   git checkout -b feat/short-description
   ```

3. **Code** following existing patterns. Add tests for any new logic
   (`*_test.go` next to the file). Avoid speculative abstractions.

4. **Validate** before pushing:
   ```bash
   make test
   make lint
   ```

5. **PR description** — summarize the change, link the bd issue, list any
   deviations from the plan if applicable.

## Conventions

- Go 1.23+
- Logs are JSON via `zerolog`. Required fields per `docs/11`: `service`,
  `request_id`, `tenant`, `kind`.
- All persisted secrets go through `internal/crypto.Keystore` (AES-GCM).
- HTTP webhooks must validate authentication **before** any other work.
- Workers must classify errors as retriable (5xx/timeout) vs not (4xx) so
  asynq makes the right decision.

## Reporting bugs

Open a `bd` issue:

```bash
bd create --type=bug --priority=2 --title="..." --description="..."
```
