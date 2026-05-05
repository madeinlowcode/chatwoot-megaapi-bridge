-- name: GetContactByJID :one
SELECT id, tenant_id, wa_jid, cw_contact_id, cw_conversation_id,
       display_name, last_seen_at, created_at, updated_at
FROM contacts
WHERE tenant_id = $1 AND wa_jid = $2;

-- name: UpsertContact :one
INSERT INTO contacts (tenant_id, wa_jid, cw_contact_id, display_name, last_seen_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (tenant_id, wa_jid) DO UPDATE SET
  cw_contact_id = EXCLUDED.cw_contact_id,
  display_name = COALESCE(EXCLUDED.display_name, contacts.display_name),
  last_seen_at = now()
RETURNING id, tenant_id, wa_jid, cw_contact_id, cw_conversation_id,
          display_name, last_seen_at, created_at, updated_at;

-- name: SetContactConversation :exec
UPDATE contacts SET cw_conversation_id = $2, updated_at = now()
WHERE id = $1;
