# chatwoot-megaapi-bridge — Documentação de Planejamento

Bridge multi-tenant em Go que conecta **Chatwoot self-hosted** à **megaAPI**
(WhatsApp não-oficial), com host megaAPI dinâmico por tenant, alta vazão de
mensagens, filas de retry, e instalação fácil para usuários leigos.

> **Status:** Fase de planejamento. Nenhum código produzido. Toda
> implementação depende de aprovação dos documentos abaixo.

## Índice

| # | Documento | Descrição |
|---|-----------|-----------|
| 01 | [Contexto e Objetivos](./01-context-and-goals.md) | Problema, escopo, não-escopo, métricas de sucesso |
| 02 | [Arquitetura](./02-architecture.md) | Diagrama de componentes, fluxos de mensagem |
| 03 | [Stack Tecnológico](./03-tech-stack.md) | Decisões de linguagem, bibliotecas, justificativas |
| 04 | [Multi-tenancy](./04-multi-tenancy.md) | Modelo de tenant, host dinâmico, isolamento |
| 05 | [Modelo de Dados](./05-data-model.md) | Schema PostgreSQL, índices, retenção |
| 06 | [API e Protocolos](./06-api-and-protocols.md) | Endpoints bridge, webhook contracts, mapping |
| 07 | [Confiabilidade e Performance](./07-reliability-and-performance.md) | Filas, retry, idempotência, throughput |
| 08 | [Deploy e Instalação](./08-deployment-and-install.md) | docker-compose, Caddy/TLS, install wizard |
| 09 | [UI Admin](./09-admin-ui.md) | Wizard de configuração, dashboards, autoteste |
| 10 | [Segurança](./10-security.md) | HMAC, criptografia, secrets, OWASP |
| 11 | [Observabilidade](./11-observability.md) | Logs, métricas, traces, alertas |
| 12 | [Roadmap](./12-roadmap.md) | Fases MVP → produção |
| 13 | [Riscos e Decisões](./13-risks-and-decisions.md) | ADRs, tradeoffs, mitigações |

## Glossário rápido

- **Tenant** — um cliente final (uma conta megaAPI + uma inbox Chatwoot).
- **Bridge** — serviço Go que traduz mensagens entre megaAPI e Chatwoot.
- **API Channel** — tipo de inbox do Chatwoot que aceita integração custom via API/webhook.
- **Instance Key** — identificador da instância WhatsApp dentro da megaAPI.
- **DLQ** — Dead Letter Queue, fila de mensagens que falharam todas as retentativas.
