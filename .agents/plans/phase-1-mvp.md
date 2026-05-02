# Phase 1 — MVP Funcional (Texto E2E)

## Goal

Implementar bridge bidirecional megaAPI ↔ Chatwoot em Go, com **1 tenant
provisionável via CLI**, suportando apenas mensagens de **texto**, com
garantias mínimas de confiabilidade (filas + retry + idempotência) e stack
docker-compose pronto para dev local.

**Critério de saída único:** mensagem de texto trafega ponta a ponta,
WhatsApp → Chatwoot e Chatwoot → WhatsApp, sem perda em condições normais.

## Scope

### IN SCOPE
- Estrutura do projeto Go (cmd/, internal/, sqlc, migrations)
- Schema PostgreSQL inicial (migration 0001) — ver `docs/05-data-model.md`
- Cliente HTTP megaAPI (sendText, configWebhook, instance status)
- Cliente HTTP Chatwoot (search/create contact, create conversation, post
  message com `message_type=incoming`)
- HTTP server `bridge-api` com chi:
  - `POST /v1/wa/{tenant_slug}` (auth Bearer token por tenant)
  - `POST /v1/cw/{tenant_slug}` (auth HMAC-SHA256)
  - `GET /healthz`, `GET /readyz`
- Worker `bridge-worker` consumindo asynq:
  - Fila `wa-to-cw` — postar inbound no Chatwoot
  - Fila `cw-to-wa` — enviar outbound via megaAPI
  - Retry exponencial 1s→10min (8 tentativas)
- CLI tenants: `bridge tenants create|list|show|delete` (com flags pra host
  megaAPI dinâmico, instance_key, token, URL Chatwoot, etc)
- AES-GCM encryption dos tokens em repouso (master key via env)
- HMAC validation no webhook Chatwoot (`subtle.ConstantTimeCompare`)
- Idempotência via tabela `idempotency_keys` (INSERT ON CONFLICT DO NOTHING)
- Logs zerolog estruturados (taxonomia `kind` em `docs/11`)
- Dockerfile multi-stage scratch (imagem final <30 MB)
- `docker-compose.yml` MVP: postgres + redis + bridge-api + bridge-worker
  (sem Chatwoot/Caddy nesta fase — se já existirem, integrar via env)
- `README.md` com instruções dev local

### OUT OF SCOPE (deferido)
- Mídia (imagem/áudio/vídeo/documento) → Phase 2
- DLQ admin endpoint → Phase 2
- Métricas Prometheus → Phase 2
- Backpressure (`/readyz` 503) → Phase 2
- Rate limiting por tenant → Phase 2
- UI admin htmx → Phase 3
- Wizard `install.sh` → Phase 4
- Caddy + Let's Encrypt → Phase 4
- Múltiplos admins → Phase 3

## Reference Documents (READ FIRST, in this order)

1. `docs/01-context-and-goals.md` — escopo geral, métricas
2. `docs/02-architecture.md` — fluxos inbound/outbound completos
3. `docs/03-tech-stack.md` — Go 1.23+, libs (chi, asynq, sqlc, pgx, zerolog,
   goose), estrutura de pastas, Dockerfile template
4. `docs/04-multi-tenancy.md` — modelo tenant, host megaAPI dinâmico,
   lookup+cache
5. `docs/05-data-model.md` — schema PostgreSQL completo (DDL pronto pra
   migration 0001)
6. `docs/06-api-and-protocols.md` — contratos megaAPI/Chatwoot, mapping
   payload bidirecional, endpoints bridge
7. `docs/07-reliability-and-performance.md` — filas asynq, retry,
   idempotência (apenas seções aplicáveis ao MVP)
8. `docs/10-security.md` — AES-GCM helper, HMAC validation, hardening
9. `docs/11-observability.md` — campos obrigatórios de log, healthz/readyz

## Implementation Tasks (mapeadas das bd issues)

### 1. Estrutura inicial do projeto Go
**bd:** chatwoot-megaapi-bridge-96l.1

- Criar `go.mod` com module `github.com/madeinlowcode/chatwoot-megaapi-bridge`
- Layout exato: `cmd/bridge-api/`, `cmd/bridge-worker/`, `internal/{config,
  tenant, megaapi, chatwoot, queue, handler, worker, crypto, db, repo,
  observability}/`, `migrations/`, `queries/`, `deploy/`
- `Makefile` com targets: `build`, `test`, `lint`, `migrate`, `sqlc`,
  `run-api`, `run-worker`
- `sqlc.yaml` configurando geração em `internal/repo/`
- `golangci.yml` com linters padrão

### 2. Schema PostgreSQL (migration 0001)
**bd:** chatwoot-megaapi-bridge-96l.2

- Aplicar DDL completo de `docs/05-data-model.md`:
  - `tenants`, `megaapi_configs`, `chatwoot_configs`, `contacts`, `messages`,
    `audit_events`, `idempotency_keys`, `admin_users`
  - Triggers `set_updated_at()`
  - Tipos enum `msg_direction`, `msg_status`
- Migrations via `goose` em `migrations/0001_init.sql`
- Auto-run no startup com flag `--migrate=auto` (default dev)

### 3. Cliente HTTP megaAPI
**bd:** chatwoot-megaapi-bridge-96l.3

- `internal/megaapi/client.go` com interface `Client`:
  - `SendText(ctx, host, instanceKey, token, to, text) error`
  - `ConfigWebhook(ctx, host, instanceKey, token, webhookURL) error`
  - `InstanceStatus(ctx, host, instanceKey, token) (*Status, error)`
- `http.Transport` global compartilhado (config de
  `docs/07#connection-pooling`)
- Validação base URL, escapamento de path
- Tipos request/response em `internal/megaapi/types.go`
- Tests com `httpmock`

### 4. Cliente HTTP Chatwoot
**bd:** chatwoot-megaapi-bridge-96l.4

- `internal/chatwoot/client.go`:
  - `SearchContact(ctx, cfg, phone) ([]Contact, error)`
  - `CreateContact(ctx, cfg, payload) (*Contact, error)`
  - `CreateConversation(ctx, cfg, payload) (*Conversation, error)`
  - `CreateMessage(ctx, cfg, conversationID, payload) (*Message, error)`
- Mesma `http.Transport` global (reuso pool)
- Header `api_access_token`
- Tipos em `internal/chatwoot/types.go`
- Tests com `httpmock`

### 5. HTTP server + rotas webhook
**bd:** chatwoot-megaapi-bridge-96l.5

- `cmd/bridge-api/main.go` — bootstrap (config, db, redis, asynq client,
  router, server graceful shutdown)
- `internal/handler/`:
  - `health.go` — `/healthz` (sempre 200), `/readyz` (ping PG + Redis)
  - `webhook_megaapi.go` — `POST /v1/wa/{slug}`:
    1. Resolve tenant via `tenant.Lookup(slug)` (cache)
    2. Valida Bearer token (constant-time compare)
    3. Hash external_id → INSERT idempotency_keys → se duplicate, ACK
    4. Persiste mensagem em `messages` com status=queued
    5. Enfileira job asynq `WAtoCWJob`
    6. ACK 200 (corpo vazio)
  - `webhook_chatwoot.go` — `POST /v1/cw/{slug}`:
    1. Lê body raw, valida HMAC-SHA256 com `hmac_secret` do tenant
    2. Filtra evento (apenas `message_created` com `message_type=outgoing`,
       `private=false`)
    3. Idem etapas 3–6 acima, mas fila `cw-to-wa`
- Middleware: requestID, logger, recoverer, contentType JSON

### 6. Asynq queue + worker
**bd:** chatwoot-megaapi-bridge-96l.6

- `internal/queue/types.go` — payloads: `WAtoCWPayload`, `CWtoWAPayload`
  com `tenant_id`, `message_id`, `external_id`, `payload jsonb`
- `internal/queue/client.go` — wrapper asynq.Client com helpers
  `EnqueueWAtoCW`, `EnqueueCWtoWA`
- `cmd/bridge-worker/main.go` — bootstrap server asynq, registra handlers
- `internal/worker/wa_to_cw.go`:
  1. Carrega `messages` row por id
  2. Carrega tenant config (chatwoot)
  3. Resolve/cria contato Chatwoot
  4. Resolve/cria conversation (mantém em `contacts` row)
  5. POST mensagem
  6. UPDATE `messages` status=delivered, delivered_at
  7. Em erro: classifica retriable (5xx/timeout) → retorna erro pra asynq
     reagendar; non-retriable (4xx) → marca failed, sem retry
- `internal/worker/cw_to_wa.go`:
  1. Carrega messages + tenant config (megaapi)
  2. Decifra token AES-GCM
  3. Mapeia payload Chatwoot → request megaAPI sendText
  4. POST `{host}/rest/sendMessage/{instance_key}/text`
  5. UPDATE status
- Configuração asynq: 50 concorrência cada fila, retention 7d, archive na
  DLQ após 8 falhas

### 7. CLI tenants CRUD
**bd:** chatwoot-megaapi-bridge-96l.7

- `cmd/bridge-api/main.go` aceita subcomandos via spf13/cobra (ou flag
  parser próprio):
  - `bridge serve` (default — sobe HTTP server)
  - `bridge migrate up|down|status`
  - `bridge tenants create --slug <s> --name <n> --megaapi-host <url> 
    --megaapi-instance <key> --megaapi-token <t> --chatwoot-url <url> 
    --chatwoot-token <t> --chatwoot-account <id> --chatwoot-inbox <id>`
    - Bridge gera HMAC secret aleatório
    - Bridge gera bearer token aleatório pra webhook megaAPI
    - Persiste com tokens cifrados
    - Imprime URL webhook pra registrar manualmente na megaAPI
  - `bridge tenants list` — tabela slug, host, criado em
  - `bridge tenants show <slug>` — detalhes (sem segredos)
  - `bridge tenants delete <slug>` — soft delete (active=false)
- Auto-registro de webhooks fica fora do MVP (Phase 3)

### 8. AES-GCM encryption
**bd:** chatwoot-megaapi-bridge-96l.8

- `internal/crypto/aesgcm.go`:
  - `Encrypt(plaintext []byte, key []byte) ([]byte, error)`
  - `Decrypt(ciphertext []byte, key []byte) ([]byte, error)`
  - Nonce 96 bits aleatório, prepended ao ciphertext
  - `kid` (key id) — versão de chave, hardcoded `1` no MVP
- `internal/crypto/keystore.go` — carrega master key de `MASTER_KEY` env
  (base64 32 bytes); falha startup se ausente
- Helper `EncryptToken(plaintext) ([]byte, kid int16)` usado no repo de
  configs

### 9. HMAC validation Chatwoot
**bd:** chatwoot-megaapi-bridge-96l.9

- `internal/crypto/hmac.go`:
  - `VerifyHMAC(body []byte, signature, secret string) bool`
  - Usa `hmac/sha256` + `hex.EncodeToString` + `subtle.ConstantTimeCompare`
- Aplicado no handler `webhook_chatwoot.go` antes de qualquer parsing
- 401 quando inválido

### 10. Idempotência
**bd:** chatwoot-megaapi-bridge-96l.10

- `internal/repo/idempotency.go` — gerada via sqlc:
  - `InsertIdempotencyKey(tenant, scope, hash) (inserted bool, err error)`
  - SQL: `INSERT INTO idempotency_keys ... ON CONFLICT DO NOTHING RETURNING true`
  - `inserted=false` → mensagem duplicada
- Hash SHA-256 do `external_id` (megaAPI `key.id` ou Chatwoot `id`)
- Cron diário (asynq scheduler) limpa `created_at < now() - interval '7 days'`

### 11. Logs zerolog
**bd:** chatwoot-megaapi-bridge-96l.11

- `internal/observability/logger.go`:
  - `Init(level, service)` — global zerolog + JSON output
  - `FromContext(ctx)` — logger contextual com `request_id`, `tenant`
  - Helper `Audit(ctx, kind, ok bool, fields ...)` grava em
    `audit_events` async (fire-and-forget via worker queue dedicated)
- Middleware HTTP injeta `request_id` no contexto
- Taxonomia `kind` conforme `docs/11`

### 12. Dockerfile multi-stage scratch
**bd:** chatwoot-megaapi-bridge-96l.12

- `Dockerfile` exato de `docs/03#dockerfile-planejado`
- Builds simultâneos `bridge` + `bridge-worker`
- Final stage `FROM scratch`, `USER 65534:65534`
- `COPY` de `ca-certificates.crt`
- `EXPOSE 8080`
- Imagem final medida com `docker images` deve ser <30 MB

### 13. docker-compose.yml MVP
**bd:** chatwoot-megaapi-bridge-96l.13

- `deploy/docker-compose.yml`:
  - `postgres:15-alpine` (volume nomeado, healthcheck)
  - `redis:7-alpine` AOF habilitado, requirepass
  - `bridge-api` — build local OR pull `ghcr.io/...`
  - `bridge-worker`
- `.env.example` com vars necessárias
- Script `init.sql` cria DB `bridge`
- Comandos prontos no README:
  ```bash
  docker compose up -d postgres redis
  docker compose run --rm bridge-api /bridge migrate up
  docker compose up -d
  ```
- **Chatwoot fica fora deste compose** — assume rodando externamente. Se
  conveniente, incluir como compose extra `docker-compose.chatwoot.yml`.

### 14. README + dev docs
**bd:** chatwoot-megaapi-bridge-96l.14

- `README.md`:
  - Pitch curto (1 parágrafo)
  - Pré-requisitos (Docker 24+, Go 1.23+ se for dev)
  - Quickstart dev (clone, .env, compose up, criar tenant via CLI)
  - Rodar tests
  - Arquitetura (link `docs/02`)
  - Como contribuir (branch, PR, bd workflow)
- `CONTRIBUTING.md` curto
- `LICENSE` — sugerir MIT (ou AGPL se preferir copyleft — abrir como
  decision pra usuário decidir)

### 15. Critério MVP — validação E2E
**bd:** chatwoot-megaapi-bridge-96l.15 (P1)

Script `scripts/e2e-test.sh`:
1. `docker compose up -d`
2. `bridge tenants create --slug demo ...`
3. Inicia mock megaAPI (servidor HTTP local que recebe `sendText`)
4. POST simulado em `http://localhost:8080/v1/wa/demo` com payload texto
5. Verifica via API Chatwoot (manual ou mock) que mensagem foi criada
6. POST simulado em `/v1/cw/demo` com HMAC válido
7. Verifica que mock megaAPI recebeu chamada `sendText`
8. Replay mesmo payload → idempotência (mensagem não duplica)
9. POST com HMAC inválido → 401

## Acceptance Criteria

- [ ] `docker compose up -d` sobe stack sem erros, todos `healthy`
- [ ] `bridge migrate up` aplica schema com sucesso
- [ ] `bridge tenants create` aceita todas as flags e persiste tokens
      cifrados (verificável via `psql` que `bearer_token_enc` é binário)
- [ ] `POST /v1/wa/{slug}` com payload texto válido → 200 em <50 ms +
      mensagem aparece na fila asynq (verificável via `redis-cli`)
- [ ] Worker processa job e cria mensagem real no Chatwoot (mock OU real)
- [ ] `POST /v1/cw/{slug}` com HMAC inválido → 401
- [ ] `POST /v1/cw/{slug}` com HMAC válido → 200 + worker chama megaAPI
      sendText (verificável via mock)
- [ ] Replay do mesmo `external_id` não cria mensagem duplicada (status
      `duplicate`)
- [ ] Falha 5xx do Chatwoot → job retentado pelo asynq (verificável
      observando `attempts` em `messages`)
- [ ] `go test ./...` passa
- [ ] `docker images` mostra `bridge:latest` <30 MB
- [ ] Logs JSON estruturados com campos `service`, `request_id`, `tenant`,
      `kind`

## Verification (post-implementation)

### Auto
```bash
make test                    # unit tests
make lint                    # golangci-lint
docker compose up -d
./scripts/e2e-test.sh        # cenários acima
```

### Manual (último passo)
1. Subir Chatwoot real (compose extra ou instalação existente)
2. Criar API Channel no Chatwoot, anotar inbox_id
3. `bridge tenants create --slug demo ...` com host megaAPI real
4. Manualmente registrar webhook na megaAPI apontando para
   `https://<tunnel>/v1/wa/demo` (ngrok/cloudflared)
5. Mandar WhatsApp pra número da instância → ver chegando no Chatwoot
6. Responder no Chatwoot → ver chegando no WhatsApp
7. Gravar screencast 1 min do fluxo

## Constraints

- Nenhum código de F2/F3 antecipado (não criar UI admin, não implementar
  mídia, não adicionar Prometheus métricas).
- Não modificar `/docs` exceto adicionar pequenos esclarecimentos quando
  descobrir incongruências.
- Manter cobertura testes >70% nas funções de `internal/crypto`,
  `internal/handler`, `internal/worker`.
- Pinar versões em `go.mod` (não usar `latest` em imagens Docker do compose).
- Bridge deve subir mesmo sem nenhum tenant cadastrado (estado vazio
  válido).

## Out-of-band coordination

- Após PR criado pelo archon, abrir e revisar antes de merge.
- Atualizar bd: fechar `chatwoot-megaapi-bridge-96l.*` filhos conforme
  acceptance critérios validados.
- Não fechar epic `chatwoot-megaapi-bridge-96l` até o item .15 (validação
  E2E manual) estar feito.
