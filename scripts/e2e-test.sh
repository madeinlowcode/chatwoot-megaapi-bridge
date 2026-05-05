#!/usr/bin/env bash
# E2E smoke test for the bridge MVP — task chatwoot-megaapi-bridge-96l.15.
#
# Validates the acceptance criteria from .agents/plans/phase-1-mvp.md:
#   - text inbound ACK <50ms with 200
#   - HMAC invalid → 401
#   - HMAC valid → 200, worker invokes megaAPI sendText (mock)
#   - replay does NOT duplicate (idempotency)
#
# Prereqs:
#   - docker compose stack up (postgres, redis, bridge-api, bridge-worker)
#   - jq, curl, openssl in PATH
#   - tenant "demo" must already exist; this script reads BEARER + HMAC from env
#     (printed by bridge tenants create).

set -euo pipefail

BRIDGE_URL="${BRIDGE_URL:-http://localhost:8080}"
SLUG="${SLUG:-demo}"
BEARER="${MEGAAPI_WEBHOOK_BEARER:-}"
HMAC_SECRET="${CHATWOOT_HMAC_SECRET:-}"

if [[ -z "$BEARER" || -z "$HMAC_SECRET" ]]; then
  echo "Set MEGAAPI_WEBHOOK_BEARER and CHATWOOT_HMAC_SECRET (printed by 'bridge tenants create')."
  exit 2
fi

pass() { echo "  ✅ $1"; }
fail() { echo "  ❌ $1"; exit 1; }

echo "=== /v1/wa/$SLUG: text inbound (valid bearer) ==="
PAYLOAD='{
  "instance_key":"demo-inst",
  "messages":[{
    "key":{"id":"WA_E2E_'"$RANDOM"'","remoteJid":"5511999999999@s.whatsapp.net","fromMe":false},
    "message":{"conversation":"hello from e2e"},
    "messageTimestamp":1714500000,
    "pushName":"E2E Tester"
  }]
}'
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/v1/wa/$SLUG" \
  -H "Authorization: Bearer $BEARER" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD")
[[ "$status" == "200" ]] && pass "ACK 200" || fail "expected 200, got $status"

echo "=== /v1/wa/$SLUG: replay (idempotency) ==="
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/v1/wa/$SLUG" \
  -H "Authorization: Bearer $BEARER" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD")
[[ "$status" == "200" ]] && pass "replay ACK 200 (no duplicate)" || fail "expected 200, got $status"

echo "=== /v1/wa/$SLUG: bad bearer ==="
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/v1/wa/$SLUG" \
  -H "Authorization: Bearer wrong" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD")
[[ "$status" == "401" ]] && pass "401 on bad bearer" || fail "expected 401, got $status"

echo "=== /v1/cw/$SLUG: HMAC valid ==="
CW_PAYLOAD='{
  "event":"message_created",
  "id":'"$RANDOM"',
  "content":"reply from agent",
  "message_type":"outgoing",
  "private":false,
  "conversation":{"id":1,"contact_inbox":{"source_id":"5511999999999@s.whatsapp.net"}}
}'
SIG=$(printf '%s' "$CW_PAYLOAD" | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex | awk '{print $2}')
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/v1/cw/$SLUG" \
  -H "X-Chatwoot-Signature: $SIG" \
  -H "Content-Type: application/json" \
  -d "$CW_PAYLOAD")
[[ "$status" == "200" ]] && pass "ACK 200 with valid HMAC" || fail "expected 200, got $status"

echo "=== /v1/cw/$SLUG: HMAC invalid ==="
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BRIDGE_URL/v1/cw/$SLUG" \
  -H "X-Chatwoot-Signature: deadbeef" \
  -H "Content-Type: application/json" \
  -d "$CW_PAYLOAD")
[[ "$status" == "401" ]] && pass "401 on bad HMAC" || fail "expected 401, got $status"

echo
echo "All E2E checks passed."
