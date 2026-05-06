# 12 — Roadmap

## Filosofia

Entregar valor incremental. Cada fase é deployável e testável de ponta a
ponta. Não há "fase preparatória" sem entrega de função visível.

## Fase 0 — Planejamento

**Status:** ✅ Concluída.

Entregas:
- [x] Pesquisa de viabilidade
- [x] Definição de arquitetura
- [x] Documentação completa em `/docs`
- [x] Issues criadas no `bd`
- [x] Aprovação do plano com stakeholders
- [x] Setup do repo (git, licença)

**Saída:** Aprovação para começar Fase 1.

---

## Fase 1 — MVP funcional (entregue)

**Status:** ✅ Entregue como **reset MVP "flat-first"** — ver
[`.agents/plans/reset-mvp.md`](../.agents/plans/reset-mvp.md). O escopo abaixo
foi deliberadamente reduzido em relação ao planejado originalmente; itens
adiados aparecem em fases posteriores ou ficaram como deviations registrados.

**Objetivo:** 1 tenant manda e recebe mensagem texto end-to-end.

Entregas:
- [x] Estrutura inicial Go (`cmd/bridge/`, `internal/bridge/`, `migrations/`)
- [x] Schema PostgreSQL aplicado (3 tabelas: `tenants`, `contacts`, `messages`)
- [x] Cliente HTTP megaAPI (`sendMegaAPIText`)
- [x] Cliente HTTP Chatwoot (criar contato, conversa, mensagem)
- [x] HTTP server bridge com rotas `/v1/wa/{slug}` e `/v1/cw/{slug}`
- [x] Worker pool com canais Go in-process (sem Redis, sem asynq)
- [x] CLI `bridge tenant add`
- [x] Encryption AES-256-GCM dos tokens
- [x] HMAC validation Chatwoot
- [x] Idempotência via `UNIQUE (tenant_id, direction, external_id)` em
      `messages` (sem tabela `idempotency_keys` separada)
- [x] Logs estruturados zerolog
- [x] Dockerfile multi-stage scratch
- [x] `docker-compose.yml` (2 serviços: postgres + bridge)
- [x] README com instruções básicas
- [x] Recuperação de pending no boot (`RecoverPending` via `messages.payload`)

**Adiado (sai em fases posteriores):**

- `sqlc` — `pgx` puro é suficiente para o MVP.
- `Asynq` / Redis — canais Go in-process resolvem com restart-recovery.
- Tabela `idempotency_keys` — UNIQUE em `messages` cobre o caso.
- `docker-compose` com 5 serviços — 2 bastam (postgres + bridge).

**Critério de saída (atendido):**
- Mandar WhatsApp pra número conectado → mensagem aparece no Chatwoot.
- Responder no Chatwoot → mensagem chega no WhatsApp.
- Texto apenas. Mídia não.

---

## Fase 2 — Mídia + reliability (semanas 4–5)

**Objetivo:** suporte a imagens/áudio/vídeo/documento + retry robusto.

Entregas:
- [ ] Mídia inbound (megaAPI → Chatwoot, pass-through URL)
- [ ] Mídia outbound (Chatwoot → megaAPI, URL ou stream)
- [ ] Retry exponencial com classificação de erro
- [ ] DLQ + endpoint admin para inspecionar/retentar
- [ ] Backpressure (`/readyz` 503 com queue cheia)
- [ ] Métricas Prometheus básicas
- [ ] Endpoint `/healthz`, `/readyz`, `/metrics`
- [ ] Testes integração com testcontainers (PG + Redis reais)

**Critério de saída:**
- 1000 msgs/s sustentado em load test sem perda.
- Chatwoot offline 5 min → 100% das mensagens entregues após volta.
- Mídia (imagem/áudio/doc) trafega nos 2 sentidos.

---

## Fase 3 — UI Admin + UX leigo (semanas 6–7)

**Objetivo:** zero edição de arquivo após install.

Entregas:
- [ ] Layout htmx + Tailwind + alpine
- [ ] Login admin (argon2id + sessão)
- [ ] Wizard "Novo Tenant" 4 passos
- [ ] Dashboard global + dashboard por tenant
- [ ] Auto-discovery de inboxes Chatwoot
- [ ] Auto-registro de webhooks (megaAPI + Chatwoot)
- [ ] Diagnóstico em 1 clique (checklist live)
- [ ] Log de mensagens paginado
- [ ] DLQ admin
- [ ] Configurações (admins, alertas)
- [ ] i18n PT-BR completo
- [ ] CSRF + headers segurança

**Critério de saída:**
- Usuário leigo cria tenant ponta a ponta sem CLI.
- Usuário leigo diagnostica problema com botão "Diagnosticar".
- Time interno consegue suporte remoto via screen-share da UI.

---

## Fase 4 — Instalador 1-comando (semana 8)

**Objetivo:** `curl | bash` funciona em VPS limpa.

Entregas:
- [ ] Script `install.sh` interativo
- [ ] Templates `.env`, `Caddyfile`, `init.sql`
- [ ] Modo Caddy + Let's Encrypt
- [ ] Modo Cloudflare Tunnel (sem domínio)
- [ ] Geração de secrets aleatórios
- [ ] Bootstrap DB Chatwoot + bridge
- [ ] Criação automática de admin
- [ ] Pós-check (`./scripts/postinstall-check.sh`)
- [ ] Script `upgrade.sh`
- [ ] Backup sidecar (`postgres-backup-local`)
- [ ] Documentação `INSTALL.md` + vídeo screencast 5 min

**Critério de saída:**
- VPS Ubuntu 22.04 limpa → operacional em <15 min.
- Usuário leigo recrutado em teste real consegue sem suporte.

---

## Fase 5 — Observabilidade avançada (semana 9)

**Objetivo:** diagnóstico remoto sem login no servidor.

Entregas:
- [ ] Dashboard Grafana pré-construído
- [ ] AlertManager com rules padrão
- [ ] Webhook de alerta configurável (Slack/Discord/email)
- [ ] Compose extra `docker-compose.observability.yml`
- [ ] OpenTelemetry traces (opt-in)
- [ ] Profiling endpoint (admin gated)
- [ ] Documentação de troubleshooting comum

**Critério de saída:**
- Operador instala dashboard com 1 comando extra.
- Time recebe alertas de DLQ crescendo / tenant parado.

---

## Fase 6 — Hardening + 1.0 release (semanas 10–11)

**Objetivo:** produção-ready com confiança.

Entregas:
- [ ] Pen test interno (OWASP ZAP, nuclei, gosec)
- [ ] Code review de segurança com skill `appsec-elite-auditor`
- [ ] Load test prolongado (24h, 500 msgs/s sustentado)
- [ ] Chaos test (kill containers durante tráfego)
- [ ] Documentação completa de operação
- [ ] Política de versão + breaking changes
- [ ] Política de suporte / SLA (se aplicável)
- [ ] Tag `v1.0.0`
- [ ] Anúncio interno + onboarding de N clientes piloto

**Critério de saída:**
- 5 clientes piloto rodando 7 dias sem incidente crítico.
- Métricas batem targets ([07](./07-reliability-and-performance.md)).

---

## Pós-1.0 (futuro)

Backlog para v1.x:

- Sticker / localização / contato (vCard) / reações
- 2FA TOTP no admin
- Multi-admin com roles (owner/operator/viewer)
- Auditoria exportável (CSV)
- API pública do bridge (gerenciamento programático de tenants)
- Suporte a múltiplas inboxes por tenant (1 tenant → N inboxes)
- Templates de mensagem com variáveis
- Auto-respostas / horário comercial
- Métricas de SLA (TTFR, TTR) por tenant
- Compatibilidade com outras APIs WhatsApp (Evolution, WAHA, baileys) via
  driver pluggável
- Helm chart para Kubernetes
- Multi-arch images (arm64 incluído)

## Marcos visíveis

| Marco | Quando | Demo |
|-------|--------|------|
| M1: Texto E2E | Fim Fase 1 | Vídeo 1 min mostrando msg ida/volta |
| M2: Reliability | Fim Fase 2 | Mostrar load test + Chatwoot offline |
| M3: Wizard UI | Fim Fase 3 | Vídeo screencast criando tenant |
| M4: One-command | Fim Fase 4 | Vídeo install em VPS zerada |
| M5: Production | Fim Fase 6 | Cliente piloto operando |

## Estimativa total

~11 semanas de trabalho dedicado (1 dev sênior). Pode paralelizar UI (Fase
3) com reliability (Fase 2) por outro dev.
