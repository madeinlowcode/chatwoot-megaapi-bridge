# chatwoot-megaapi-bridge

Bidirectional bridge between [megaAPI](https://doc.mega-api.app.br/) (WhatsApp)
and [Chatwoot](https://www.chatwoot.com/), written in Go. Multi-tenant from day
one — one binary serves N (megaAPI instance ↔ Chatwoot inbox) pairs.

**Phase 1 (MVP) status:** text-only end-to-end. Media (image/audio/video/doc),
admin UI, Prometheus metrics, install wizard, and Caddy/TLS auto-provisioning
land in later phases — see `docs/12-roadmap.md`.

## Quickstart (dev)

Prerequisites:

- Docker 24+ and Docker Compose v2
- Go 1.23+ (only if you want to build/test locally outside Docker)
- `make` (optional)

```bash
# 1. Configure
cp .env.example .env
# Generate a master key (keep this safe — secrets in DB are unrecoverable without it):
echo "MASTER_KEY=$(openssl rand -base64 32)" >> .env

# 2. Bring up Postgres + Redis
docker compose -f deploy/docker-compose.yml --env-file .env up -d postgres redis

# 3. Run migrations
docker compose -f deploy/docker-compose.yml --env-file .env run --rm bridge-api /bridge migrate up

# 4. Bring up bridge-api + bridge-worker
docker compose -f deploy/docker-compose.yml --env-file .env up -d

# 5. Create your first tenant
docker compose -f deploy/docker-compose.yml --env-file .env exec bridge-api /bridge tenants create \
  --slug demo \
  --name "Demo Tenant" \
  --megaapi-host https://apibusiness1.megaapi.com.br \
  --megaapi-instance YOUR_INSTANCE_KEY \
  --megaapi-token YOUR_MEGAAPI_BEARER \
  --chatwoot-url https://chatwoot.example.com \
  --chatwoot-token YOUR_CHATWOOT_API_TOKEN \
  --chatwoot-account 1 \
  --chatwoot-inbox 42 \
  --chatwoot-hmac YOUR_INBOX_HMAC \
  --public-base-url https://your-bridge.example.com
```

The CLI prints the webhook URLs and the generated megaAPI Bearer token. Paste
those into your megaAPI/Chatwoot dashboards manually (auto-registration is
deferred to Phase 3).

## Endpoints

| Path | Auth | Purpose |
|------|------|---------|
| `POST /v1/wa/{slug}` | `Authorization: Bearer <generated>` | megaAPI inbound |
| `POST /v1/cw/{slug}` | `X-Chatwoot-Signature: <HMAC-SHA256>` | Chatwoot outbound |
| `GET /healthz` | none | liveness |
| `GET /readyz` | none | PG + Redis readiness |

## Architecture

See `docs/02-architecture.md`. TL;DR: bridge-api enqueues every webhook into
Redis (asynq) and ACKs in <10 ms; bridge-worker drains the queues and calls
the downstream APIs with retry + DLQ.

## Local development

```bash
# Requires Go 1.23+
make build       # build cmd/bridge-api and cmd/bridge-worker
make test        # go test ./... -race
make lint        # golangci-lint (requires golangci-lint installed)
make run-api     # start API in-process
make run-worker  # start worker in-process
```

Tests:
- Unit tests live next to the code (`*_test.go`) and use the stdlib + `httptest`.
- The end-to-end smoke script is at `scripts/e2e-test.sh`.

## Contributing

See `CONTRIBUTING.md`.

## License

See `LICENSE` (decision pending — defaults to MIT in placeholder; flag for
user input before publishing).
