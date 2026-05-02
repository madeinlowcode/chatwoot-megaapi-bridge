# 13 — Riscos e Decisões Arquiteturais (ADRs)

## Riscos identificados

### R1 — megaAPI é WhatsApp não-oficial
**Impacto:** Alto. Risco de ban de número pelo WhatsApp.
**Probabilidade:** Média.
**Mitigação:** Documentar claramente para o cliente final. Não fazer
promessas de SLA além do que a megaAPI promete. Oferecer roadmap futuro com
suporte a Evolution/WAHA/Cloud API como driver alternativo.

### R2 — Quebra de contrato megaAPI
**Impacto:** Alto. Bridge para de funcionar.
**Probabilidade:** Média (não controlamos).
**Mitigação:** Parser tolerante (ignora campos desconhecidos). Versão de
bridge pinada. Monitor de regressão (alerta se taxa de erro 4xx subir
súbito). Contato direto com a megaAPI quando houver.

### R3 — Quebra de contrato Chatwoot
**Impacto:** Alto.
**Probabilidade:** Baixa (API estável há anos).
**Mitigação:** Pinar versão Chatwoot. Smoke tests no startup do bridge
contra endpoints conhecidos.

### R4 — Volume sustentado acima do projetado
**Impacto:** Médio. Latência sobe, queue cresce.
**Probabilidade:** Baixa em fase 1, alta em escala.
**Mitigação:** Backpressure + escala horizontal. Alerta de queue depth.
Documentar limites por instância e quando escalar.

### R5 — Operador esquece master key
**Impacto:** Alto. Perda de configs cifradas.
**Probabilidade:** Média (usuário leigo).
**Mitigação:** Install script imprime master key + obriga o usuário a salvar
em local seguro com confirmação. Documentar recovery.

### R6 — Usuário expõe `/admin` na internet sem TLS
**Impacto:** Alto.
**Probabilidade:** Baixa (Caddy força TLS).
**Mitigação:** Caddy default é HTTPS-only. Install nunca configura HTTP
puro. Aviso explícito em logs se detectar config insegura.

### R7 — Performance do parser JSONB em alto volume
**Impacto:** Médio.
**Probabilidade:** Baixa.
**Mitigação:** Postgres handles JSONB bem até dezenas de milhões de linhas.
Particionamento por mês quando necessário.

### R8 — Suporte a casos extremos (mídia >100 MB)
**Impacto:** Baixo. Falha localizada na mensagem.
**Probabilidade:** Média.
**Mitigação:** Limite de tamanho configurável por tenant. Fallback: grava
mensagem como texto explicativo "mídia muito grande, baixe direto".

### R9 — Concorrência entre bridge e Chatwoot escrevendo mesmo conversation
**Impacto:** Baixo. Status race entre múltiplas mensagens chegando juntas.
**Probabilidade:** Média em alto volume.
**Mitigação:** Lock por `conversation_id` no worker (Redis lua
`SET NX EX`).

### R10 — Custo operacional para usuário leigo (DNS, domínio)
**Impacto:** Médio. Adoção menor.
**Probabilidade:** Alta.
**Mitigação:** Modo Cloudflare Tunnel (sem domínio próprio).
Documentação passo-a-passo registro de domínio + DNS.

---

## ADRs (Architectural Decision Records)

### ADR-001 — Linguagem Go
**Status:** Aceito.
**Contexto:** Necessidade de bridge de alta vazão, fácil distribuição,
manutenção de longo prazo.
**Decisão:** Go.
**Alternativas consideradas:** Node.js, Rust, Elixir.
**Consequências:** Pipeline simples (1 binário), boa concorrência,
dependência menor de runtime, hire pool razoável. Velocidade de dev levemente
menor que Node.

### ADR-002 — Filas via asynq (Redis), não Kafka/RabbitMQ
**Status:** Aceito.
**Contexto:** Precisa de filas durável + retry, integrar com stack que já
tem Redis.
**Decisão:** asynq.
**Alternativas:** Kafka (overkill), RabbitMQ (mais um serviço), pgmq (DB
load), River (Postgres-only, ainda jovem).
**Consequências:** Reaproveita Redis. Asynq é maduro, dashboard pronto.
Trade-off: Redis precisa AOF habilitado para durabilidade.

### ADR-003 — sqlc + pgx, não ORM
**Status:** Aceito.
**Contexto:** Performance + queries explícitas + type-safety.
**Decisão:** sqlc gera código Go a partir de SQL puro.
**Alternativas:** GORM (ORM completo), bun, ent.
**Consequências:** Zero mágica, PRs revisam SQL real. Pequena curva com
sqlc.yaml.

### ADR-004 — htmx no admin, não React/Vue
**Status:** Aceito.
**Contexto:** UI admin é CRUD básico. Reduzir complexidade de build.
**Decisão:** htmx + alpine.js, server-rendered.
**Alternativas:** React/Next.js, Vue.
**Consequências:** Sem pipeline de build JS. Bundle pequeno. Ajustes de UX
mais simples. Trade-off: limita UX rica (ok pra esse caso).

### ADR-005 — Caddy, não nginx + certbot
**Status:** Aceito.
**Contexto:** Reverse proxy + TLS automático, foco em "fácil pra leigo".
**Decisão:** Caddy 2.
**Alternativas:** nginx + certbot, traefik.
**Consequências:** Caddyfile mais simples que nginx.conf. Renovação TLS
automática out-of-the-box. Trade-off: comunidade menor que nginx.

### ADR-006 — DB único Postgres com 2 databases (chatwoot + bridge)
**Status:** Aceito.
**Contexto:** Reduzir overhead operacional vs separação de risco.
**Decisão:** Mesmo cluster, databases distintos.
**Alternativas:** Cluster separado, schemas separados.
**Consequências:** Backup/restore independente por database. Compartilha
recursos do host. Possível migrar pra cluster separado depois sem mudança de
código.

### ADR-007 — Mídia por URL pass-through
**Status:** Aceito.
**Contexto:** Bridge não deve passar bytes em memória.
**Decisão:** Encaminhar URL da megaAPI para o Chatwoot baixar.
**Alternativas:** Stream proxy, download+upload.
**Consequências:** Latência baixa, memória previsível. Risco: URL expirar
(megaAPI usa URLs assinadas). Mitigação: fallback de stream se URL falhar.

### ADR-008 — Idempotência via tabela dedicada, não Redis
**Status:** Aceito.
**Contexto:** Idempotência precisa sobreviver a restart de Redis.
**Decisão:** Tabela `idempotency_keys` em Postgres com retenção 7 dias.
**Alternativas:** Redis com TTL.
**Consequências:** Mais durável. Trade-off: lookup em DB no hot path. Custo
mitigado por índice + cache em memória do bridge.

### ADR-009 — Wizard de install em bash, não TUI dedicada
**Status:** Aceito.
**Contexto:** Reduzir dependências de instalação.
**Decisão:** `install.sh` em bash com `read` interativo.
**Alternativas:** TUI (charm/gum), CLI Go binária pré-built.
**Consequências:** Funciona em qualquer Linux/Mac. Windows via WSL.
Trade-off: UX bash é limitada (mas suficiente pra perguntas simples).

### ADR-010 — Cloudflare Tunnel como opção zero-domínio
**Status:** Aceito.
**Contexto:** Reduzir barreira de entrada para usuário sem domínio.
**Decisão:** Suportar tunnel como modo alternativo.
**Alternativas:** ngrok (pago), localtunnel (instável).
**Consequências:** Cloudflare é confiável e gratuito até volumes
significativos. Trade-off: dependência de terceiro (mas opcional).

---

## Decisões em aberto

### DA-1 — Multi-admin desde MVP ou só v1.x?
**Pendente.** Por enquanto: 1 admin, suficiente para fase 1.

### DA-2 — Suporte a múltiplas inboxes Chatwoot por tenant?
**Pendente.** MVP: 1 tenant = 1 inbox. v1.x: avaliar demanda.

### DA-3 — Distribuir como SaaS gerenciado por nós?
**Pendente.** Foco atual on-premise. Avaliar comercial pós-1.0.

### DA-4 — Suportar driver pluggável (Evolution/WAHA além de megaAPI)?
**Pendente.** Arquitetura permite (interface `WhatsAppProvider`). Implementar
quando demanda surgir.

### DA-5 — Helm chart para Kubernetes?
**Pendente.** Após Compose maduro. Provavelmente v1.x.

### DA-6 — Telemetria opcional pro fabricante (nós)?
**Pendente.** Se sim, completamente opt-in com termo claro. Útil para
estatísticas anônimas de erro. Decidir antes do 1.0 público.
