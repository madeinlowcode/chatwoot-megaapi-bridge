# 03 — Stack Tecnológico

## Linguagem: Go

### Por quê Go

| Critério | Go | Node.js | Rust | Elixir |
|----------|-----|---------|------|--------|
| Concorrência alta | ✅ Goroutines | ⚠️ event loop, CPU-bound sofre | ✅ async/await | ✅ BEAM |
| Tempo dev | ✅ Bom | ✅ Ótimo | ❌ Lento | ⚠️ Médio |
| Distribuição | ✅ 1 binário | ❌ node + node_modules | ✅ 1 binário | ❌ Erlang VM |
| Ecossistema HTTP/queue | ✅ Maduro | ✅ Maduro | ⚠️ Crescendo | ⚠️ Médio |
| Memória previsível | ✅ | ⚠️ V8 GC | ✅ | ✅ |
| Hire/manutenção | ✅ Comum | ✅ Comum | ❌ Raro | ❌ Raro |

**Escolha: Go.** Combinação ótima de throughput, ops simples e curva de
manutenção baixa. Rust é overkill (não somos CPU-bound), Elixir seria ideal
mas pool de devs menor.

### Versão Go

- **Go 1.23+** (latest stable). Tirar proveito de `errors.Join`, melhorias de
  GC, slog estável.

## Bibliotecas principais

| Função | Biblioteca | Justificativa |
|--------|------------|---------------|
| HTTP router | `chi` | Leve, idiomático, middleware modular |
| HTTP client outbound | `net/http` stdlib + `Transport` custom | Connection pool fino, HTTP/2, retry no worker |
| Driver Postgres | `pgx/v5` | Performance superior, prepared statements nativos |
| Query builder/codegen | `sqlc` | SQL puro, type-safe, sem ORM mágico |
| Migrations | `goose` | Simples, embed migrations no binário |
| Fila distribuída | `asynq` | Redis, retry, scheduling, dashboard pronto, maduro |
| Logger | `zerolog` | Estruturado, zero alloc, JSON nativo |
| Métricas | `prometheus/client_golang` | Padrão de fato |
| Tracing | `otel-go` | OpenTelemetry, opcional |
| Config | `koanf` | Camadas (env > arquivo > defaults), simples |
| Validation | `go-playground/validator` | Tags struct |
| Crypto | `crypto/aes`, `crypto/hmac` stdlib | AES-GCM tokens, HMAC webhook |
| Templating UI | `html/template` stdlib + `htmx` | Sem build step JS |
| Embed assets | `embed` stdlib | Bundle UI no binário |
| Tests | `testify`, `testcontainers-go` | Padrão; PG/Redis reais nos testes |
| HTTP mock testes | `httpmock` | Mock megaAPI/Chatwoot em unit tests |
| Lint | `golangci-lint` | Combo de linters padrão |

### Anti-escolhas (e por quê)

- ❌ **Gin/Echo/Fiber** — chi é suficiente, menos opinativo.
- ❌ **GORM** — ORM esconde queries lentas. sqlc gera código a partir de SQL.
- ❌ **rabbitmq/kafka** — overhead operacional grande para o caso. Redis +
  asynq cobre 100% dos requisitos por bem mais barato.
- ❌ **React/Vue para admin** — htmx + alpine resolve formulários CRUD com
  zero pipeline de build.

## Estrutura de pastas (planejada)

```
.
├── cmd/
│   ├── bridge-api/
│   │   └── main.go
│   └── bridge-worker/
│       └── main.go
├── internal/
│   ├── config/             # carga de config + validação
│   ├── tenant/             # repo, cache, lookup
│   ├── megaapi/            # client HTTP megaAPI
│   ├── chatwoot/           # client HTTP Chatwoot
│   ├── queue/              # wrappers asynq, payloads de job
│   ├── handler/            # HTTP handlers (webhooks, admin)
│   ├── worker/             # consumers asynq
│   ├── crypto/             # AES-GCM, HMAC helpers
│   ├── db/                 # pgx pool, migrations
│   ├── repo/               # sqlc generated + interfaces
│   ├── observability/      # logging, metrics, tracing
│   └── ui/                 # templates html, assets, handlers UI
├── migrations/
│   └── 0001_init.sql
├── queries/                # SQL pra sqlc
├── deploy/
│   ├── docker-compose.yml
│   ├── Caddyfile
│   └── install.sh
├── docs/                   # documentação
├── go.mod
├── sqlc.yaml
├── Dockerfile              # multi-stage, scratch final
└── Makefile
```

## Dockerfile (planejado)

```dockerfile
# build
FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/bridge ./cmd/bridge-api && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/bridge-worker ./cmd/bridge-worker

# runtime
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/bridge /bridge
COPY --from=build /out/bridge-worker /bridge-worker
EXPOSE 8080
ENTRYPOINT ["/bridge"]
```

Imagem final ~25 MB.

## Versionamento

- Tags semver: `v0.1.0`, `v0.2.0`...
- Imagens publicadas: `ghcr.io/<org>/bridge:v0.1.0` e `:latest` (mas usuário
  fixa versão).
- Renovate bot abre PR de updates de dependências semanal.

## Observabilidade incluída de fábrica

- `/healthz` (liveness) e `/readyz` (readiness com checks PG+Redis).
- `/metrics` Prometheus.
- Logs JSON estruturados em stdout (Docker captura).
- Painel Grafana opcional (compose extra `docker-compose.observability.yml`).
