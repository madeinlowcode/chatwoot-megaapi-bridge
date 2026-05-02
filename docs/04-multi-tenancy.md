# 04 — Multi-tenancy

## Definição de tenant

Um tenant representa **uma associação 1-pra-1 entre uma instância megaAPI e
uma inbox do tipo API Channel no Chatwoot**. Um operador pode cadastrar N
tenants na mesma instância do bridge.

## Por que multi-tenant

- Operador pode ter múltiplos clientes finais no mesmo Chatwoot.
- Cada cliente tem instância megaAPI própria (potencialmente em hosts
  diferentes: `apibusiness1...apibusinessN`).
- Cada inbox no Chatwoot recebe mensagens de uma instância específica.

## Host megaAPI dinâmico

**Requisito explícito do produto.** Cada tenant carrega o host completo. Não
há lista hard-coded.

Exemplos válidos por tenant:
- `https://apibusiness1.megaapi.com.br`
- `https://apibusiness7.megaapi.com.br`
- `https://apicustomdomain.cliente.com.br` (megaAPI white-label)

Validação na criação do tenant:
1. Schema de URL válido (`https://`).
2. DNS resolve.
3. `GET {host}/rest/instance/{instance_key}/me` retorna 200 com bearer token.

## Modelo de dados de tenant

(detalhamento completo em [05-data-model.md](./05-data-model.md))

```sql
CREATE TABLE tenants (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slug          TEXT UNIQUE NOT NULL,    -- usado em URLs
  display_name  TEXT NOT NULL,
  active        BOOLEAN NOT NULL DEFAULT true,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE megaapi_configs (
  tenant_id        UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  host             TEXT NOT NULL,
  instance_key     TEXT NOT NULL,
  bearer_token_enc BYTEA NOT NULL,        -- AES-GCM
  webhook_secret   TEXT,                  -- opcional, megaAPI ainda não suporta
  UNIQUE(host, instance_key)
);

CREATE TABLE chatwoot_configs (
  tenant_id      UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  base_url       TEXT NOT NULL,
  api_token_enc  BYTEA NOT NULL,
  account_id     INTEGER NOT NULL,
  inbox_id       INTEGER NOT NULL,
  hmac_secret_enc BYTEA NOT NULL,         -- valida webhook do Chatwoot
  inbox_identifier TEXT,                  -- API Channel identifier
  UNIQUE(base_url, account_id, inbox_id)
);
```

## Roteamento por URL

Bridge expõe rotas tenant-scoped:

| Rota | Origem | Direção |
|------|--------|---------|
| `POST /v1/wa/{tenant_slug}` | megaAPI webhook | Inbound (WA→CW) |
| `POST /v1/cw/{tenant_slug}` | Chatwoot webhook | Outbound (CW→WA) |
| `GET  /v1/tenants/{slug}/health` | Operador | Status |

**Por que slug e não UUID na URL?** Legibilidade humana ao configurar webhook
na megaAPI/Chatwoot. Slug é único, sluggificado a partir do nome.

## Lookup e cache

Dado que cada webhook recebido faz lookup de tenant, hot path precisa ser
ultra-rápido.

```
in-memory cache (TTL 5min, LRU 10k entradas) ──► PostgreSQL
                  │ miss
                  ▼
              SELECT joined config
              encrypt/decrypt secrets em memória só
```

Invalidação:
- Em update via UI: cache invalidado por slug.
- TTL natural de 5min cobre cenários de N réplicas (eventual consistency
  aceitável).
- PubSub Redis canal `tenant:invalidate` propaga entre réplicas (opcional v2).

## Isolamento

- **Dados** — toda query carrega `tenant_id` em WHERE. Camada `repo` força
  passagem do tenant_id em todos os métodos (assinatura).
- **Filas** — jobs asynq incluem `tenant_id` no payload. Logs estruturados
  carregam `tenant_id` no contexto.
- **Crypto** — secrets cifrados em repouso. Master key única do bridge
  (rotacionável).
- **Rate limit** — token bucket por tenant (ver
  [07-reliability-and-performance.md](./07-reliability-and-performance.md)).

## Provisionamento de tenant — fluxo na UI

1. Admin acessa `/admin/tenants/new`
2. Preenche: nome, slug (auto), host megaAPI, instance_key, token
3. Bridge testa `GET {host}/rest/instance/{instance_key}/me` com token
4. Preenche: URL Chatwoot, API token (de uma conta admin do Chatwoot)
5. Bridge chama `GET {chatwoot}/api/v1/accounts` e popula dropdown de accounts
6. Bridge chama `GET {chatwoot}/api/v1/accounts/{aid}/inboxes` e popula
   dropdown filtrando apenas API Channel
7. Admin escolhe inbox; bridge captura `inbox_identifier` + `hmac_token` (se
   inbox ainda não criada, oferece criar com 1 clique)
8. Bridge persiste configs (tokens cifrados)
9. Bridge chama `POST {host}/rest/webhook/{instance_key}/configWebhook` com
   URL `https://dominio/v1/wa/{slug}` para auto-registrar webhook
10. Bridge configura webhook outgoing no Chatwoot apontando para
    `https://dominio/v1/cw/{slug}` via API
11. UI mostra status: "Tenant ativo, aguardando primeira mensagem"

## Migração de tenant entre instâncias bridge

- Export: `bridge tenants export {slug} > tenant.json` (sem secrets em claro)
- Import: `bridge tenants import < tenant.json` (admin reinforma secrets)
- Útil para mover de POC local para VPS produção.

## Limites

- Soft limit: 500 tenants ativos por instância bridge (limitado por cache LRU
  e queue depth manejável). Acima disso, escalar horizontalmente.
- Por tenant: 1 instância megaAPI + 1 inbox Chatwoot. Múltiplos números =
  múltiplos tenants.
