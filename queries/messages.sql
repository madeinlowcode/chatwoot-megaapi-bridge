-- name: InsertMessageIfAbsent :one
INSERT INTO messages (tenant_id, direction, external_id, payload, status)
VALUES ($1, $2, $3, $4, 'queued')
ON CONFLICT (tenant_id, direction, external_id) DO NOTHING
RETURNING id;

-- name: GetMessage :one
SELECT id, tenant_id, direction, external_id, cw_message_id, contact_id,
       status, payload, attempts, last_error,
       created_at, updated_at, delivered_at
FROM messages
WHERE id = $1;

-- name: UpdateMessageStatus :exec
UPDATE messages
SET status = $2,
    last_error = $3,
    attempts = attempts + 1,
    delivered_at = CASE WHEN $2 = 'delivered' THEN now() ELSE delivered_at END
WHERE id = $1;

-- name: SetMessageCWID :exec
UPDATE messages SET cw_message_id = $2 WHERE id = $1;

-- name: SetMessageContact :exec
UPDATE messages SET contact_id = $2 WHERE id = $1;

-- name: MarkMessageDuplicate :exec
UPDATE messages SET status = 'duplicate' WHERE id = $1;
