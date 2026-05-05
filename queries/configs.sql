-- name: UpsertMegaapiConfig :exec
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
  rate_limit_rps = EXCLUDED.rate_limit_rps;

-- name: GetMegaapiConfig :one
SELECT tenant_id, host, instance_key, bearer_token_enc, bearer_token_kid,
       webhook_bearer_enc, webhook_bearer_kid, rate_limit_rps
FROM megaapi_configs
WHERE tenant_id = $1;

-- name: UpsertChatwootConfig :exec
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
  hmac_secret_kid = EXCLUDED.hmac_secret_kid;

-- name: GetChatwootConfig :one
SELECT tenant_id, base_url, api_token_enc, api_token_kid,
       account_id, inbox_id, inbox_identifier,
       hmac_secret_enc, hmac_secret_kid
FROM chatwoot_configs
WHERE tenant_id = $1;
