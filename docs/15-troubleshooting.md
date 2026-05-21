# 15 — Troubleshooting Runbook

Diagnóstico ordenado para falhas no fluxo E2E. Vai do **mais externo** (provider
megaAPI, Chatwoot, tunnel) ao **mais interno** (bridge HTTP, worker, DB).
Sempre desça a árvore — não vale começar pelo bridge se nem o tunnel chegou.

Complementa o [`scripts/e2e/RUNBOOK.md`](../scripts/e2e/RUNBOOK.md) seção
Troubleshooting (resumo) com a árvore completa.

## Árvore de diagnóstico

```
mensagem não chega
  ├─ 1. tunnel está vivo?
  ├─ 2. webhook está cadastrado certo (URL + auth) no provedor?
  ├─ 3. bridge está respondendo (healthz/readyz)?
  ├─ 4. bridge autenticou o request (não 401)?
  ├─ 5. bridge persistiu a mensagem em DB?
  ├─ 6. worker processou (status=done) ou marcou failed?
  └─ 7. provider de destino aceitou (megaAPI/Chatwoot)?
```

A cada degrau, há um comando para confirmar e um sintoma típico.

---

## 1. Tunnel

### Sintoma: webhook nunca chega no bridge

```bash
# cloudflared quick — métricas locais
curl -s http://127.0.0.1:20242/metrics | grep cloudflared_tunnel_response_by_code

# ngrok — inspector
curl -s http://127.0.0.1:4040/api/requests/http | jq '.requests[0]'

# Healthz via tunnel
curl https://<hash>.trycloudflare.com/healthz
```

Resultados esperados:

- métricas mostram requests entrando e responses 200
- inspector mostra a request raw com Body + Headers
- `/healthz` retorna `{"status":"ok"}`

Falhas típicas:

| Sintoma | Causa provável | Fix |
|---------|----------------|-----|
| `cloudflared_tunnel_response_by_code{status_code="404"}` | Cred file user em `~/.cloudflared/` colidindo com quick tunnel | Renomear `~/.cloudflared` → `~/.cloudflared.bak-e2e` e reiniciar |
| Tunnel não emite URL em 30s | Firewall bloqueando | Tentar `--protocol http2` ou trocar por ngrok |
| ngrok dá `ERR_NGROK_*` | Token / plano | `ngrok config add-authtoken <t>` |
| URL caiu (cloudflared morreu) | Process kill / boot reciclou | Re-rodar setup ou `cloudflared tunnel --url ...` manual |

## 2. Webhook cadastrado no provedor

### 2.1 megaAPI

No dashboard da instância — confirme:

- URL completa exata: `https://<hash>.trycloudflare.com/v1/wa/<slug>?token=<bearer>`
- Sem trailing slash
- Slug bate com `tenants.slug` no DB
- megaAPI **não permite header custom**, então o Bearer **tem que estar na
  query string** (`?token=`). Ver
  [postmortem #3](./16-postmortem-e2e-validation.md#3-megaapi-sem-custom-headers).

Teste manual de chegada:

```bash
curl -X POST "https://<hash>.trycloudflare.com/v1/wa/<slug>?token=<bearer>" \
  -H 'Content-Type: application/json' \
  -d '{"messages":[]}'
```

Esperado: 200 ou 400 (payload incompleto), **não** 401.

### 2.2 Chatwoot

Inbox → Configuration:

- Webhook URL: `https://<hash>.trycloudflare.com/v1/cw/<slug>`
- HMAC Verification: ON
- HMAC Secret: tem que ser **o mesmo** que foi gerado pelo setup e guardado em
  `tenant-creds.json` / cifrado no DB bridge. Mismatch é a causa #1 de 401
  CW→bridge. Ver [postmortem #7](./16-postmortem-e2e-validation.md#7-hmac-mismatch).

Inspeção via API:

```bash
curl -H "api_access_token: $CHATWOOT_TOKEN" \
  http://localhost:3000/api/v1/accounts/1/inboxes/<id> | jq '.hmac_token, .webhook_url'
```

## 3. Bridge HTTP

### Sintoma: tunnel chega no bridge mas algo dá errado

```bash
curl http://localhost:8090/healthz   # liveness
curl http://localhost:8090/readyz    # readiness (queue, DB)
docker logs chatwoot-megaapi-bridge-bridge-1 --tail 50
```

Falhas típicas:

| Sintoma | Causa | Fix |
|---------|-------|-----|
| `/healthz` 200, `/readyz` 503 | queue cheia ou DB down | ver logs, considerar `BUFFER_LIMIT` maior |
| `Cannot connect` em qualquer | container caiu | `docker compose up -d` |
| Bridge sobe e morre | migrate falhou ou MASTER_KEY inválida | logs do container |

## 4. Autenticação inbound (Bearer / HMAC)

### Sintoma: bridge responde 401 nos logs

Após patches desta sessão, ambos os handlers emitem log diagnóstico antes do
401. Procure por:

```
WA webhook unauthorized — diagnostic
  has_auth_header: false
  query_token_len: 0
```

Decisão:

- `has_auth_header=false` e `query_token_len=0` → provedor não está mandando
  o token (cheque step 2)
- `query_token_len>0` mas 401 → token diferente do cifrado no DB. Confronte
  com `tenant-creds.json`. Se sumiu, recriar tenant.

Para Chatwoot HMAC:

- Header esperado `X-Chatwoot-Signature` (sem prefixo `sha256=`; bridge faz
  strip — ver
  [postmortem #7](./16-postmortem-e2e-validation.md#7-hmac-mismatch))
- Algoritmo: HMAC-SHA256 hex do body cru
- **Bypass de emergência**: `DEBUG_SKIP_HMAC=1` no env do container faz o
  bridge logar `WARN` e processar mesmo assim. **Apenas dev.**

Comparação manual de HMAC (PowerShell):

```powershell
$secret = '<hmac-secret-tenant>'
$body = Get-Content -Raw -Path body.json  # exportado do inspector ngrok
$hmac = [System.Security.Cryptography.HMACSHA256]::new([System.Text.Encoding]::UTF8.GetBytes($secret))
$sig = [BitConverter]::ToString($hmac.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($body))).Replace('-','').ToLower()
$sig  # compare com X-Chatwoot-Signature
```

## 5. Persistência

### Sintoma: bridge aceitou (200) mas mensagem não aparece em `messages`

```bash
docker compose exec -T db psql -U bridge -d bridge -c \
  "SELECT id, direction, status, external_id, created_at FROM messages ORDER BY created_at DESC LIMIT 20;"
```

Casos possíveis:

- **Não está lá** → handler retornou 200 mas o payload caiu em `chatwootShouldRelay` false / formato inesperado. Ver logs `event ignored`.
- **Lá com `external_id` repetido** → idempotência funcionou (UNIQUE
  `(tenant_id, direction, external_id)`). Mesma mensagem mandada 2×. Normal.

## 6. Worker

### Sintoma: linhas com `status='pending'` ou `'failed'`

```bash
docker compose exec -T db psql -U bridge -d bridge -c \
  "SELECT id, direction, status, attempts, last_error FROM messages WHERE status IN ('pending','failed') ORDER BY created_at DESC LIMIT 20;"
```

| `status` | `last_error` exemplo | Causa | Ação |
|----------|---------------------|-------|------|
| `pending` >30s | (vazio) | worker travado | logs procurando panic/nil; restart bridge |
| `failed` | `cipher: message authentication failed` | MASTER_KEY trocada vs DB legado | `DELETE FROM messages WHERE created_at < '...';` ([postmortem #1](./16-postmortem-e2e-validation.md#1-cipher-message-authentication-failed)) |
| `failed` | `megaAPI 401 UNAUTHORIZED` | token errado no tenant | recriar tenant com token correto ([postmortem #9](./16-postmortem-e2e-validation.md#9-megaapi-401-token-errado)) |
| `failed` | `x509: certificate signed by unknown authority` | CA bundle ausente na imagem | `apk add ca-certificates` no Dockerfile ([postmortem #8](./16-postmortem-e2e-validation.md#8-tls-x509-unknown-authority)) |
| `failed` | `422 already been taken` | race de criação de contato | já mitigado por `cwFindContactByPhone` fallback ([postmortem #6](./16-postmortem-e2e-validation.md#6-inbound-race-condition-de-contato)) |

## 7. Provider de destino

### Sintoma: bridge marca `done` mas mensagem não chega no celular / Chatwoot

Para megaAPI:

```bash
# Status da instância
curl -H "Authorization: Bearer $MEGAAPI_TOKEN" \
  "$MEGAAPI_HOST/rest/instance/$MEGAAPI_INSTANCE"

# Enviar texto manualmente
curl -X POST -H "Authorization: Bearer $MEGAAPI_TOKEN" \
  -H 'Content-Type: application/json' \
  "$MEGAAPI_HOST/rest/sendMessage/$MEGAAPI_INSTANCE/text" \
  -d '{"messageData":{"to":"5511999999999","text":"teste","isGroup":false,"linkPreview":false}}'
```

Se manual funciona mas bridge falha: comparar payload exato no log do bridge
com o curl que funcionou.

Para Chatwoot: ver se a conversation existe e está no estado certo:

```bash
curl -H "api_access_token: $CHATWOOT_TOKEN" \
  "http://localhost:3000/api/v1/accounts/1/conversations" | jq '.data.payload[0]'
```

---

## Checklist rápido (5 min)

Use quando algo quebrar e você quer triagem rápida antes de cavar:

```bash
# 1. Tunnel vivo?
curl -fsS https://<hash>.trycloudflare.com/healthz

# 2. Bridge respondendo?
curl -fsS http://localhost:8090/healthz
curl -fsS http://localhost:8090/readyz

# 3. Última mensagem processada?
docker compose exec -T db psql -U bridge -d bridge -c \
  "SELECT direction, status, last_error, created_at FROM messages ORDER BY created_at DESC LIMIT 5;"

# 4. Erros nos logs (últimos 5 min)?
docker logs chatwoot-megaapi-bridge-bridge-1 --since 5m 2>&1 | grep -i -E 'error|warn|panic'

# 5. Chatwoot up?
curl -fsS -o /dev/null -w '%{http_code}\n' http://localhost:3000

# 6. megaAPI alcançável e instância pareada?
curl -fsS -H "Authorization: Bearer $MEGAAPI_TOKEN" \
  "$MEGAAPI_HOST/rest/instance/$MEGAAPI_INSTANCE"
```

Falhou em algum passo → seção correspondente acima.

## Limpeza de estado quebrado

Quando o ambiente está num estado inconsistente e re-runs não convergem:

```bash
# Apagar todas as mensagens (preserva tenants)
docker compose exec -T db psql -U bridge -d bridge -c "TRUNCATE messages;"

# Apagar tudo (vai forçar setup novo)
docker compose exec -T db psql -U bridge -d bridge -c "TRUNCATE tenants CASCADE;"
rm scripts/e2e/tenant-creds.json

# Recomeçar
./scripts/e2e/setup.ps1
```

## Onde olhar quando nada mais funciona

- Logs bridge: `docker logs chatwoot-megaapi-bridge-bridge-1 --tail 200`
- Logs Chatwoot: `docker logs chatwoot-dev-rails-1 --tail 100`
- Logs cloudflared: `scripts/e2e/cloudflared.log` + `.err`
- Logs ngrok: `scripts/e2e/ngrok.log` + `.err` + `http://127.0.0.1:4040`
- DB bridge: `docker compose exec db psql -U bridge -d bridge`
- DB Chatwoot: `docker exec -it chatwoot-dev-postgres-1 psql -U postgres -d chatwoot_dev`
