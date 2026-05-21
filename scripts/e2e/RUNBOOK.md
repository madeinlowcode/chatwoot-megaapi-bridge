# E2E Runbook — bd 96l.15 / SCR-69

Validação ponta-a-ponta REAL: WhatsApp pareado ↔ megaAPI ↔ bridge (local) ↔ Chatwoot dev (local) com tunnel público Cloudflare. Texto + mídia bidirecional.

Tempo alvo da primeira execução: **< 15 min** (depois ~3 min em re-runs).

---

## 0. Pré-requisitos

| Item | Como verificar | Como obter |
|------|----------------|------------|
| Docker Desktop rodando | `docker info` | `winget install Docker.DockerDesktop` |
| cloudflared no PATH | `cloudflared --version` | `winget install --id Cloudflare.cloudflared` |
| Chatwoot dev stack up | `docker ps --filter name=chatwoot-dev` mostra 4 containers | `docker compose -f deploy/chatwoot.docker-compose.yml --env-file deploy/chatwoot.env up -d` |
| Instância megaAPI pareada com WhatsApp | dashboard da sua instância mostra QR conectado | provedor megaAPI |
| PowerShell 5.1+ | `$PSVersionTable.PSVersion` | nativo Win11 |

Portas usadas no host (defaults — ajustáveis em `.env.e2e`):

- `:3000` Chatwoot
- `:8090` Bridge HTTP (default `BRIDGE_HOST_PORT` no exemplo evita conflito com :8080)
- `:5433` Bridge Postgres (evita conflito com :5432)
- `:6380` Chatwoot Redis (já configurado no compose)
- `:5434` Chatwoot Postgres (já configurado no compose)

---

## 1. Configurar Chatwoot (uma vez por instância dev)

### 1.1 Criar admin user

Acesse `http://localhost:3000`. Como `ENABLE_ACCOUNT_SIGNUP=true`, o primeiro signup vira admin. Anote email/senha.

### 1.2 Obter `api_access_token`

`Profile Settings` (avatar → top-right) → role abaixo até **Access Token** → copie o valor. Use no `CHATWOOT_TOKEN`.

### 1.3 Criar inbox tipo API

`Settings` → `Inboxes` → `Add Inbox` → escolha **API** (não WhatsApp, não Webhook):

- Channel name: `bridge-e2e`
- Webhook URL: _deixe vazio agora — preenchemos depois com a URL do tunnel_

Após criar, o inbox aparece na URL `/app/accounts/1/settings/inboxes/<id>`. Anote o `<id>` numérico — vai em `CHATWOOT_INBOX`.

### 1.4 Habilitar HMAC verification no inbox

Dentro do inbox criado → `Configuration` → toggle **`HMAC Verification`** ON. **Não preencha ainda o secret** — `setup.ps1` gera um e você cola no passo 5.

---

## 2. Preencher `.env.e2e`

```powershell
Copy-Item scripts\e2e\.env.e2e.example scripts\e2e\.env.e2e
notepad scripts\e2e\.env.e2e
```

Preencha **todos** os campos `replace-me-*`. Crítico:

- `MEGAAPI_HOST`/`MEGAAPI_INSTANCE`/`MEGAAPI_TOKEN`: do dashboard da sua instância
- `CHATWOOT_TOKEN`: do passo 1.2
- `CHATWOOT_INBOX`: do passo 1.3
- `MASTER_KEY`: gere com:
  ```powershell
  [Convert]::ToBase64String((1..32 | ForEach-Object { Get-Random -Maximum 256 }))
  ```

---

## 3. Subir tudo

```powershell
.\scripts\e2e\setup.ps1
```

O script:

1. Valida deps e `.env.e2e`
2. Verifica Chatwoot up
3. Sobe bridge stack (`docker compose up -d --build`)
4. Aguarda `/healthz`
5. Roda `bridge migrate`
6. Provisiona tenant (ou reusa se já existir + `tenant-creds.json` presente)
7. Inicia `cloudflared tunnel --url http://localhost:8090`
8. Imprime **bloco final** com as 2 URLs para colar nas UIs externas

Esperado no final:

```
════════════════════════════════════════════════════════════
 E2E READY — configure os webhooks abaixo nas UIs externas
════════════════════════════════════════════════════════════

 [1] megaAPI dashboard (instance <ID>)
     URL:    https://abc123.trycloudflare.com/v1/wa/e2e-teste
     Header: Authorization: Bearer <bearer>
     Eventos: message, message.upsert (whatever sua versão expõe)

 [2] Chatwoot Inbox <N> → Settings → Integrations → Webhooks
     URL:         https://abc123.trycloudflare.com/v1/cw/e2e-teste
     HMAC Secret: <secret>
     (cole o secret em Inbox → Configuration → 'HMAC Verification')
```

---

## 4. Configurar webhook no megaAPI

No dashboard da sua instância megaAPI:

1. Vá em **Webhooks** (ou equivalente da sua versão)
2. Cole `https://<hash>.trycloudflare.com/v1/wa/<TENANT_SLUG>`
3. **Header de autenticação**: muitas versões do megaAPI exigem header custom:
   - Nome: `Authorization`
   - Valor: `Bearer <webhook_bearer_do_setup>`
   - Se sua versão não suportar header custom, hospede num provider que faça mTLS ou rode bridge atrás de proxy que injeta o header. Fallback: contate suporte megaAPI.
4. Salve. Algumas versões testam o webhook na hora — confira logs com `watch.ps1` (espera-se 200 OK).

---

## 5. Configurar webhook + HMAC no Chatwoot Inbox

No Inbox criado no passo 1.3:

1. `Settings` → `Inboxes` → `bridge-e2e` → `Configuration`
2. **HMAC Verification**: cole `<HMAC Secret>` impresso pelo setup
3. **Webhook URL**: aba `Configuration` ou no topo — cole `https://<hash>.trycloudflare.com/v1/cw/<TENANT_SLUG>`
4. Salve

> Importante: o HMAC no Chatwoot v4.13 é calculado com `api_access_token` do agente que envia a mensagem, mas o secret cadastrado aqui é o que o bridge valida em `X-Chatwoot-Signature`. Bridge espera HMAC-SHA256 hex do body com o secret. Se você ver `signature mismatch` no log do bridge, **o secret no Inbox está diferente do que o bridge tem encriptado em DB** — recrie o tenant.

---

## 6. Observar tráfego em tempo real

Abra **outro** terminal:

```powershell
.\scripts\e2e\watch.ps1
```

Isto abre uma janela com `docker compose logs -f bridge` e mostra polling da tabela `messages` a cada 2s.

---

## 7. Teste de fumaça automatizado (Chatwoot → WhatsApp)

```powershell
.\scripts\e2e\smoke.ps1 -Phone 5511999999999 -Text "ping e2e"
```

Substitua pelo seu número WhatsApp (E.164 sem `+`). Esperado:

```
[OK] SMOKE PASS — message <id> entregue (status=done)
```

Códigos de saída:

- `0` SUCCESS — bridge processou e marcou done
- `2` FAIL — bridge marcou `failed` (veja `last_error`)
- `3` TIMEOUT — message ficou `pending` > 15s (worker travado? megaAPI down?)
- `4` Bridge nunca recebeu o webhook (URL/HMAC errado no Inbox)

Confira no celular que a mensagem chegou no WhatsApp.

---

## 8. Teste manual completo (critério de aceite)

Capture **4 screenshots**:

1. **WA→CW texto**: mande "olá teste" do seu celular WhatsApp → veja aparecer na conversation Chatwoot
2. **CW→WA texto**: responda no Chatwoot → confirme chegou no celular
3. **WA→CW mídia**: mande uma foto do celular → veja aparecer como anexo na conversation Chatwoot
4. **CW→WA mídia**: anexe uma imagem no Chatwoot e envie → confirme chegou no WhatsApp

Em paralelo, abra `watch.ps1` e confirme `count` na tabela `messages`:

```sql
SELECT direction, status, count(*) FROM messages
WHERE tenant_id = (SELECT id FROM tenants WHERE slug='e2e-teste')
GROUP BY 1,2;
```

Esperado: 4 rows com `status=done` (2 in, 2 out).

---

## 9. Teardown

```powershell
.\scripts\e2e\teardown.ps1            # só para o cloudflared
.\scripts\e2e\teardown.ps1 -Full      # também derruba bridge stack (mantém DB)
.\scripts\e2e\teardown.ps1 -PurgeData # apaga TUDO (volume + tenant-creds.json)
```

Chatwoot dev fica up independentemente. Para parar: `docker compose -f deploy/chatwoot.docker-compose.yml down`.

---

## Troubleshooting

### Tunnel não emite URL em 30s

- Cloudflared bloqueado por firewall corporativo? Tente `cloudflared tunnel --url http://localhost:8090 --protocol http2`
- Veja `scripts/e2e/cloudflared.log` e `cloudflared.log.err`

### megaAPI retorna 404 ao testar webhook

- URL termina com `/v1/wa/<slug>` exato (sem trailing slash)
- Slug está correto e bate com `TENANT_SLUG`
- `curl https://<hash>.trycloudflare.com/healthz` direto retorna `200`?

### Chatwoot webhook retorna 401 (signature mismatch) no log do bridge

- HMAC secret cadastrado no Inbox **diferente** do gerado pelo setup
- Apague tenant + tenant-creds.json, rode setup novamente, cole o **novo** secret no Inbox:
  ```powershell
  docker compose exec db psql -U bridge -d bridge -c "DELETE FROM tenants WHERE slug='e2e-teste';"
  Remove-Item scripts\e2e\tenant-creds.json
  .\scripts\e2e\setup.ps1
  ```

### Bridge não alcança Chatwoot (`chatwoot-url unreachable`)

- O setup usa `--skip-reach-check` mas o **bridge em runtime** ainda precisa alcançar. Em Docker Desktop Windows, `host.docker.internal:3000` funciona out-of-the-box. Se você desabilitou DNS interno, use IP do host na LAN: `http://192.168.x.y:3000`.

### Porta 8090 ou 5433 já em uso

- Edite `BRIDGE_HOST_PORT` / `POSTGRES_HOST_PORT` no `.env.e2e` e re-rode `setup.ps1`. Cuide pra refazer `docker compose down && up` se já tiver subido com porta antiga.

### Smoke retorna exit 4 (webhook não chegou)

- URL do webhook no Inbox tem typo
- Tunnel caiu (`Get-Process cloudflared` deve listar o processo)
- Re-rode `setup.ps1` — nova URL será gerada (Cloudflare quick tunnels são efêmeras)

### messages preso em `pending` por minutos

- Worker travado: olhar `docker compose logs bridge` por panic / nil pointer
- megaAPI lenta / token revogado: tenta `curl -H "Authorization: Bearer $MEGAAPI_TOKEN" $MEGAAPI_HOST/rest/instance/$MEGAAPI_INSTANCE`

### Re-runs e idempotência

- `setup.ps1` detecta tenant existente via DB e reusa creds de `tenant-creds.json`
- Se `tenant-creds.json` foi apagado mas tenant existe na DB, setup falha (creds criptografadas, não recuperáveis) — apague o tenant manualmente conforme troubleshooting de HMAC mismatch
