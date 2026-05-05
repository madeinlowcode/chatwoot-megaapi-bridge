-- name: GetTenantBySlug :one
SELECT id, slug, display_name, active, created_at, updated_at
FROM tenants
WHERE slug = $1 AND active = true;

-- name: CreateTenant :one
INSERT INTO tenants (slug, display_name)
VALUES ($1, $2)
RETURNING id, slug, display_name, active, created_at, updated_at;

-- name: ListTenants :many
SELECT id, slug, display_name, active, created_at, updated_at
FROM tenants
ORDER BY created_at DESC;

-- name: GetTenantBySlugAny :one
SELECT id, slug, display_name, active, created_at, updated_at
FROM tenants
WHERE slug = $1;

-- name: DisableTenant :exec
UPDATE tenants SET active = false WHERE slug = $1;
