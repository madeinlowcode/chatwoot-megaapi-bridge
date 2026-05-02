# 07 — Confiabilidade e Performance

## Princípios

1. **Receber é sagrado.** Webhook nunca falha por motivo interno. ACK 200
   antes de qualquer trabalho real.
2. **Processar é responsabilidade da fila.** I/O downstream sempre via
   worker, com retry automático.
3. **Idempotência em tudo.** Toda operação assume que pode ser executada N
   vezes.
4. **Falhar visível.** DLQ + alerta antes de silenciar.

## Filas (asynq)

| Fila | Propósito | Concorrência | Prioridade |
|------|-----------|--------------|------------|
| `wa-to-cw` | Inbound — postar no Chatwoot | 50 workers | alta |
| `cw-to-wa` | Outbound — enviar via megaAPI | 50 workers | alta |
| `housekeeping` | Cleanup, retenção, métricas | 5 workers | baixa |
| `dlq-retry` | Reprocessar DLQ manual | 5 workers | sob demanda |

Asynq aceita prioridades por fila. Configuração:

```go
asynq.Config{
  Concurrency: 110,
  Queues: map[string]int{
    "wa-to-cw":    5,
    "cw-to-wa":    5,
    "housekeeping": 1,
    "dlq-retry":    1,
  },
}
```

## Política de retry

Backoff exponencial com jitter:

| Tentativa | Espera (base) |
|-----------|----------------|
| 1 → 2 | 1 s |
| 2 → 3 | 2 s |
| 3 → 4 | 4 s |
| 4 → 5 | 8 s |
| 5 → 6 | 30 s |
| 6 → 7 | 2 min |
| 7 → 8 | 10 min |
| 8 → DLQ | — |

Total: ~13 min de tentativas antes de DLQ. Cobertura suficiente para 99% das
falhas transitórias (deploy de Chatwoot, DNS flakiness, etc).

Erros classificados:
- **Retriable:** 5xx, timeouts, connection refused, 429.
- **Não-retriable:** 400, 401, 404 → vai direto pra DLQ com erro classificado.

## Idempotência

### Inbound
- Hash SHA-256 do `external_id` (megaAPI `key.id`).
- Tabela `idempotency_keys` com PK composta — INSERT falha se duplicado.
- Estratégia: `INSERT ON CONFLICT DO NOTHING`. Se 0 linhas afetadas, marca
  msg como `duplicate` e ACK sem enfileirar.

### Outbound
- Hash do Chatwoot `message.id`.
- Mesma lógica.

### Idempotência no envio para megaAPI
- megaAPI **não suporta idempotency-key nativo**. Mitigação: bridge marca
  `status=sending` antes de chamar; em retry, antes de re-enviar consulta
  Chatwoot pra ver se mensagem já foi marcada como entregue (cross-check).

## Rate limiting

Por tenant, token-bucket em Redis (lib `redis_rate` ou implementação custom):

- **Outbound (CW→WA):** default 20 msg/s por tenant. Configurável.
- **Inbound (WA→CW):** sem limite no bridge (megaAPI controla). Bridge mede
  e alerta se >100 msg/s sustentado por tenant (pode indicar spam).

Em rate-limit hit:
- Worker reagenda job com delay = `1 / rate_limit_rps`.
- Não conta como retry para fim de DLQ.

## Backpressure

- Threshold de queue depth: 1000 jobs/fila.
- Acima: `/readyz` retorna 503 (load balancer para de mandar tráfego para
  réplica).
- Métrica `bridge_queue_depth{queue}` exposta — alarme em Grafana se >5000.

## Connection pooling

### HTTP client outbound
```go
http.Transport{
  MaxIdleConns:        500,
  MaxIdleConnsPerHost: 50,
  IdleConnTimeout:     90 * time.Second,
  DialContext: (&net.Dialer{
    Timeout:   5 * time.Second,
    KeepAlive: 30 * time.Second,
  }).DialContext,
  TLSHandshakeTimeout:   5 * time.Second,
  ResponseHeaderTimeout: 10 * time.Second,
  ExpectContinueTimeout: 1 * time.Second,
  ForceAttemptHTTP2:     true,
}
```

Um cliente HTTP global compartilhado por todos os workers. `Host` no request
muda por tenant; pool reutiliza conexões por host (ex: pool dedicado
implícito por `apibusiness7.megaapi.com.br` vs `apibusiness1.megaapi.com.br`).

### PostgreSQL pool (pgx)
- `MaxConns`: 50 por instância
- `MinConns`: 5
- `MaxConnLifetime`: 1h
- `MaxConnIdleTime`: 30min

### Redis pool
- `PoolSize`: 100
- `MinIdleConns`: 10

## Targets de performance

Hardware referência: 2 vCPU, 4 GB RAM, SSD.

| Métrica | Target | Observado em load test |
|---------|--------|------------------------|
| Inbound webhook ACK p50 | <2 ms | TBD |
| Inbound webhook ACK p99 | <10 ms | TBD |
| End-to-end inbound (msg até Chatwoot) p99 (vazio) | <200 ms | TBD |
| Throughput sustentado | 1000 msg/s | TBD |
| Pico (5 min) | 5000 msg/s | TBD |
| RAM por tenant ativo | <10 MB | TBD |
| RAM total bridge-api | <512 MB com 500 tenants | TBD |

(Valores TBD serão preenchidos após load tests com `vegeta` ou `k6`.)

## DLQ — Dead Letter Queue

- Asynq nativo move jobs falhados para `archive`.
- Bridge expõe `/admin/dlq` listando, com payload e último erro.
- Ações disponíveis:
  - **Retry** — re-enfileira na fila original.
  - **Inspect** — vê payload completo + tentativas.
  - **Discard** — descarta com confirmação.
- Alerta automático: se DLQ cresce >50 itens em 1h, dispara webhook de
  notificação (Slack/Discord configurado por operador).

## Recuperação de falhas

| Falha | Comportamento |
|-------|----------------|
| Postgres cai | `/readyz` 503; webhooks recusados (megaAPI re-tenta). Volta sozinho. |
| Redis cai | Mesma coisa. Asynq não funciona sem Redis. |
| Chatwoot cai | Workers retentam. Mensagens acumulam em `wa-to-cw`. Quando volta, dreno automático. |
| megaAPI cai | Mesma lógica para `cw-to-wa`. |
| Bridge cai | Containers reiniciam (Docker `restart: unless-stopped`). Filas no Redis intactas. |
| Bridge perde host | Restore Postgres + Redis backup → estado recuperado. |

## Health checks

```
GET /healthz   → 200 sempre
GET /readyz    → 200 se PG+Redis OK; 503 caso contrário
GET /admin/dashboard/healths → JSON com status de cada tenant
```

Por tenant:
- Última inbound vista (timestamp)
- Última outbound enviada
- Erros nas últimas 24h
- Profundidade da fila

Se `last_inbound > 1h` e existia tráfego antes → alerta "fluxo parou".

## Testes de carga

Plano:
- `k6` com cenário de 1000 RPS de inbound webhooks por 5 min.
- Validar ACK p99 <10 ms.
- Validar zero perda (count enviado = count entregue Chatwoot mock).
- Repetir com Chatwoot mock retornando 500 a 30% das chamadas → validar
  retry funciona.
