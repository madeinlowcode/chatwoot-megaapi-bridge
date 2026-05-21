# 14 — E2E Setup Runbook (validação real)

> Sessão de referência: WhatsApp pareado megaAPI ↔ bridge ↔ Chatwoot v4.13.0-ce
> rodando local. Tráfego bidirecional de texto confirmado funcional.
> Complementa [`scripts/e2e/RUNBOOK.md`](../scripts/e2e/RUNBOOK.md) com os
> detalhes operacionais descobertos durante a validação inicial.

Este documento descreve **como o ambiente E2E real foi montado**: a ordem dos
componentes, as decisões tomadas e os detalhes que não aparecem no
`README.md`. Para troubleshooting passo-a-passo, ver
[`15-troubleshooting.md`](./15-troubleshooting.md). Para o histórico de bugs
encontrados durante a validação, ver
[`16-postmortem-e2e-validation.md`](./16-postmortem-e2e-validation.md).

## 1. Topologia validada

```
[Celular WhatsApp]
        │
        ▼
[apistart01.megaapi.com.br]  (instance: megaapi-chatwoot, token: chatwoot)
        │
        │ HTTPS POST webhook
        ▼
[Tunnel público]  (ngrok ou Cloudflare Tunnel quick) → http://localhost:8090
        │
        ▼
[bridge container]   docker-compose, porta host 8090, postgres host 5433
        │
        │ HTTP (host.docker.internal:3000)
        ▼
[Chatwoot dev stack]  containers chatwoot-dev-*, v4.13.0-ce, porta 3000
```

Decisões de porta — **trocadas dos defaults** para evitar conflitos com outros
projetos do dev:

| Serviço | Porta host | Default original | Por quê |
|---------|-----------|-------------------|---------|
| bridge HTTP | `8090` | `8080` | `:8080` ocupado por outro projeto local |
| bridge Postgres | `5433` | `5432` | `:5432` ocupado pelo Postgres de outro projeto |
| Chatwoot HTTP | `3000` | `3000` | sem conflito |
| Chatwoot Postgres | `5434` | `5432` | namespace separado |
| Chatwoot Redis | `6380` | `6379` | namespace separado |

Configurável via `BRIDGE_HOST_PORT` / `POSTGRES_HOST_PORT` em `.env.e2e`.

## 2. Ordem de provisionamento

A ordem importa — abaixo é a sequência canônica usada pela validação:

1. **Chatwoot dev up** — `docker compose -f deploy/chatwoot.docker-compose.yml --env-file deploy/chatwoot.env up -d`
2. **Signup admin** no Chatwoot (primeiro signup vira admin enquanto `ENABLE_ACCOUNT_SIGNUP=true`)
3. **Obter `api_access_token`** do perfil do admin (Profile Settings → Access Token)
4. **Criar inbox tipo API** (não WhatsApp, não Webhook) com nome `bridge-e2e`. Anotar o id numérico da URL
5. **Habilitar `HMAC Verification`** no inbox — sem preencher secret ainda
6. **Preencher `scripts/e2e/.env.e2e`** com host/instance/token da megaAPI, token Chatwoot, inbox id, e `MASTER_KEY` (32 bytes base64)
7. **Rodar `setup.ps1`** — sobe bridge stack, migra DB, cria tenant, gera credenciais
8. **Iniciar tunnel público** (cloudflared quick ou ngrok)
9. **Colar URL do tunnel + Bearer no webhook do megaAPI dashboard**
10. **Colar URL do tunnel + HMAC secret no inbox Chatwoot**
11. **Validar** com `smoke.ps1` (CW → WA) e mensagem real do celular (WA → CW)

## 3. Componentes específicos

### 3.1 Chatwoot dev local (v4.13.0-ce)

Containers identificados no Docker Desktop por prefixo `chatwoot-dev-`:

- `chatwoot-dev-rails-1` (web)
- `chatwoot-dev-sidekiq-1` (workers)
- `chatwoot-dev-postgres-1`
- `chatwoot-dev-redis-1`

Endpoints úteis durante debug:

```bash
# Listar inbox e ver hmac_token / webhook_url cadastrados
curl -H "api_access_token: $CHATWOOT_TOKEN" \
  http://localhost:3000/api/v1/accounts/1/inboxes/<id>

# Criar contato manualmente (útil para testar 422 race)
curl -X POST -H "api_access_token: $CHATWOOT_TOKEN" \
  -H 'Content-Type: application/json' \
  http://localhost:3000/api/v1/accounts/1/contacts \
  -d '{"inbox_id":<id>,"name":"foo","phone_number":"+5511999999999","identifier":"5511999999999"}'

# Buscar contato por telefone (fallback do bridge em race de criação)
curl -H "api_access_token: $CHATWOOT_TOKEN" \
  "http://localhost:3000/api/v1/accounts/1/contacts/search?q=5511999999999"
```

### 3.2 Bridge stack

Definido em `docker-compose.yml` (raiz). Dois serviços: `bridge` (build local) +
`db` (postgres). Acessos canônicos:

```bash
# Logs do bridge
docker logs chatwoot-megaapi-bridge-bridge-1 --tail 50

# Inspecionar últimas mensagens processadas
docker compose exec -T db psql -U bridge -d bridge -c \
  "SELECT id, direction, status, external_id, last_error FROM messages ORDER BY created_at DESC LIMIT 10;"

# Inspecionar tenants (sem segredos — esses são bytea cifrados)
docker compose exec -T db psql -U bridge -d bridge -c \
  "SELECT id, slug, chatwoot_account_id, chatwoot_inbox_id FROM tenants;"
```

Health: `curl http://localhost:8090/healthz` e `/readyz`.

### 3.3 Tunnel público

Duas opções validadas — escolha pela que funcionar primeiro no seu ambiente.

**Cloudflare Tunnel (quick)**:

```bash
cloudflared tunnel --url http://localhost:8090
```

Métricas locais úteis para debug (porta varia, geralmente `20242` ou `20243`):

```bash
curl -s http://127.0.0.1:20242/metrics | grep cloudflared_tunnel_response_by_code
```

Cuidado: se o usuário já tem um named tunnel configurado em `~/.cloudflared/`,
o cred file pode interferir no roteamento do quick tunnel. Workaround usado
na validação: renomear temporariamente `~/.cloudflared` → `~/.cloudflared.bak-e2e`.

**ngrok** (substituto quando cloudflared falhou na validação):

```bash
ngrok http 8090
```

UI inspector em `http://127.0.0.1:4040` é **essencial** para diagnóstico —
mostra request raw (headers, body, query) e response. JSON em
`http://127.0.0.1:4040/api/requests/http`.

### 3.4 megaAPI

Provedor: `apistart01.megaapi.com.br`. Endpoint usado pelo bridge para envio
de texto:

```
POST {host}/rest/sendMessage/{instance}/text
Authorization: Bearer <token>
Content-Type: application/json

{
  "messageData": {
    "to": "5511999999999",
    "text": "olá",
    "isGroup": false,
    "linkPreview": false
  }
}
```

**Limitação importante descoberta**: o dashboard megaAPI não permite
configurar header customizado no webhook. O bridge agora aceita
`?token=<bearer>` como fallback na URL (ver
[bug #3 no postmortem](./16-postmortem-e2e-validation.md#3-megaapi-sem-custom-headers)).

URL final do webhook no megaAPI:

```
https://<hash>.trycloudflare.com/v1/wa/<slug>?token=<webhook_bearer>
```

## 4. Credenciais geradas pelo setup

`scripts/e2e/setup.ps1` grava credenciais em `scripts/e2e/tenant-creds.json`
(gitignored). Conteúdo:

```json
{
  "tenant_id": "<uuid>",
  "slug": "e2e-teste",
  "webhook_bearer": "<random>",
  "hmac_secret": "<random>"
}
```

Re-runs do setup detectam tenant existente e reusam esse arquivo. Se ele for
apagado mas o tenant existir no DB, o setup falha — os segredos estão
AES-GCM-cifrados na coluna `bytea` e não são recuperáveis. Solução: apagar
o tenant manualmente e refazer o setup:

```bash
docker compose exec db psql -U bridge -d bridge -c "DELETE FROM tenants WHERE slug='e2e-teste';"
rm scripts/e2e/tenant-creds.json
./scripts/e2e/setup.ps1
```

## 5. Variáveis de ambiente operacionais

| Variável | Onde vive | Para que serve |
|----------|-----------|----------------|
| `MASTER_KEY` | `docker-compose.yml` → bridge env | chave AES-256-GCM para tokens em repouso |
| `DEBUG_SKIP_HMAC` | idem | `=1` pula validação HMAC de webhook Chatwoot (apenas dev) |
| `BRIDGE_HOST_PORT` | `.env` raiz | porta host para o bridge (default `8090`) |
| `POSTGRES_HOST_PORT` | `.env` raiz | porta host para o postgres do bridge |

**Cuidado**: `setup.ps1` regrava `.env` raiz a cada execução, perdendo
overrides como `DEBUG_SKIP_HMAC` se você os tiver adicionado manualmente.
Workaround atual: re-export após cada `setup.ps1` ou editar o script para
preservar. Issue rastreada em
[postmortem #10](./16-postmortem-e2e-validation.md#10-setupps1-sobrescreve-env-raiz).

## 6. Validação de aceite

Critério mínimo (texto bidirecional):

1. Mandar mensagem do celular WhatsApp → ver aparecer em uma conversation
   nova no Chatwoot
2. Responder no Chatwoot → confirmar que chega no celular
3. `SELECT direction, status, count(*) FROM messages GROUP BY 1,2;` mostra
   2 rows `status=done`, sem `failed`

Mídia (imagem/áudio/doc) é Fase 2 — ainda não validado E2E real.

## 7. Referências

- [`scripts/e2e/RUNBOOK.md`](../scripts/e2e/RUNBOOK.md) — passo-a-passo da
  primeira execução
- [`docs/15-troubleshooting.md`](./15-troubleshooting.md) — diagnóstico
  ordenado quando algo falha
- [`docs/16-postmortem-e2e-validation.md`](./16-postmortem-e2e-validation.md)
  — histórico dos bugs encontrados e fixes aplicados nesta sessão
- [`docs/10-security.md`](./10-security.md) — modelo de segurança que o E2E
  exercita (HMAC, AES-GCM, Bearer)
