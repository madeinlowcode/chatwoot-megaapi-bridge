-- name: InsertIdempotencyKey :one
INSERT INTO idempotency_keys (tenant_id, scope, key_hash)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING
RETURNING true AS inserted;

-- name: PurgeOldIdempotencyKeys :exec
DELETE FROM idempotency_keys WHERE created_at < $1;
