# 05 — Modelo de Dados

## Database

PostgreSQL 15+. Schema dedicado `bridge` no mesmo cluster do Chatwoot
(separação por database lógico ou schema; recomendado **database separado**
`bridge` para isolamento de backup e migrations).

## DDL completo (planejado)

```sql
-- ============================================================
-- Migration 0001 — schema inicial
-- ============================================================

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- TENANTS ----------------------------------------------------

CREATE TABLE tenants (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slug         TEXT UNIQUE NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{2,63}$'),
  display_name TEXT NOT NULL,
  active       BOOLEAN NOT NULL DEFAULT true,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tenants_active ON tenants(active) WHERE active = true;

-- CONFIGS ----------------------------------------------------

CREATE TABLE megaapi_configs (
  tenant_id        UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  host             TEXT NOT NULL CHECK (host ~ '^https://'),
  instance_key     TEXT NOT NULL,
  bearer_token_enc BYTEA NOT NULL,            -- AES-GCM ciphertext
  bearer_token_kid SMALLINT NOT NULL,         -- key id para rotação
  webhook_secret   TEXT,
  rate_limit_rps   INTEGER NOT NULL DEFAULT 20,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(host, instance_key)
);

CREATE TABLE chatwoot_configs (
  tenant_id         UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  base_url          TEXT NOT NULL CHECK (base_url ~ '^https?://'),
  api_token_enc     BYTEA NOT NULL,
  api_token_kid     SMALLINT NOT NULL,
  account_id        INTEGER NOT NULL,
  inbox_id          INTEGER NOT NULL,
  inbox_identifier  TEXT,
  hmac_secret_enc   BYTEA NOT NULL,
  hmac_secret_kid   SMALLINT NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(base_url, account_id, inbox_id)
);

-- CONTACTS / CONVERSATIONS MAPPING ---------------------------

CREATE TABLE contacts (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id             UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  wa_jid                TEXT NOT NULL,            -- ex: 5511999999999@s.whatsapp.net
  cw_contact_id         INTEGER NOT NULL,
  cw_conversation_id    INTEGER,                  -- atual aberta
  display_name          TEXT,
  last_seen_at          TIMESTAMPTZ,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(tenant_id, wa_jid)
);

CREATE INDEX idx_contacts_tenant ON contacts(tenant_id);
CREATE INDEX idx_contacts_cw_conversation ON contacts(tenant_id, cw_conversation_id)
  WHERE cw_conversation_id IS NOT NULL;

-- MESSAGES (audit + idempotência + retry) --------------------

CREATE TYPE msg_direction AS ENUM ('inbound', 'outbound');
CREATE TYPE msg_status    AS ENUM (
  'queued',     -- enfileirada, aguardando worker
  'sending',    -- worker pegou, em curso
  'delivered',  -- chegou no destino
  'failed',     -- esgotou retries
  'duplicate'   -- já vista, ignorada
);

CREATE TABLE messages (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  direction       msg_direction NOT NULL,
  external_id     TEXT NOT NULL,                  -- megaAPI messageId ou Chatwoot id
  cw_message_id   INTEGER,
  contact_id      UUID REFERENCES contacts(id),
  status          msg_status NOT NULL DEFAULT 'queued',
  payload         JSONB NOT NULL,                 -- payload original recebido
  attempts        SMALLINT NOT NULL DEFAULT 0,
  last_error      TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  delivered_at    TIMESTAMPTZ,
  UNIQUE(tenant_id, direction, external_id)
);

CREATE INDEX idx_messages_tenant_created ON messages(tenant_id, created_at DESC);
CREATE INDEX idx_messages_status ON messages(status) WHERE status IN ('queued', 'sending', 'failed');

-- AUDIT EVENTS -----------------------------------------------

CREATE TABLE audit_events (
  id          BIGSERIAL PRIMARY KEY,
  tenant_id   UUID REFERENCES tenants(id) ON DELETE CASCADE,
  ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
  kind        TEXT NOT NULL,           -- ex: 'tenant.created', 'webhook.received', 'send.failed'
  ok          BOOLEAN NOT NULL,
  detail      JSONB
);

CREATE INDEX idx_audit_tenant_ts ON audit_events(tenant_id, ts DESC);
CREATE INDEX idx_audit_ts ON audit_events(ts DESC);

-- IDEMPOTENCY KEYS (rápido, separado de messages) ------------

CREATE TABLE idempotency_keys (
  tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  scope       TEXT NOT NULL,               -- 'inbound' | 'outbound'
  key_hash    BYTEA NOT NULL,              -- sha256 do external_id
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, scope, key_hash)
);

CREATE INDEX idx_idempotency_created ON idempotency_keys(created_at);

-- ADMIN USERS (autenticação UI) ------------------------------

CREATE TABLE admin_users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email         TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,             -- argon2id
  role          TEXT NOT NULL DEFAULT 'admin',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ
);

-- TRIGGERS de updated_at -------------------------------------

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_tenants_updated BEFORE UPDATE ON tenants
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_megaapi_updated BEFORE UPDATE ON megaapi_configs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_chatwoot_updated BEFORE UPDATE ON chatwoot_configs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_contacts_updated BEFORE UPDATE ON contacts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_messages_updated BEFORE UPDATE ON messages
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

## Política de retenção

| Tabela | Retenção | Mecanismo |
|--------|----------|-----------|
| `messages` | 90 dias | Cron diário deleta `created_at < now() - 90d` |
| `audit_events` | 30 dias | Cron diário |
| `idempotency_keys` | 7 dias | Cron diário (suficiente para retry de fonte) |
| `contacts` | indefinido | só remove com cascade de tenant |
| `tenants` / `*_configs` | indefinido | apenas via UI |

Particionamento: ao crescer `messages` > 50M linhas, particionar por mês com
`pg_partman`.

## Backup

- `pg_dump` diário do database `bridge` → arquivo local + S3 opcional.
- Retenção 30 dias.
- Comando documentado no install (cron Docker side-container).

## Migrations

Ferramenta: `goose`. Migrations versionadas em `migrations/0001_init.sql`,
`0002_*.sql`...

Bridge roda migrations automaticamente no startup com flag `--migrate=auto`
(default em dev) ou `--migrate=manual` em produção (requer `bridge migrate
up`).

## Queries críticas (sqlc)

- `GetTenantBySlug(slug)` — hot path, indexed.
- `UpsertContact(tenant_id, wa_jid, cw_contact_id, cw_conversation_id)`.
- `InsertMessageIfAbsent(tenant_id, direction, external_id, payload)` — usa
  `INSERT ... ON CONFLICT DO NOTHING RETURNING id`.
- `UpdateMessageStatus(id, status, error)`.
- `LogAudit(tenant_id, kind, ok, detail)` — fire-and-forget, async.
