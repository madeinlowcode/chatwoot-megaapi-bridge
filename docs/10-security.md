# 10 — Segurança

## Modelo de ameaças (resumo)

| Ameaça | Vetor | Mitigação |
|--------|-------|-----------|
| Webhook forjado | Atacante chama `/v1/wa/{slug}` direto | Bearer token único por tenant; rate limit por IP |
| Webhook forjado Chatwoot | Mesma coisa em `/v1/cw/{slug}` | HMAC-SHA256 obrigatório |
| Token megaAPI vazado | Acesso ao banco | AES-GCM em repouso, master key fora do DB |
| Token Chatwoot vazado | Idem | AES-GCM idem |
| RCE via payload | Parser sem validação | Validação estrita com schema, limites de tamanho |
| SQL injection | Query string concatenada | sqlc gera prepared statements; zero string concat |
| XSS no admin UI | Conteúdo renderizado sem escape | `html/template` escape automático; CSP estrito |
| CSRF no admin | POST cross-site | Token CSRF, SameSite=Lax cookie |
| Brute force login | Tentativas senha admin | Argon2id + lockout |
| MITM | TLS comprometido | Caddy TLS 1.3, HSTS, rejeita HTTP |
| DDoS | Webhook flood | Rate limit por IP + por tenant + queue cap |
| Credential stuffing | Reuso senhas | Recomenda 2FA (roadmap) |
| Container escape | Privilege | Containers não-root, read-only fs onde possível |

## Criptografia em repouso

### Master key
- 256 bits, gerada na instalação.
- Armazenada em `MASTER_KEY` env var (NÃO no DB).
- Cifrada via Docker secrets em produção (recomendado).
- Rotacionável: cada secret persistido carrega `kid` (key id). Rotação =
  re-cifrar todos com kid novo, manter kid antigo até completar.

### Algoritmo
- **AES-256-GCM** (autenticação + cifra).
- Nonce aleatório de 96 bits por operação.
- Ciphertext = `nonce || ciphertext_tag`. Armazenado como `BYTEA`.

### Helper Go
```go
func Encrypt(plaintext []byte, key []byte) ([]byte, error) {
    block, _ := aes.NewCipher(key)
    aead, _ := cipher.NewGCM(block)
    nonce := make([]byte, aead.NonceSize())
    rand.Read(nonce)
    return aead.Seal(nonce, nonce, plaintext, nil), nil
}
```

Tudo que é segredo: `bearer_token`, `api_token`, `hmac_secret` → cifrado.

## Validação de webhook entrante

### megaAPI → bridge
megaAPI **não assina** payload nativamente. Mitigação: token Bearer único por
tenant na URL do webhook.

```
POST /v1/wa/{slug}
Authorization: Bearer <token gerado pelo bridge na criação do tenant>
```

Token armazenado no DB cifrado. Comparação em tempo constante
(`subtle.ConstantTimeCompare`).

### Chatwoot → bridge
Chatwoot assina com HMAC-SHA256. Bridge valida:

```go
func VerifyChatwootSig(body []byte, signature, secret string) bool {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := hex.EncodeToString(mac.Sum(nil))
    return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}
```

## TLS

Caddy gerencia automaticamente:
- ACME (Let's Encrypt) com renovação automática.
- TLS 1.2 e 1.3 apenas. TLS 1.0/1.1 desabilitados.
- HSTS: `max-age=31536000; includeSubDomains; preload`.
- Cipher suites modernos (curva P-256/X25519, ChaCha20/AES-GCM).

## Headers de segurança (Caddyfile)

```caddyfile
header {
    Strict-Transport-Security "max-age=31536000; includeSubDomains"
    X-Content-Type-Options "nosniff"
    X-Frame-Options "DENY"
    Referrer-Policy "strict-origin-when-cross-origin"
    Permissions-Policy "geolocation=(), microphone=(), camera=()"
    Content-Security-Policy "default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com; script-src 'self' 'unsafe-inline' https://unpkg.com"
}
```

## Auth admin

### Senha
- Argon2id parâmetros: `t=3, m=64MB, p=2`.
- Tamanho mínimo 12 caracteres no UI.
- Política: maiúscula+minúscula+número (recomendação, não bloqueio).

### Sessão
- Cookie `bridge_session` HttpOnly + Secure + SameSite=Lax.
- TTL 24h, refresh em uso.
- Sliding expiration.

### Lockout
- 5 tentativas falhas em 15 min → IP bloqueado por 1h.
- Tabela `login_attempts` (transient, limpa por cron).

### 2FA (roadmap v1.x)
- TOTP (RFC 6238).
- Lib: `pquerna/otp`.

## Segredos no compose / .env

- `.env` files com permissão `600` no host.
- Em produção avançada: Docker secrets ou Vault.
- Install script seta permissões corretas automaticamente.

## Logs

- **Nunca logar tokens, senhas, HMAC secrets, payloads sensíveis.**
- Função `redact()` para remover campos `password`, `token`, `secret` de logs
  estruturados.
- Audit log captura *intenção* (operação, sucesso/falha) sem segredos.

## Container hardening

```yaml
bridge-api:
  read_only: true
  tmpfs:
    - /tmp
  cap_drop: [ALL]
  security_opt:
    - no-new-privileges:true
  user: "65534:65534"  # nobody
```

Imagem `scratch` já não tem shell ou ferramentas para escalonamento.

## Rate limit anti-abuse

- Por IP em `/v1/*`: 1000 req/min (geralmente megaAPI/Chatwoot são poucos
  IPs, mas guarda contra ataque).
- Por IP em `/admin/login`: 10 req/min.

## Atualizações de segurança

- Dependabot/Renovate semanais para Go modules.
- Imagem base Alpine atualizada mensalmente.
- Subscribe nos advisories de:
  - Go security
  - Chatwoot releases
  - Caddy
  - Postgres / Redis

## Compliance

- LGPD: documentar quais dados pessoais o bridge armazena (números WA,
  conteúdo de mensagens em `payload jsonb`).
- DPA padrão para clientes que peçam.
- Anonymizer opcional (v2): hash números de WA depois de N dias.

## Pen test

Antes de v1.0:
- OWASP ZAP em modo headless contra UI admin.
- `nuclei` com templates contra rotas públicas.
- Code review com `gosec`.
- Skill `appsec-elite-auditor` aplicada antes de cada release.

## Backup e recuperação

- Backups Postgres cifrados em repouso (compressão + GPG opcional).
- Master key NÃO está no backup do DB. Operador tem que guardar separado
  (cofre, papel impresso, password manager).
- Sem master key, dados cifrados = perda total. Documentado no install.
