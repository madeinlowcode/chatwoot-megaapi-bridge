package repo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Queries wraps a pgx pool and exposes one method per business operation.
type Queries struct {
	pool *pgxpool.Pool
}

// New constructs a Queries instance.
func New(pool *pgxpool.Pool) *Queries {
	return &Queries{pool: pool}
}

// Pool returns the underlying connection pool (for advanced callers).
func (q *Queries) Pool() *pgxpool.Pool { return q.pool }

// ---------- Tenants ----------

const sqlGetTenantBySlug = `
SELECT id, slug, display_name, active, created_at, updated_at
FROM tenants WHERE slug = $1 AND active = true`

func (q *Queries) GetTenantBySlug(ctx context.Context, slug string) (*Tenant, error) {
	var t Tenant
	err := q.pool.QueryRow(ctx, sqlGetTenantBySlug, slug).Scan(
		&t.ID, &t.Slug, &t.DisplayName, &t.Active, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

const sqlGetTenantBySlugAny = `
SELECT id, slug, display_name, active, created_at, updated_at
FROM tenants WHERE slug = $1`

func (q *Queries) GetTenantBySlugAny(ctx context.Context, slug string) (*Tenant, error) {
	var t Tenant
	err := q.pool.QueryRow(ctx, sqlGetTenantBySlugAny, slug).Scan(
		&t.ID, &t.Slug, &t.DisplayName, &t.Active, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

const sqlCreateTenant = `
INSERT INTO tenants (slug, display_name) VALUES ($1, $2)
RETURNING id, slug, display_name, active, created_at, updated_at`

func (q *Queries) CreateTenant(ctx context.Context, slug, name string) (*Tenant, error) {
	var t Tenant
	err := q.pool.QueryRow(ctx, sqlCreateTenant, slug, name).Scan(
		&t.ID, &t.Slug, &t.DisplayName, &t.Active, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

const sqlListTenants = `
SELECT id, slug, display_name, active, created_at, updated_at
FROM tenants ORDER BY created_at DESC`

func (q *Queries) ListTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := q.pool.Query(ctx, sqlListTenants)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Tenant, 0)
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Slug, &t.DisplayName, &t.Active, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const sqlDisableTenant = `UPDATE tenants SET active = false WHERE slug = $1`

func (q *Queries) DisableTenant(ctx context.Context, slug string) error {
	_, err := q.pool.Exec(ctx, sqlDisableTenant, slug)
	return err
}

// ---------- megaapi_configs ----------

const sqlUpsertMegaapi = `
INSERT INTO megaapi_configs (
  tenant_id, host, instance_key,
  bearer_token_enc, bearer_token_kid,
  webhook_bearer_enc, webhook_bearer_kid,
  rate_limit_rps
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tenant_id) DO UPDATE SET
  host = EXCLUDED.host,
  instance_key = EXCLUDED.instance_key,
  bearer_token_enc = EXCLUDED.bearer_token_enc,
  bearer_token_kid = EXCLUDED.bearer_token_kid,
  webhook_bearer_enc = EXCLUDED.webhook_bearer_enc,
  webhook_bearer_kid = EXCLUDED.webhook_bearer_kid,
  rate_limit_rps = EXCLUDED.rate_limit_rps`

func (q *Queries) UpsertMegaapiConfig(ctx context.Context, c MegaapiConfig) error {
	_, err := q.pool.Exec(ctx, sqlUpsertMegaapi,
		c.TenantID, c.Host, c.InstanceKey,
		c.BearerTokenEnc, c.BearerTokenKID,
		c.WebhookBearerEnc, c.WebhookBearerKID,
		c.RateLimitRPS,
	)
	return err
}

const sqlGetMegaapi = `
SELECT tenant_id, host, instance_key,
       bearer_token_enc, bearer_token_kid,
       webhook_bearer_enc, webhook_bearer_kid,
       rate_limit_rps
FROM megaapi_configs WHERE tenant_id = $1`

func (q *Queries) GetMegaapiConfig(ctx context.Context, tenantID uuid.UUID) (*MegaapiConfig, error) {
	var c MegaapiConfig
	err := q.pool.QueryRow(ctx, sqlGetMegaapi, tenantID).Scan(
		&c.TenantID, &c.Host, &c.InstanceKey,
		&c.BearerTokenEnc, &c.BearerTokenKID,
		&c.WebhookBearerEnc, &c.WebhookBearerKID,
		&c.RateLimitRPS,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

// ---------- chatwoot_configs ----------

const sqlUpsertChatwoot = `
INSERT INTO chatwoot_configs (
  tenant_id, base_url, api_token_enc, api_token_kid,
  account_id, inbox_id, inbox_identifier,
  hmac_secret_enc, hmac_secret_kid
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (tenant_id) DO UPDATE SET
  base_url = EXCLUDED.base_url,
  api_token_enc = EXCLUDED.api_token_enc,
  api_token_kid = EXCLUDED.api_token_kid,
  account_id = EXCLUDED.account_id,
  inbox_id = EXCLUDED.inbox_id,
  inbox_identifier = EXCLUDED.inbox_identifier,
  hmac_secret_enc = EXCLUDED.hmac_secret_enc,
  hmac_secret_kid = EXCLUDED.hmac_secret_kid`

func (q *Queries) UpsertChatwootConfig(ctx context.Context, c ChatwootConfig) error {
	_, err := q.pool.Exec(ctx, sqlUpsertChatwoot,
		c.TenantID, c.BaseURL, c.APITokenEnc, c.APITokenKID,
		c.AccountID, c.InboxID, c.InboxIdentifier,
		c.HMACSecretEnc, c.HMACSecretKID,
	)
	return err
}

const sqlGetChatwoot = `
SELECT tenant_id, base_url, api_token_enc, api_token_kid,
       account_id, inbox_id, inbox_identifier,
       hmac_secret_enc, hmac_secret_kid
FROM chatwoot_configs WHERE tenant_id = $1`

func (q *Queries) GetChatwootConfig(ctx context.Context, tenantID uuid.UUID) (*ChatwootConfig, error) {
	var c ChatwootConfig
	err := q.pool.QueryRow(ctx, sqlGetChatwoot, tenantID).Scan(
		&c.TenantID, &c.BaseURL, &c.APITokenEnc, &c.APITokenKID,
		&c.AccountID, &c.InboxID, &c.InboxIdentifier,
		&c.HMACSecretEnc, &c.HMACSecretKID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

// ---------- messages ----------

const sqlInsertMessageIfAbsent = `
INSERT INTO messages (tenant_id, direction, external_id, payload, status)
VALUES ($1, $2, $3, $4, 'queued')
ON CONFLICT (tenant_id, direction, external_id) DO NOTHING
RETURNING id`

// InsertMessageIfAbsent inserts a queued message; returns (id, true) if new,
// (zero, false) if a row already existed (duplicate external_id).
func (q *Queries) InsertMessageIfAbsent(ctx context.Context, tenantID uuid.UUID, dir MsgDirection, externalID string, payload []byte) (uuid.UUID, bool, error) {
	if !json.Valid(payload) {
		payload = []byte("{}")
	}
	var id uuid.UUID
	err := q.pool.QueryRow(ctx, sqlInsertMessageIfAbsent,
		tenantID, string(dir), externalID, payload,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return id, true, nil
}

const sqlGetMessage = `
SELECT id, tenant_id, direction, external_id, cw_message_id, contact_id,
       status, payload, attempts, last_error,
       created_at, updated_at, delivered_at
FROM messages WHERE id = $1`

func (q *Queries) GetMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	var m Message
	var dir, status string
	err := q.pool.QueryRow(ctx, sqlGetMessage, id).Scan(
		&m.ID, &m.TenantID, &dir, &m.ExternalID, &m.CWMessageID, &m.ContactID,
		&status, &m.Payload, &m.Attempts, &m.LastError,
		&m.CreatedAt, &m.UpdatedAt, &m.DeliveredAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.Direction = MsgDirection(dir)
	m.Status = MsgStatus(status)
	return &m, nil
}

const sqlUpdateMessageStatus = `
UPDATE messages
SET status = $2,
    last_error = $3,
    attempts = attempts + 1,
    delivered_at = CASE WHEN $2::text = 'delivered' THEN now() ELSE delivered_at END
WHERE id = $1`

func (q *Queries) UpdateMessageStatus(ctx context.Context, id uuid.UUID, status MsgStatus, errMsg *string) error {
	_, err := q.pool.Exec(ctx, sqlUpdateMessageStatus, id, string(status), errMsg)
	return err
}

const sqlSetMessageCWID = `UPDATE messages SET cw_message_id = $2 WHERE id = $1`

func (q *Queries) SetMessageCWID(ctx context.Context, id uuid.UUID, cwID int64) error {
	_, err := q.pool.Exec(ctx, sqlSetMessageCWID, id, cwID)
	return err
}

// sqlSetMessageDelivered atomically pins cw_message_id and the terminal status
// in a single UPDATE so a successful Chatwoot send and its DB-side acknowledgement
// can never disagree. Returning the error lets the worker surface it to asynq for retry.
const sqlSetMessageDelivered = `
UPDATE messages
SET cw_message_id = $2,
    status        = 'delivered',
    last_error    = NULL,
    delivered_at  = now()
WHERE id = $1`

// SetMessageDelivered pins cw_message_id and flips status to 'delivered' atomically.
func (q *Queries) SetMessageDelivered(ctx context.Context, id uuid.UUID, cwID int64) error {
	_, err := q.pool.Exec(ctx, sqlSetMessageDelivered, id, cwID)
	return err
}

// sqlMarkMessageDelivered is the no-cw-id variant for outbound messages where
// the upstream (megaAPI) does not return a stable id we want to persist.
const sqlMarkMessageDelivered = `
UPDATE messages
SET status       = 'delivered',
    last_error   = NULL,
    delivered_at = now()
WHERE id = $1`

// MarkMessageDelivered flips status to 'delivered' atomically without a cw_message_id.
func (q *Queries) MarkMessageDelivered(ctx context.Context, id uuid.UUID) error {
	_, err := q.pool.Exec(ctx, sqlMarkMessageDelivered, id)
	return err
}

const sqlSetMessageContact = `UPDATE messages SET contact_id = $2 WHERE id = $1`

func (q *Queries) SetMessageContact(ctx context.Context, id, contactID uuid.UUID) error {
	_, err := q.pool.Exec(ctx, sqlSetMessageContact, id, contactID)
	return err
}

const sqlMarkMessageDuplicate = `UPDATE messages SET status = 'duplicate' WHERE id = $1`

func (q *Queries) MarkMessageDuplicate(ctx context.Context, id uuid.UUID) error {
	_, err := q.pool.Exec(ctx, sqlMarkMessageDuplicate, id)
	return err
}

// ---------- contacts ----------

const sqlGetContactByJID = `
SELECT id, tenant_id, wa_jid, cw_contact_id, cw_conversation_id,
       display_name, last_seen_at, created_at, updated_at
FROM contacts WHERE tenant_id = $1 AND wa_jid = $2`

func (q *Queries) GetContactByJID(ctx context.Context, tenantID uuid.UUID, jid string) (*Contact, error) {
	var c Contact
	err := q.pool.QueryRow(ctx, sqlGetContactByJID, tenantID, jid).Scan(
		&c.ID, &c.TenantID, &c.WAJID, &c.CWContactID, &c.CWConversationID,
		&c.DisplayName, &c.LastSeenAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

const sqlUpsertContact = `
INSERT INTO contacts (tenant_id, wa_jid, cw_contact_id, display_name, last_seen_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (tenant_id, wa_jid) DO UPDATE SET
  cw_contact_id = EXCLUDED.cw_contact_id,
  display_name = COALESCE(EXCLUDED.display_name, contacts.display_name),
  last_seen_at = now()
RETURNING id, tenant_id, wa_jid, cw_contact_id, cw_conversation_id,
          display_name, last_seen_at, created_at, updated_at`

func (q *Queries) UpsertContact(ctx context.Context, tenantID uuid.UUID, jid string, cwContactID int64, displayName *string) (*Contact, error) {
	var c Contact
	err := q.pool.QueryRow(ctx, sqlUpsertContact,
		tenantID, jid, cwContactID, displayName,
	).Scan(
		&c.ID, &c.TenantID, &c.WAJID, &c.CWContactID, &c.CWConversationID,
		&c.DisplayName, &c.LastSeenAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

const sqlSetContactConversation = `
UPDATE contacts SET cw_conversation_id = $2, updated_at = now() WHERE id = $1`

func (q *Queries) SetContactConversation(ctx context.Context, id uuid.UUID, conversationID int64) error {
	_, err := q.pool.Exec(ctx, sqlSetContactConversation, id, conversationID)
	return err
}

// ---------- idempotency ----------

const sqlInsertIdempotencyKey = `
INSERT INTO idempotency_keys (tenant_id, scope, key_hash)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING
RETURNING true`

// InsertIdempotencyKey returns true if the key was newly inserted, false if it already existed.
// scope is "inbound" or "outbound".
func (q *Queries) InsertIdempotencyKey(ctx context.Context, tenantID uuid.UUID, scope string, externalID string) (bool, error) {
	if scope != "inbound" && scope != "outbound" {
		return false, fmt.Errorf("repo: invalid idempotency scope %q", scope)
	}
	hash := sha256.Sum256([]byte(externalID))
	var inserted bool
	err := q.pool.QueryRow(ctx, sqlInsertIdempotencyKey, tenantID, scope, hash[:]).Scan(&inserted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return inserted, nil
}

const sqlPurgeIdempotency = `DELETE FROM idempotency_keys WHERE created_at < $1`

func (q *Queries) PurgeOldIdempotencyKeys(ctx context.Context, olderThan time.Time) error {
	_, err := q.pool.Exec(ctx, sqlPurgeIdempotency, olderThan)
	return err
}

// ---------- audit ----------

const sqlInsertAudit = `
INSERT INTO audit_events (tenant_id, kind, ok, detail) VALUES ($1, $2, $3, $4)`

func (q *Queries) InsertAuditEvent(ctx context.Context, tenantID *uuid.UUID, kind string, ok bool, detail []byte) error {
	if detail != nil && !json.Valid(detail) {
		detail = nil
	}
	_, err := q.pool.Exec(ctx, sqlInsertAudit, tenantID, kind, ok, detail)
	return err
}

// ErrNotFound is returned when a single-row query produced no rows.
var ErrNotFound = errors.New("repo: not found")
