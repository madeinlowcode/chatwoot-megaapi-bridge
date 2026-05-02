# 02 — Arquitetura

## Visão geral

Sistema composto por três camadas dentro de uma única rede Docker, mais
serviços externos (megaAPI SaaS, WhatsApp do cliente final).

```
┌─────────────────────────────────────────────────────────────────────────┐
│                       Rede Docker: omni-net                             │
│                                                                         │
│   ┌──────────┐    ┌──────────────────┐    ┌─────────────────────┐      │
│   │  Caddy   │───▶│ chatwoot (rails) │───▶│ postgres            │      │
│   │ 80/443   │    │ :3000            │    │ (chatwoot + bridge) │      │
│   │ auto-TLS │    └──────────────────┘    └─────────────────────┘      │
│   └────┬─────┘    ┌──────────────────┐    ┌──────────┐                 │
│        │          │ sidekiq (worker) │    │ redis    │◀────┐           │
│        │          └──────────────────┘    │ AOF on   │     │           │
│        │                                  └──────────┘     │           │
│        │          ┌──────────────────┐                     │           │
│        ├─────────▶│ bridge-api (Go)  │─────────────────────┤           │
│        │          │ :8080            │ pub/sub asynq       │           │
│        │          └──────────────────┘                     │           │
│        │          ┌──────────────────┐                     │           │
│        │          │ bridge-worker(Go)│─────────────────────┘           │
│        │          └──────────────────┘                                 │
│        │                                                               │
└────────┼───────────────────────────────────────────────────────────────┘
         │
         ▼
   Internet ──────▶ megaAPI SaaS (apibusiness{N}.megaapi.com.br)
                        │
                        ▼
                   WhatsApp (cliente final)
```

## Componentes

### Caddy
- Reverse proxy + TLS automático (Let's Encrypt).
- Roteia: `/` → Chatwoot, `/admin` e `/v1/*` → bridge-api.
- Único ponto de entrada público.

### Chatwoot (rails + sidekiq)
- Imagem oficial `chatwoot/chatwoot:vX.Y` (versão pinada).
- Não modificamos. Usa API Channel do Chatwoot para integração custom.
- Compartilha PostgreSQL e Redis com bridge (schemas/DBs distintos).

### bridge-api (Go)
- HTTP server: rotas administrativas (`/admin/*`) e webhooks
  (`/v1/wa/{tenant_id}`, `/v1/cw/{tenant_id}`).
- Recebe webhook → valida → enfileira em Redis (asynq) → ACK 200.
- Stateless. Escala horizontalmente.

### bridge-worker (Go)
- Mesmo binário do bridge-api, comando diferente.
- Consome filas asynq, traduz payload, faz HTTP outbound.
- Filas separadas por direção: `wa-to-cw`, `cw-to-wa`.
- Configuração de concorrência por fila.

### PostgreSQL
- DB `chatwoot` (uso do Chatwoot, intocado).
- DB `bridge` (uso exclusivo do bridge): tenants, configs, mensagens, audit.

### Redis
- Backend asynq (filas, scheduler, retry, DLQ).
- Persistência AOF habilitada (durabilidade da fila).

## Fluxos de mensagem

### Fluxo INBOUND (WhatsApp → Chatwoot)

```
1. Cliente final manda msg WhatsApp
2. megaAPI recebe e dispara webhook para:
     POST https://dominio.com/v1/wa/{tenant_id}
     Authorization: Bearer <token registrado pelo bridge na megaAPI>
3. bridge-api:
     a. Resolve tenant_id → config (cache hot, fallback Postgres)
     b. Valida assinatura/token
     c. Hash do messageId — se duplicado, ACK 200 sem enfileirar
     d. Persiste mensagem em messages(direction='inbound', status='queued')
     e. Enfileira job asynq na fila "wa-to-cw"
     f. ACK 200 (≤10 ms p99)
4. bridge-worker (consumer):
     a. Pega job
     b. Resolve/cria contato Chatwoot (POST /api/v1/accounts/{aid}/contacts)
     c. Resolve/cria conversation (POST /conversations)
     d. POST mensagem (POST /conversations/{cid}/messages)
     e. Atualiza messages(status='delivered')
     f. Em falha: retry com backoff exponencial
     g. Após N falhas: move pra DLQ + alerta
```

### Fluxo OUTBOUND (Chatwoot → WhatsApp)

```
1. Atendente responde no Chatwoot
2. Chatwoot dispara webhook outgoing (configurado uma vez por inbox):
     POST https://dominio.com/v1/cw/{tenant_id}
     X-Chatwoot-Signature: HMAC-SHA256
3. bridge-api:
     a. Valida HMAC com secret do tenant
     b. Filtra eventos (só message_created, sender=user)
     c. Hash do message.id — dedupe
     d. Persiste messages(direction='outbound', status='queued')
     e. Enfileira na fila "cw-to-wa"
     f. ACK 200
4. bridge-worker:
     a. Resolve config megaAPI do tenant (host, instance_key, token)
     b. Mapeia payload Chatwoot → schema megaAPI
     c. POST https://{host}/rest/sendMessage/{instance_key}/text
        Authorization: Bearer {megaapi_token}
     d. Atualiza status conforme resposta
     e. Retry/DLQ em falha
```

## Pontos chave de design

1. **ACK rápido sempre** — bridge-api nunca aguarda I/O downstream. Toda
   chamada externa é responsabilidade do worker.
2. **Stateless API + stateful workers** — réplicas API atrás de load balancer;
   workers escalam por queue depth.
3. **DB compartilhado, schemas separados** — economia operacional, sem
   acoplamento de aplicação.
4. **Idempotência ponta a ponta** — `external_id` único por direção evita
   duplicatas em retry.
5. **Mídia por URL** — bridge não baixa/upa bytes. Encaminha URL assinada da
   megaAPI para o Chatwoot baixar (e vice-versa quando suportado).

## Diagrama de sequência: msg inbound

```
WhatsApp     megaAPI       bridge-api    redis     bridge-worker    Chatwoot
   │            │              │           │            │              │
   ├───msg────▶│              │           │            │              │
   │            ├─webhook────▶│           │            │              │
   │            │              ├─enqueue──▶           │              │
   │            │◀─200─────────┤           │            │              │
   │            │              │           ├─dequeue──▶│              │
   │            │              │           │            ├─POST contact▶│
   │            │              │           │            │◀─201─────────┤
   │            │              │           │            ├─POST msg────▶│
   │            │              │           │            │◀─201─────────┤
   │            │              │           │            ├─update DB    │
   │            │              │           │            │              │
```

## Escalabilidade

- **Vertical** primeiro: Go aguenta milhares de req/s em 1 vCPU.
- **Horizontal**: réplicas bridge-api stateless atrás do Caddy. Workers
  escalam por `--concurrency` ou réplicas.
- **DB**: read replicas se necessário. Particionamento de `messages` por
  `created_at` (mensal) quando volume justificar.
