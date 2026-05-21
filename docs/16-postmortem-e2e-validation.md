# 16 — Postmortem: Validação E2E Real

> Sessão de validação end-to-end com WhatsApp pareado real (megaAPI) e
> Chatwoot v4.13.0-ce dev local. Resultado: **tráfego bidirecional de texto
> confirmado funcional**. Sessão produziu 10 bugs catalogados + fixes aplicados
> + 1 dívida técnica conhecida (HMAC bypass).

Este documento registra **o que quebrou, por que quebrou, e como foi
corrigido**, na ordem em que cada bug apareceu durante o debug. Serve como
referência futura e como base para [`15-troubleshooting.md`](./15-troubleshooting.md).

## Sumário

| # | Bug | Severidade | Status |
|---|-----|-----------|--------|
| 1 | `cipher: message authentication failed` em rows antigas | Média | Fix: limpar rows legadas |
| 2 | Cloudflare Tunnel 404 por cred file user | Alta | Fix: rename `~/.cloudflared` |
| 3 | megaAPI não permite header custom no webhook | Alta | Fix: bridge aceita `?token=` |
| 4 | `cloudflared` kill permission denied | Baixa | Fix: try/catch + best-effort |
| 5 | PowerShell 5.1 `2>&1` em native cmd dá ErrorRecord | Média | Fix: `*>&1` + `$ErrorActionPreference='Continue'` |
| 6 | INBOUND race condition de criação de contato | Alta | Fix: fallback search no 422 |
| 7 | HMAC mismatch persistente CW→bridge | Alta | Workaround: `DEBUG_SKIP_HMAC=1`; dívida técnica registrada |
| 8 | TLS x509 unknown authority no container scratch | Alta | Fix: `apk add ca-certificates` + COPY |
| 9 | megaAPI 401 — token errado no `.env.e2e` | Média | Fix: corrigir + recriar tenant |
| 10 | `setup.ps1` sobrescreve `.env` raiz | Baixa | Pendente — workaround documentado |

---

## 1. `cipher: message authentication failed`

**Sintoma**: bridge subiu, todas as mensagens antigas marcadas `failed` com
esse erro em `last_error`.

**Root cause**: 300k+ rows leftover do load test SCR-72 estavam encriptadas
com a `MASTER_KEY` anterior. Ao trocar a key (esperado em ambiente novo), o
AES-GCM rejeita o ciphertext — o tag de autenticação não bate. Worker
re-tentando essas rows no boot via `RecoverPending`.

**Fix**: limpar rows legadas que não são mais úteis:

```sql
DELETE FROM messages WHERE external_id LIKE 'run-%';
```

**Lição**: trocar `MASTER_KEY` invalida tudo que está cifrado. Em produção,
a master key é eterna; rotação é via `kid` (ver `docs/10-security.md`).

---

## 2. Cloudflare Tunnel 404

**Sintoma**: `cloudflared tunnel --url http://localhost:8090` sobe, emite
URL `<hash>.trycloudflare.com`. `curl` na URL → 404 do edge Cloudflare. Bridge
nunca recebia o request.

**Root cause**: o usuário tinha `~/.cloudflared/<id>.json` (named tunnel
preexistente). O daemon lê o cred file e tentou rotear como named em vez de
quick — conflito interno, edge não roteava.

**Fix**: renomear o diretório temporariamente:

```powershell
Rename-Item "$HOME\.cloudflared" "$HOME\.cloudflared.bak-e2e"
```

**Lição**: cloudflared mistura state global. Em dev, ngrok foi mais
confiável (URL diferente, mas sem ambiguidade).

---

## 3. megaAPI sem custom headers

**Sintoma**: bridge respondia 401 a todo POST do megaAPI. Logs sem
`Authorization` header.

**Root cause**: o dashboard megaAPI não tem campo para configurar header
custom de autenticação no webhook. Só URL.

**Fix**: bridge agora aceita o Bearer via `?token=` na query string além do
header. Trecho do patch em `internal/bridge/server.go`:

```go
func (s *Server) checkBearer(r *http.Request, t Tenant) (bool, error) {
    got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
    if got == "" {
        got = r.URL.Query().Get("token")
    }
    ...
}
```

URL final no dashboard megaAPI:

```
https://<hash>.trycloudflare.com/v1/wa/<slug>?token=<webhook_bearer>
```

Comparação ainda em tempo constante (`subtle.ConstantTimeCompare`).

**Lição**: APIs WhatsApp não-oficiais variam muito em quê suportam de auth.
A query string como fallback é compromisso aceitável (HTTPS protege o
trânsito; o token vaza pra logs do edge, mas isso já valia pra header).

---

## 4. `cloudflared` kill permission denied

**Sintoma**: `setup.ps1` falhava ao tentar matar `cloudflared.exe` rodando
antes de subir o novo.

**Root cause**: o usuário tinha cloudflared instalado como Windows service
rodando como SYSTEM. PowerShell sem admin não mata processos SYSTEM.

**Fix**: o setup agora trata como best-effort — se falha, continua e sobe
outra instância em paralelo (cloudflared permite múltiplas):

```powershell
try {
    $existingTunnel | Stop-Process -Force -ErrorAction Stop
} catch {
    Write-Warn2 "Não consegui encerrar cloudflared existente (provavelmente service/admin). Iniciando novo quick tunnel em paralelo — múltiplas instâncias coexistem."
}
```

---

## 5. PowerShell 5.1 `2>&1` em native cmd

**Sintoma**: `setup.ps1` reportava falha em comandos `docker compose ...` que
na verdade tinham sucesso (exit 0).

**Root cause**: PowerShell 5.1 ao redirecionar stderr de native executable
com `2>&1` wrappa cada linha de stderr em ErrorRecord (`NativeCommandError`)
e força `$?` para `$false`. Docker escreve progresso de build em stderr —
todos viravam erros falsos.

**Fix**: substituir `2>&1` por `*>&1` (merge total) **e** salvar/restaurar
`$ErrorActionPreference='Continue'` em volta da chamada:

```powershell
$prevEAP = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
& docker compose up -d --build *>&1 | ForEach-Object { Write-Host "    $_" }
$rc = $LASTEXITCODE
$ErrorActionPreference = $prevEAP
if ($rc -ne 0) { Fail "docker compose up falhou (exit $rc)" }
```

**Lição**: validar exit code com `$LASTEXITCODE` para native exes, **nunca**
com `$?` em PowerShell 5.1.

---

## 6. INBOUND race condition de contato

**Sintoma**: ao mandar 8 mensagens em rajada do mesmo número WhatsApp, 1
criava o contato com sucesso e as outras 7 retornavam `422 already been
taken`. Bridge marcava 7 como `failed`.

**Root cause**: 8 workers paralelos chamavam `POST /contacts` simultaneamente
para o mesmo phone_number. Chatwoot tem UNIQUE constraint — 1 vence, 7 perdem.

**Fix**: bridge detecta o 422 e cai num fallback de search:

```go
// internal/bridge/bridge.go (cwCreateContact)
if resp.StatusCode == 422 && strings.Contains(string(body), "already been taken") {
    return b.cwFindContactByPhone(ctx, tenant, phone)
}
```

`cwFindContactByPhone` faz `GET /api/v1/accounts/{acc}/contacts/search?q=<phone>`
e retorna o contato existente. Idempotente.

**Lição**: APIs com UNIQUE constraints precisam de fallback de leitura em
qualquer fluxo concorrente.

---

## 7. HMAC mismatch (CW→bridge)

**Sintoma**: 100% dos webhooks Chatwoot retornavam 401 do bridge. Tentamos
sincronizar o HMAC secret 3 vezes (gerar no bridge, copiar pro Inbox; gerar
no Inbox, recriar tenant; tentar pegar via API Chatwoot) — nenhum bateu.

**Root cause não totalmente confirmada**: suspeita de algoritmo / encoding
distinto entre o que o bridge espera (HMAC-SHA256 hex do body cru) e o que
o Chatwoot v4.13 envia. Ações tomadas:

1. Bridge agora faz strip do prefixo `sha256=` no header `X-Chatwoot-Signature`
   (alguns clientes mandam com prefixo):
   ```go
   sig := strings.TrimPrefix(r.Header.Get(hmacHeader), "sha256=")
   ```
2. Bypass de emergência adicionado: env `DEBUG_SKIP_HMAC=1` faz o bridge
   logar `WARN` e processar mesmo assim — usado para destravar a validação
   funcional. **Não usar em produção**.

**Workaround atual**: `DEBUG_SKIP_HMAC=1` ligado no `docker-compose.yml` em dev.

**Dívida técnica (pós-MVP)**: investigar formato real do header Chatwoot
v4.13 capturando um request via inspector ngrok, regenerar HMAC manualmente
em PowerShell, e ajustar o bridge. Opções:

- **CLI** `bridge tenant update --hmac-secret=<valor>` para sincronizar
  manualmente sem recriar tenant
- **Auto-pull**: bridge lê `hmac_token` direto de
  `GET /api/v1/accounts/{acc}/inboxes/{id}` no provisionamento

**Lição**: HMAC é um vetor clássico de incompatibilidade silenciosa. Vale
construir uma ferramenta interna `bridge debug verify-hmac --body=file.json
--sig=<sig> --slug=<slug>` para comparar local sem rodar tunnel.

---

## 8. TLS x509 unknown authority

**Sintoma**: bridge não conseguia fazer HTTPS para `apistart01.megaapi.com.br`.
Erro: `x509: certificate signed by unknown authority`.

**Root cause**: `Dockerfile` usa `FROM scratch` (boa prática de hardening),
mas `scratch` não tem CA bundle. Go usa o bundle do sistema para validar
TLS.

**Fix**: stage de build instala ca-certificates e o stage final copia o
bundle:

```dockerfile
FROM alpine:3 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/bridge /bridge
ENTRYPOINT ["/bridge"]
```

**Lição**: imagens `scratch` precisam de bundle explícito para qualquer
cliente HTTPS. Documentar como invariante.

---

## 9. megaAPI 401 — token errado

**Sintoma**: bridge marca mensagens outbound como `failed` com
`megaAPI 401 UNAUTHORIZED`.

**Root cause**: typo em `scripts/e2e/.env.e2e` —
`MEGAAPI_TOKEN=hatwoot` (sem `c`). Token correto é `chatwoot`.

**Fix**: corrigir o `.env.e2e` **não basta** — o token está AES-GCM cifrado
no DB já. Como não há `bridge tenant update`, foi necessário:

```bash
docker compose exec db psql -U bridge -d bridge -c "DELETE FROM tenants WHERE slug='e2e-teste';"
rm scripts/e2e/tenant-creds.json
./scripts/e2e/setup.ps1
```

**Dívida técnica**: implementar `bridge tenant update --megaapi-token=<v>`
(idem para outros secrets) para evitar drop+recreate.

---

## 10. `setup.ps1` sobrescreve `.env` raiz

**Sintoma**: cada execução do setup perde edições manuais no `.env` raiz,
notavelmente `DEBUG_SKIP_HMAC=1`.

**Root cause**: o script regrava o arquivo inteiro a partir de template
fixo. Sem merge.

**Workaround atual**: re-adicionar `DEBUG_SKIP_HMAC=1` após cada setup.

**Pendente**: alterar `setup.ps1` para preservar / fazer append de overrides
existentes em vez de overwrite total. Issue a ser criada via `bd`.

---

## Outros aprendizados operacionais

### Inspector ngrok foi essencial

Sem ele teríamos perdido horas adicionais no HMAC mismatch. `http://127.0.0.1:4040`
mostra request raw exato — headers, body, query — em formato que dá pra
comparar com o que o bridge logou.

### Cloudflared métricas locais

`http://127.0.0.1:20242/metrics` (porta varia) expõe
`cloudflared_tunnel_response_by_code{status_code=...}`. Permite ver
request+response sem precisar instrumentar o origin.

### Comparação HMAC manual

PowerShell com `HMACSHA256` permite calcular o esperado a partir do body bruto
copiado do inspector. Caminho mais rápido para confirmar se é mismatch de
secret ou de algoritmo.

### Inspeção de DB

`docker compose exec -T db psql -U bridge -d bridge` é o atalho mais usado.
Vale alias.

---

## Estado final da sessão

- Bridge processa texto bidirecional E2E real
- 9 bugs corrigidos no código (8 fixes definitivos + 1 workaround com bypass HMAC)
- 1 dívida pendente: HMAC sync entre Chatwoot e bridge
- 1 melhoria pendente: `bridge tenant update` CLI para evitar drop+recreate
- 1 melhoria pendente: `setup.ps1` preservar `.env` raiz

Os fixes 3, 5, 6, 7, 8 são patches relevantes para qualquer futura tentativa
E2E — sem eles o caminho seria igualmente bloqueado.

## Referências

- [`docs/14-e2e-setup-runbook.md`](./14-e2e-setup-runbook.md) — como montar o ambiente
- [`docs/15-troubleshooting.md`](./15-troubleshooting.md) — diagnóstico ordenado
- [`scripts/e2e/RUNBOOK.md`](../scripts/e2e/RUNBOOK.md) — quick-start
- [`docs/10-security.md`](./10-security.md) — modelo de auth (HMAC, Bearer, AES-GCM)
