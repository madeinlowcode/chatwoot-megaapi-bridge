# 01 — Contexto e Objetivos

## Contexto

Operamos com clientes que consomem a **megaAPI** (API não-oficial de WhatsApp,
hospedada em múltiplos hosts: `apibusiness1.megaapi.com.br`,
`apibusiness7.megaapi.com.br`, etc — cada plano da megaAPI tem host distinto).
Vários clientes pediram interface omnichannel de atendimento. Já validamos o
padrão "WhatsApp + plataforma de atendimento" usando Rocket.Chat em containers
Docker separados.

Chatwoot tem UX/relatórios superiores ao Rocket para o caso de uso de
atendimento e vendas, e é alternativa open-source madura.

## Problema

1. Chatwoot não tem suporte nativo à megaAPI. O canal "WhatsApp" oficial do
   Chatwoot exige Cloud API da Meta ou 360Dialog.
2. Existem brigdes da comunidade (ex: Evolution+Chatwoot, WAHA+Chatwoot), mas
   nenhuma multi-tenant nem com host megaAPI dinâmico.
3. Soluções existentes não focam em **alta vazão garantida** nem em
   **instalação fácil para usuário leigo**.

## Objetivos (G)

- **G1** — Bridge bidirecional megaAPI ↔ Chatwoot, multi-tenant, com host
  megaAPI configurável por tenant.
- **G2** — Garantia de entrega: zero perda de mensagem em condições normais e
  recuperação automática em falhas transitórias.
- **G3** — Throughput sustentado de 1000+ mensagens/segundo por instância do
  bridge, latência p99 webhook ACK < 10 ms.
- **G4** — Instalação em 1 comando para usuário não-técnico, configuração via
  UI web (zero edição de arquivos).
- **G5** — Observabilidade ponta a ponta para diagnóstico rápido sem suporte
  manual.

## Não-objetivos (NG)

- **NG1** — Não substituir Chatwoot. Apenas integrar.
- **NG2** — Não construir API própria de WhatsApp. Dependemos da megaAPI.
- **NG3** — Não cobrir Cloud API oficial da Meta nesta fase (Chatwoot já cobre).
- **NG4** — Não atender SaaS multi-cliente em uma única instância gerenciada
  por nós nesta fase. Foco é deploy on-premise/per-cliente.
- **NG5** — Não desenvolver módulos de billing, pagamentos, ou CRM.

## Métricas de sucesso

| Métrica | Alvo |
|---------|------|
| Taxa de entrega (msg recebidas pelo Chatwoot ÷ msgs disparadas pela megaAPI) | ≥ 99,95% em janela de 30 dias |
| Latência p99 ACK webhook | < 10 ms |
| Latência p99 entrega Chatwoot (sem fila acumulada) | < 200 ms |
| Tempo médio de instalação (do `curl` ao primeiro tenant operando) | < 15 min |
| Taxa de instalações que requerem suporte manual | < 5% |
| MTTR (mean time to recovery) após falha de dependência | < 60 s |
| RAM consumida por tenant ativo | < 10 MB |

## Stakeholders

- **Operador final (usuário leigo)** — instala e administra Chatwoot+bridge na
  máquina dele/VPS.
- **Atendentes** — usam Chatwoot direto, não precisam saber do bridge.
- **Clientes finais** — enviam/recebem WhatsApp, não notam o bridge.
- **Time de desenvolvimento (nós)** — mantém bridge, adiciona features.

## Premissas

- megaAPI continua estável e mantém contrato atual de webhook + envio.
- Chatwoot mantém API Channel funcional (existe há vários anos, pouco risco).
- Docker Desktop / Docker Engine disponível na máquina alvo.
- Para webhook receber chamadas externas, máquina precisa de domínio público
  ou túnel (Cloudflare Tunnel/ngrok) — abordado em [08](./08-deployment-and-install.md).

## Restrições

- Sem dependência de serviços pagos obrigatórios (operador pode escolher).
- Suporte mínimo: Linux (qualquer distro com Docker) e Windows (via Docker
  Desktop + WSL2). macOS por extensão.
- Banco de dados: PostgreSQL (já presente no stack Chatwoot).
- Fila: Redis (já presente).
