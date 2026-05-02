# 06 — API e Protocolos

## Endpoints expostos pelo bridge

### Webhooks (públicos, autenticados por origem)

#### `POST /v1/wa/{tenant_slug}`
Recebe webhook da megaAPI.

- **Auth:** `Authorization: Bearer <token>` configurado no momento de
  registrar o webhook (token gerado pelo bridge por tenant).
- **Body:** payload megaAPI (ver mapping abaixo).
- **Resposta:** `200 OK` em ≤10 ms p99. Sem body.
- **Erros:** `401` token inválido, `404` tenant não existe, `429` rate limit
  excedido, `503` se queue cheia (raríssimo).

#### `POST /v1/cw/{tenant_slug}`
Recebe webhook do Chatwoot.

- **Auth:** header `X-Chatwoot-Signature` com HMAC-SHA256 sobre o body,
  assinado pelo `hmac_secret` do tenant.
- **Body:** payload Chatwoot.
- **Resposta:** `200 OK`. Sem body.
- **Filtragem:** processa apenas `event=message_created` com
  `message_type=outgoing` e `private=false`.

### Saúde

- `GET /healthz` — liveness. Sempre 200 se processo rodando.
- `GET /readyz` — readiness. Checa conexão Postgres + Redis. Retorna 503 se
  qualquer dependência indisponível.
- `GET /metrics` — Prometheus. Restrito a IPs locais ou auth básico.

### Admin (autenticado)

Prefixo: `/admin/`. Sessão cookie HttpOnly + CSRF token.

- `GET  /admin/login`, `POST /admin/login`
- `GET  /admin/tenants`
- `GET  /admin/tenants/new`
- `POST /admin/tenants` — cria tenant (form multipart)
- `GET  /admin/tenants/{slug}` — dashboard
- `POST /admin/tenants/{slug}/test-megaapi`
- `POST /admin/tenants/{slug}/test-chatwoot`
- `POST /admin/tenants/{slug}/setup-webhooks` — auto-registra
- `POST /admin/tenants/{slug}/disable`
- `DELETE /admin/tenants/{slug}`
- `GET  /admin/tenants/{slug}/messages` — lista paginada
- `GET  /admin/dlq` — fila de mortas

## Contratos megaAPI

Documentação base: `https://doc.mega-api.app.br/`

### Configurar webhook
```
POST {host}/rest/webhook/{instance_key}/configWebhook
Authorization: Bearer {megaapi_token}
Content-Type: application/json

{
  "messageData": {
    "webhookUrl": "https://dominio/v1/wa/{tenant_slug}",
    "webhookEnabled": true
  }
}
```

### Enviar texto
```
POST {host}/rest/sendMessage/{instance_key}/text
Authorization: Bearer {megaapi_token}
Content-Type: application/json

{
  "messageData": {
    "to": "5511999999999@s.whatsapp.net",
    "text": "Olá!"
  }
}
```

### Enviar mídia
```
POST {host}/rest/sendMessage/{instance_key}/mediaUrl
{
  "messageData": {
    "to": "...",
    "mediaUrl": "https://...",
    "type": "image" | "video" | "audio" | "document",
    "caption": "opcional",
    "fileName": "opcional"
  }
}
```

### Webhook recebido (estrutura típica)
```json
{
  "instance_key": "abc123",
  "messages": [
    {
      "key": { "id": "WA_MSG_ID", "remoteJid": "5511...@s.whatsapp.net", "fromMe": false },
      "message": {
        "conversation": "texto" 
        // ou imageMessage / audioMessage / documentMessage / videoMessage com .url e .mimetype
      },
      "messageTimestamp": 1714500000,
      "pushName": "Nome do contato"
    }
  ]
}
```

> Schema exato conferir em `apibusiness1.megaapi.com.br/docs/`. Bridge implementa
> parser tolerante (ignora campos desconhecidos).

## Contratos Chatwoot

Documentação base: `https://www.chatwoot.com/developers/api/`

### Resolver/criar contato
```
POST {base}/api/v1/accounts/{aid}/contacts/search?q={phone}
api_access_token: {token}
```
Se vazio:
```
POST {base}/api/v1/accounts/{aid}/contacts
{
  "inbox_id": {inbox_id},
  "name": "{pushName}",
  "phone_number": "+55119...",
  "identifier": "{wa_jid}"
}
```

### Resolver/criar conversation (API Channel)
```
POST {base}/api/v1/accounts/{aid}/conversations
{
  "source_id": "{wa_jid}",
  "inbox_id": {inbox_id},
  "contact_id": {cw_contact_id},
  "status": "open"
}
```

### Postar mensagem (inbound, do contato)
```
POST {base}/api/v1/accounts/{aid}/conversations/{cid}/messages
{
  "content": "Olá!",
  "message_type": "incoming",
  "content_attributes": { "external_id": "WA_MSG_ID" },
  "private": false,
  "attachments": [ ... ]
}
```

### Webhook outgoing recebido
```json
{
  "event": "message_created",
  "id": 12345,
  "content": "Resposta do atendente",
  "message_type": "outgoing",
  "private": false,
  "conversation": {
    "id": 678,
    "contact_inbox": { "source_id": "5511...@s.whatsapp.net" }
  },
  "sender": { "type": "user", "id": 1 }
}
```

## Mapping (resumo)

| megaAPI → Chatwoot | Campo |
|---------------------|-------|
| `key.remoteJid` | `contacts.phone_number` (parse) e `source_id` |
| `pushName` | `contacts.name` |
| `message.conversation` | `messages.content` |
| `message.imageMessage.url` | `attachments[].file_url` |
| `key.id` | `content_attributes.external_id` |

| Chatwoot → megaAPI | Campo |
|---------------------|-------|
| `conversation.contact_inbox.source_id` | `messageData.to` |
| `content` | `messageData.text` |
| `attachments[]` | múltiplas chamadas mediaUrl |

## Tipos de conteúdo suportados (MVP)

| Tipo | WA→CW | CW→WA |
|------|-------|-------|
| Texto | ✅ | ✅ |
| Imagem | ✅ | ✅ |
| Áudio (PTT) | ✅ | ✅ |
| Vídeo | ✅ | ⚠️ (limite tamanho megaAPI) |
| Documento | ✅ | ✅ |
| Localização | ⚠️ v2 | ⚠️ v2 |
| Contato (vCard) | ⚠️ v2 | ⚠️ v2 |
| Sticker | ⚠️ v2 | ⚠️ v2 |
| Reações | ❌ | ❌ |

## Versionamento da API do bridge

- Prefix `/v1/` fixo.
- Quebras de contrato → `/v2/` com período de coexistência.

## Códigos de erro padronizados

```json
{ "error": "tenant_not_found", "message": "...", "request_id": "uuid" }
```

| Código | Significado |
|--------|-------------|
| `tenant_not_found` | slug inexistente ou desabilitado |
| `auth_invalid` | token/HMAC inválido |
| `rate_limited` | rate limit excedido |
| `payload_invalid` | body não parseável |
| `dependency_unavailable` | PG/Redis fora |
| `queue_full` | fila acima do threshold |
