# chatwoot-megaapi-bridge

Open-source HTTP bridge between **megaAPI** (WhatsApp) and **Chatwoot**, multi-tenant,
flat-first Go implementation. One binary, three tables, in-process channels —
no Redis, no Worker pool service, no microservices.

## Quickstart (5 commands)

```bash
cp .env.example .env
echo "MASTER_KEY=$(openssl rand -base64 32)" >> .env
docker compose up -d --build
docker compose exec bridge /bridge migrate
docker compose exec bridge /bridge tenant add \
  --slug demo \
  --megaapi-host https://apibusiness7.megaapi.com.br \
  --megaapi-instance YOUR_INSTANCE \
  --megaapi-token YOUR_MEGA_TOKEN \
  --chatwoot-url https://your-chatwoot.example.com \
  --chatwoot-token YOUR_CW_TOKEN \
  --chatwoot-account 1 \
  --chatwoot-inbox 5
```

The `tenant add` command prints a **Webhook Bearer** (configure on megaAPI) and
an **HMAC Secret** (configure on Chatwoot webhook integration).

## Endpoints

| Method | Path                | Auth                      |
|--------|---------------------|---------------------------|
| `POST` | `/v1/wa/{slug}`     | `Authorization: Bearer …` |
| `POST` | `/v1/cw/{slug}`     | `X-Chatwoot-Signature: …` |
| `GET`  | `/healthz`          | _none_                    |
| `GET`  | `/readyz`           | _none_                    |

## Architecture

```
megaAPI ──▶ POST /v1/wa/{slug}  ─▶ DB.InsertMessage(pending) ─▶ inboxChan ──▶ workerPool ─▶ Chatwoot REST
Chatwoot ─▶ POST /v1/cw/{slug}  ─▶ DB.InsertMessage(pending) ─▶ outboxChan ─▶ workerPool ─▶ megaAPI sendMessage
```

- **1 package** `internal/bridge/` — server, storage, crypto, bridge core
- **1 binary** `bridge` — subcommands `serve`, `migrate`, `tenant add`
- **3 tables** `tenants`, `contacts`, `messages` (idempotency via `UNIQUE(tenant_id, direction, external_id)`)
- **AES-256-GCM** at-rest for all tokens, **HMAC-SHA256** for inbound signatures

## Development

```bash
make test          # unit tests
make integration   # testcontainers (Docker required)
make lint          # go vet + golangci-lint
make build         # static binary
```

Go 1.25+ required (the `testcontainers-go` indirect dep sets the floor).

## License

MIT — see [LICENSE](./LICENSE).
