CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS tenants (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slug                TEXT UNIQUE NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{2,63}$'),
  megaapi_host        TEXT NOT NULL,
  megaapi_instance    TEXT NOT NULL,
  megaapi_token_enc   BYTEA NOT NULL,
  chatwoot_url        TEXT NOT NULL,
  chatwoot_token_enc  BYTEA NOT NULL,
  chatwoot_account_id INTEGER NOT NULL,
  chatwoot_inbox_id   INTEGER NOT NULL,
  hmac_secret_enc     BYTEA NOT NULL,
  webhook_bearer_enc  BYTEA NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS contacts (
  tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  wa_jid              TEXT NOT NULL,
  cw_contact_id       BIGINT NOT NULL,
  cw_conversation_id  BIGINT NOT NULL,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, wa_jid)
);

CREATE TABLE IF NOT EXISTS messages (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  direction    TEXT NOT NULL CHECK (direction IN ('in','out')),
  external_id  TEXT NOT NULL,
  status       TEXT NOT NULL CHECK (status IN ('pending','done','failed')),
  attempts     SMALLINT NOT NULL DEFAULT 0,
  last_error   TEXT,
  payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(tenant_id, direction, external_id)
);

CREATE INDEX IF NOT EXISTS idx_messages_pending ON messages(tenant_id) WHERE status = 'pending';
