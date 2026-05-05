-- +goose Up
-- +goose StatementBegin

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
  bearer_token_enc BYTEA NOT NULL,
  bearer_token_kid SMALLINT NOT NULL,
  webhook_bearer_enc BYTEA NOT NULL,
  webhook_bearer_kid SMALLINT NOT NULL,
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
  wa_jid                TEXT NOT NULL,
  cw_contact_id         INTEGER NOT NULL,
  cw_conversation_id    INTEGER,
  display_name          TEXT,
  last_seen_at          TIMESTAMPTZ,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(tenant_id, wa_jid)
);

CREATE INDEX idx_contacts_tenant ON contacts(tenant_id);
CREATE INDEX idx_contacts_cw_conversation ON contacts(tenant_id, cw_conversation_id)
  WHERE cw_conversation_id IS NOT NULL;

-- MESSAGES ---------------------------------------------------

CREATE TYPE msg_direction AS ENUM ('inbound', 'outbound');
CREATE TYPE msg_status    AS ENUM (
  'queued',
  'sending',
  'delivered',
  'failed',
  'duplicate'
);

CREATE TABLE messages (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  direction       msg_direction NOT NULL,
  external_id     TEXT NOT NULL,
  cw_message_id   INTEGER,
  contact_id      UUID REFERENCES contacts(id),
  status          msg_status NOT NULL DEFAULT 'queued',
  payload         JSONB NOT NULL,
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
  kind        TEXT NOT NULL,
  ok          BOOLEAN NOT NULL,
  detail      JSONB
);

CREATE INDEX idx_audit_tenant_ts ON audit_events(tenant_id, ts DESC);
CREATE INDEX idx_audit_ts ON audit_events(ts DESC);

-- IDEMPOTENCY KEYS -------------------------------------------

CREATE TABLE idempotency_keys (
  tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  scope       TEXT NOT NULL,
  key_hash    BYTEA NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, scope, key_hash)
);

CREATE INDEX idx_idempotency_created ON idempotency_keys(created_at);

-- ADMIN USERS ------------------------------------------------

CREATE TABLE admin_users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email         TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'admin',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ
);

-- TRIGGERS ---------------------------------------------------

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

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_messages_updated ON messages;
DROP TRIGGER IF EXISTS trg_contacts_updated ON contacts;
DROP TRIGGER IF EXISTS trg_chatwoot_updated ON chatwoot_configs;
DROP TRIGGER IF EXISTS trg_megaapi_updated ON megaapi_configs;
DROP TRIGGER IF EXISTS trg_tenants_updated ON tenants;
DROP FUNCTION IF EXISTS set_updated_at;

DROP TABLE IF EXISTS admin_users;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS messages;
DROP TYPE IF EXISTS msg_status;
DROP TYPE IF EXISTS msg_direction;
DROP TABLE IF EXISTS contacts;
DROP TABLE IF EXISTS chatwoot_configs;
DROP TABLE IF EXISTS megaapi_configs;
DROP TABLE IF EXISTS tenants;

-- +goose StatementEnd
