# 09 — UI Admin

## Filosofia de UX

Usuário leigo. Cada decisão deve eliminar fricção:
- Zero edição de arquivos depois do install.
- Botões de "testar conexão" antes de salvar.
- Auto-discovery de inboxes Chatwoot via dropdown.
- Auto-registro de webhooks (1 clique).
- Diagnóstico embutido com mensagens em português, acionáveis.
- Idioma: PT-BR primário, EN como segundo.

## Stack frontend

- **HTML server-rendered** via `html/template` (Go).
- **htmx** para interatividade (form submit parcial, polling status).
- **alpine.js** para estado leve no cliente (toggles, modais).
- **Tailwind CSS** via CDN no MVP (depois compilado no embed).
- **Sem build step JS.** Tudo via `embed.FS` no binário.

Total CSS+JS: ~50 KB.

## Mapa de telas

```
/admin/login                       → autenticação
/admin                             → dashboard global
/admin/tenants                     → lista
/admin/tenants/new                 → wizard 4 passos
/admin/tenants/{slug}              → dashboard do tenant
/admin/tenants/{slug}/messages     → log paginado
/admin/tenants/{slug}/diagnose     → checklist live
/admin/tenants/{slug}/edit         → editar config
/admin/dlq                         → fila de mortas
/admin/settings                    → admin user, master key, alerts
```

## Wizard "Novo Tenant" — fluxo

### Passo 1 — Identificação
```
Nome do tenant: [____________________]
Slug:           [auto-gerado, editável]
                  (usado em URL: /v1/wa/<slug>)
```

### Passo 2 — megaAPI
```
Host megaAPI:  [https://apibusiness1.megaapi.com.br]
                ▼ ou escolha de presets:
                  - apibusiness1.megaapi.com.br
                  - apibusiness7.megaapi.com.br
                  - Outro (digitar)
Instance Key:  [________________________]
Bearer Token:  [••••••••••••••••••••••]

[ Testar conexão ]  ← chama GET /rest/instance/{key}/me
```

Resposta inline:
- ✅ "Conectado. Número: +55 11 9..."
- ❌ "Falha: 401 Unauthorized — verifique token"

### Passo 3 — Chatwoot
```
URL Chatwoot:  [https://atendimento.empresa.com] (auto-preenche com domínio)
API Token:     [••••••••••••••••••••••]
                (Configurações → Perfil → Access Token)

[ Conectar ]
```

Após conectar:
```
Account:       [▼ dropdown carregado via API]
Inbox:         [▼ dropdown filtra apenas API Channels]
                
                Não tem API Channel? [+ Criar agora]
```

### Passo 4 — Webhooks (auto)
```
Pronto pra finalizar:

  ☑ Vai registrar webhook na megaAPI:
    https://atendimento.empresa.com/v1/wa/cliente-x

  ☑ Vai registrar webhook outgoing no Chatwoot:
    https://atendimento.empresa.com/v1/cw/cliente-x

  ☑ Vai gerar HMAC secret automaticamente

[ Voltar ]                            [ Criar tenant ]
```

Após submit:
- Persist no DB.
- Chama megaAPI `configWebhook`.
- Chama Chatwoot `POST /webhooks` na inbox.
- Redireciona pra dashboard do tenant.

## Dashboard do tenant

```
┌────────────────────────────────────────────────────┐
│ Cliente X                          [Editar] [DEL]  │
│ Slug: cliente-x                                    │
│                                                    │
│ Status: ✅ Operacional                              │
│ Última msg recebida: há 3 min                      │
│ Última msg enviada:  há 1 min                      │
│                                                    │
│ ┌─────────────────┬─────────────────────────────┐ │
│ │ Recebidas (24h) │ Enviadas (24h)              │ │
│ │      342        │       198                   │ │
│ ├─────────────────┼─────────────────────────────┤ │
│ │ Falhas (24h)    │ Fila atual                  │ │
│ │       2         │        0                    │ │
│ └─────────────────┴─────────────────────────────┘ │
│                                                    │
│ [Sparkline 24h: msgs/h por direção]                │
│                                                    │
│ Ações:                                             │
│ [ Diagnosticar ] [ Ver mensagens ] [ Testar envio ]│
└────────────────────────────────────────────────────┘
```

## Diagnóstico (1 clique)

`/admin/tenants/{slug}/diagnose` roda checklist:

```
✅ Conectividade megaAPI (ping /me)         ← 87 ms
✅ Webhook registrado na megaAPI            ← URL correta
✅ Conectividade Chatwoot                   ← 145 ms
✅ Token Chatwoot válido                    ← scope OK
✅ Inbox API Channel acessível
✅ Webhook outgoing registrado no Chatwoot
⚠️  HMAC secret não validado nas últimas 24h ← nenhuma msg outbound recente
✅ Fila Redis acessível
✅ Última mensagem inbound há 3 min

Tudo certo. Se mensagens não chegam ao WhatsApp:
  1. Confira se o número WhatsApp está conectado na megaAPI
     [Abrir painel megaAPI]
  2. Tente um envio teste:
     [ Enviar teste para 5511999999999 ]
```

Cada linha é executada em paralelo por goroutines, atualiza a UI via htmx.

## Log de mensagens (`/admin/tenants/{slug}/messages`)

Tabela paginada:

| Hora | Direção | Contato | Conteúdo (preview) | Status | Ações |
|------|---------|---------|---------------------|--------|-------|
| 14:32 | ⬇ in  | +5511… | "Olá, gostaria…" | ✅ delivered | [Detalhes] |
| 14:30 | ⬆ out | +5511… | "Bom dia!" | ❌ failed | [Retry] |

Filtros: direção, status, intervalo de tempo, número.

[Detalhes] abre drawer com payload original + erros.

## DLQ (`/admin/dlq`)

Lista global. Mesma tabela + filtro por tenant.

Ações em massa:
- Selecionar N → [Retry] / [Discard] / [Mark resolved]

## Configurações globais (`/admin/settings`)

- Alterar senha admin.
- Adicionar admins extras (multi-user opcional).
- Configurar webhook de alerta (Slack/Discord/email).
- Rotacionar master key (procedimento documentado).
- Backup manual (botão "Baixar dump").

## Autenticação

- Argon2id para senha.
- Sessão cookie HttpOnly + SameSite=Lax + secure.
- CSRF token em todos os POSTs.
- Brute-force protection: 5 tentativas em 15 min trava IP por 1h.

## Acessibilidade

- WAI-ARIA labels em todos os controles.
- Contrastes WCAG AA.
- Keyboard navigation completa.
- Sem dependência de JS para fluxos críticos (form submit funciona sem JS).

## i18n

- Estrutura `internal/ui/locales/{pt-br,en}.json`.
- PT-BR completo desde MVP.
- EN no roadmap pré-1.0.
