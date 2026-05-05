-- name: InsertAuditEvent :exec
INSERT INTO audit_events (tenant_id, kind, ok, detail)
VALUES ($1, $2, $3, $4);
