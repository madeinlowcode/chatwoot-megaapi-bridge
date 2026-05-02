# 11 — Observabilidade

## Os três pilares

1. **Logs** — eventos discretos, contexto rico.
2. **Métricas** — séries temporais agregadas.
3. **Traces** — caminho de uma requisição através do sistema (opcional MVP+).

## Logs

### Stack
- Lib: `zerolog` (estruturado, JSON nativo, zero-alloc).
- Saída: stdout (Docker captura).
- Coleta opcional: Loki/Promtail ou ELK no compose extra.

### Formato (exemplo)
```json
{
  "level": "info",
  "ts": "2026-05-01T10:46:23.123Z",
  "service": "bridge-api",
  "tenant": "cliente-x",
  "request_id": "req_abc123",
  "kind": "webhook.inbound.received",
  "external_id": "WA_MSG_xyz",
  "duration_ms": 4,
  "ok": true
}
```

### Campos obrigatórios
- `ts`, `level`, `service`, `request_id`.
- `tenant` quando aplicável.
- `kind` (taxonomia de eventos: ver abaixo).

### Taxonomia `kind`

| Prefixo | Exemplos |
|---------|----------|
| `webhook.inbound.*` | `received`, `accepted`, `rejected.auth`, `duplicate` |
| `webhook.outbound.*` | mesmo padrão |
| `worker.send.*` | `started`, `succeeded`, `retry`, `failed`, `dlq` |
| `tenant.*` | `created`, `updated`, `deleted`, `disabled` |
| `auth.*` | `login.success`, `login.failed`, `lockout` |
| `dependency.*` | `pg.down`, `redis.down`, `pg.up` |

### Níveis
- `debug` — só em dev.
- `info` — eventos de negócio.
- `warn` — degradação (retry, rate limit).
- `error` — falha que precisa atenção.
- `fatal` — crash. Apenas em startup impossível.

### Sampling
- 100% de erros e warns.
- 10% de info em alta carga (configurável).

## Métricas

### Stack
- Lib: `prometheus/client_golang`.
- Endpoint: `/metrics`.
- Restrito a IPs internos (Caddy regra) ou auth básico.

### Métricas core

```
# HTTP
bridge_http_requests_total{path,method,status}        counter
bridge_http_request_duration_seconds{path}             histogram
bridge_http_in_flight{path}                            gauge

# Webhook
bridge_webhook_received_total{tenant,direction}        counter
bridge_webhook_duplicate_total{tenant,direction}       counter
bridge_webhook_rejected_total{tenant,reason}           counter

# Worker
bridge_jobs_processed_total{queue,status}              counter
bridge_jobs_duration_seconds{queue}                    histogram
bridge_jobs_retries_total{queue}                       counter
bridge_jobs_dlq_total{queue,reason}                    counter
bridge_queue_depth{queue}                              gauge

# Outbound
bridge_outbound_calls_total{target,status}             counter   # target = megaapi|chatwoot
bridge_outbound_duration_seconds{target}               histogram

# DB
bridge_db_pool_open_connections                        gauge
bridge_db_pool_idle_connections                        gauge
bridge_db_query_duration_seconds{op}                   histogram

# Tenant health
bridge_tenant_last_inbound_seconds_ago{tenant}         gauge
bridge_tenant_last_outbound_seconds_ago{tenant}        gauge
bridge_tenant_active                                   gauge
```

### Dashboards (Grafana)
Compose extra `docker-compose.observability.yml`:
- Prometheus + Grafana + Loki opcional.
- Dashboards JSON pré-construídos:
  1. **Overview** — RPS, p99, error rate, queue depth.
  2. **Por tenant** — drill-down, último visto, erros, taxa.
  3. **DLQ & Erros** — top falhas, classificação.
  4. **Infra** — DB pool, Redis, mem/CPU.

### Alertas (Prometheus AlertManager)

```yaml
groups:
- name: bridge.rules
  rules:
  - alert: HighErrorRate
    expr: rate(bridge_jobs_processed_total{status="failed"}[5m]) > 0.05
    for: 10m
    annotations:
      summary: "Bridge: taxa de falha > 5% por 10 min"
      
  - alert: QueueDepthHigh
    expr: bridge_queue_depth > 1000
    for: 5m
      
  - alert: DLQGrowing
    expr: increase(bridge_jobs_dlq_total[1h]) > 50
    
  - alert: TenantStalled
    expr: bridge_tenant_last_inbound_seconds_ago > 3600
    annotations:
      summary: "Tenant {{$labels.tenant}} sem mensagens há >1h"
      
  - alert: DependencyDown
    expr: up{job="bridge"} == 0
```

Saídas: webhook Slack/Discord/email configurável pelo operador na UI.

## Tracing (v1.x, não MVP)

- OpenTelemetry SDK para Go.
- Exportador OTLP → Tempo/Jaeger.
- Spans:
  - `webhook.handle`
  - `tenant.lookup`
  - `queue.enqueue`
  - `worker.process`
  - `chatwoot.api_call` / `megaapi.api_call`
  - `db.query`
- `trace_id` injetado em logs (`request_id` correlato).

## Healthchecks

### Liveness `/healthz`
Retorna 200 se processo respondendo. Não checa dependências.

### Readiness `/readyz`
```go
checks := []Check{
  pingPostgres(),
  pingRedis(),
}
```
Retorna 200 só se todas OK. Caddy/Docker tira tráfego do nó se falhar.

### Tenant health `/admin/api/tenants/{slug}/health`
JSON com:
```json
{
  "tenant": "cliente-x",
  "active": true,
  "megaapi": { "ok": true, "latency_ms": 87 },
  "chatwoot": { "ok": true, "latency_ms": 145 },
  "queue_depth": 0,
  "last_inbound": "2026-05-01T10:43:21Z",
  "last_outbound": "2026-05-01T10:45:09Z",
  "errors_24h": 2
}
```

## Diagnóstico self-service

Página `/admin/tenants/{slug}/diagnose` (já descrita em [09](./09-admin-ui.md)).

Comando CLI equivalente:
```bash
docker compose exec bridge-api /bridge diagnose cliente-x
```

Sai relatório em texto colorido. Útil pra suporte remoto.

## Notificações de alerta para operador

- Webhook configurável em `/admin/settings/alerts`.
- Tipos:
  - DLQ cresceu além de threshold
  - Tenant parou de receber mensagens
  - Dependência caiu
  - Falha de auth massiva (possível ataque)
- Templates Markdown (Slack/Discord) e text (email).

## Profiling em produção

`/debug/pprof` habilitado atrás de auth admin. Para casos onde precisamos
diagnosticar latência/memória sem reproduzir local. Desabilitado por
default; toggle em settings.
