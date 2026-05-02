# 08 — Deploy e Instalação

## Cenários suportados

1. **Local (Docker Desktop Windows/Mac)** — POC, dev, cliente individual com
   máquina dedicada.
2. **VPS Linux (Debian/Ubuntu)** — produção típica, domínio próprio.
3. **VPS Linux + Cloudflare Tunnel** — produção sem IP público / sem domínio.

## Requisitos de máquina

| Cenário | RAM mínima | RAM rec. | CPU | Disco |
|---------|-----------|----------|-----|-------|
| 1–10 tenants | 4 GB | 6 GB | 2 vCPU | 20 GB |
| 10–100 tenants | 6 GB | 8 GB | 4 vCPU | 50 GB |
| 100–500 tenants | 8 GB | 16 GB | 8 vCPU | 100 GB |

Software:
- Docker 24+ e Docker Compose v2.20+
- Sistema com `curl`, `bash`
- Porta 80/443 abertas (cenários 1 e 2) **ou** Cloudflared instalado (3)

## docker-compose.yml (planejado)

```yaml
name: chatwoot-megaapi-bridge

networks:
  omni-net:
    driver: bridge

volumes:
  postgres_data:
  redis_data:
  chatwoot_storage:
  chatwoot_public:
  caddy_data:
  caddy_config:

services:

  caddy:
    image: caddy:2-alpine
    restart: unless-stopped
    ports: ["80:80", "443:443"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    networks: [omni-net]
    depends_on: [chatwoot, bridge-api]

  postgres:
    image: postgres:15-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./init.sql:/docker-entrypoint-initdb.d/init.sql:ro
    networks: [omni-net]
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "${POSTGRES_USER}"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: ["redis-server", "--appendonly", "yes", "--requirepass", "${REDIS_PASSWORD}"]
    volumes: [redis_data:/data]
    networks: [omni-net]
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "${REDIS_PASSWORD}", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  chatwoot:
    image: chatwoot/chatwoot:v3.16.0
    restart: unless-stopped
    env_file: .env.chatwoot
    depends_on:
      postgres: { condition: service_healthy }
      redis:    { condition: service_healthy }
    volumes:
      - chatwoot_storage:/app/storage
      - chatwoot_public:/app/public
    networks: [omni-net]
    entrypoint: ["docker/entrypoints/rails.sh"]
    command: ["bundle", "exec", "rails", "s", "-p", "3000", "-b", "0.0.0.0"]

  sidekiq:
    image: chatwoot/chatwoot:v3.16.0
    restart: unless-stopped
    env_file: .env.chatwoot
    depends_on:
      postgres: { condition: service_healthy }
      redis:    { condition: service_healthy }
    volumes:
      - chatwoot_storage:/app/storage
    networks: [omni-net]
    command: ["bundle", "exec", "sidekiq", "-C", "config/sidekiq.yml"]

  bridge-api:
    image: ghcr.io/${ORG}/chatwoot-megaapi-bridge:${BRIDGE_VERSION}
    restart: unless-stopped
    env_file: .env.bridge
    command: ["/bridge"]
    depends_on:
      postgres: { condition: service_healthy }
      redis:    { condition: service_healthy }
    networks: [omni-net]
    healthcheck:
      test: ["CMD", "/bridge", "healthcheck"]
      interval: 15s

  bridge-worker:
    image: ghcr.io/${ORG}/chatwoot-megaapi-bridge:${BRIDGE_VERSION}
    restart: unless-stopped
    env_file: .env.bridge
    command: ["/bridge-worker"]
    depends_on:
      postgres: { condition: service_healthy }
      redis:    { condition: service_healthy }
    networks: [omni-net]
    deploy:
      replicas: 1   # escala se necessário
```

## Caddyfile (planejado)

```caddyfile
{$DOMAIN} {
    encode gzip zstd
    
    handle /admin* {
        reverse_proxy bridge-api:8080
    }
    
    handle /v1/* {
        reverse_proxy bridge-api:8080
    }
    
    handle /healthz {
        reverse_proxy bridge-api:8080
    }
    
    handle {
        reverse_proxy chatwoot:3000
    }
    
    log {
        output file /data/access.log
        format json
    }
}
```

## init.sql

```sql
CREATE DATABASE chatwoot;
CREATE DATABASE bridge;
```

## .env.bridge (gerado pelo install)

```
POSTGRES_DSN=postgres://user:pass@postgres:5432/bridge?sslmode=disable
REDIS_ADDR=redis:6379
REDIS_PASSWORD=...
MASTER_KEY=...                # base64, AES-GCM
SESSION_SECRET=...
ADMIN_EMAIL=...
ADMIN_PASSWORD_HASH=...       # argon2
PUBLIC_DOMAIN=https://...
LOG_LEVEL=info
METRICS_ENABLED=true
```

## install.sh — wizard

```bash
#!/usr/bin/env bash
set -euo pipefail

clear
echo "Instalador chatwoot-megaapi-bridge"
echo "----------------------------------"

# 1. Pré-requisitos
command -v docker >/dev/null || { echo "Instale Docker primeiro: https://docs.docker.com/get-docker/"; exit 1; }
docker compose version >/dev/null || { echo "Compose v2 necessário"; exit 1; }

# 2. Coleta input
read -p "Domínio público (ex: atendimento.empresa.com): " DOMAIN
read -p "Email admin: " ADMIN_EMAIL
read -s -p "Senha admin: " ADMIN_PASS; echo
read -p "Modo TLS [letsencrypt/cloudflared/none]: " TLS_MODE

# 3. Gera secrets
POSTGRES_PASS=$(openssl rand -base64 32)
REDIS_PASS=$(openssl rand -base64 32)
MASTER_KEY=$(openssl rand -base64 32)
SESSION_SECRET=$(openssl rand -hex 32)
SECRET_KEY_BASE=$(openssl rand -hex 64)

# 4. Renderiza .env.bridge, .env.chatwoot, Caddyfile
# (templates incluídos no instalador)

# 5. Pull images
docker compose pull

# 6. Bootstrap Chatwoot DB
docker compose run --rm chatwoot bundle exec rails db:chatwoot_prepare

# 7. Bootstrap Bridge DB (migrations)
docker compose run --rm bridge-api /bridge migrate up

# 8. Cria admin do bridge
docker compose run --rm bridge-api /bridge admin create \
  --email "$ADMIN_EMAIL" --password "$ADMIN_PASS"

# 9. Sobe tudo
docker compose up -d

# 10. Aguarda Caddy obter TLS
echo "Aguardando TLS..."
until curl -sf "https://$DOMAIN/healthz" >/dev/null; do sleep 5; done

echo
echo "✅ Instalação concluída!"
echo "Acesse:"
echo "  Chatwoot: https://$DOMAIN"
echo "  Bridge admin: https://$DOMAIN/admin"
echo "  Login: $ADMIN_EMAIL"
```

Distribuído como:
```
curl -fsSL https://get.bridge.example/install.sh | bash
```

ou clone repo + `./install.sh`.

## Modo Cloudflare Tunnel (sem domínio)

Para usuário sem domínio próprio:
1. Install script detecta opção `cloudflared`.
2. Instala `cloudflared` e cria tunnel via `cloudflared tunnel login`.
3. Gera URL `https://random-name.trycloudflare.com` (ou domínio do usuário no
   Cloudflare).
4. Caddy fica apenas como proxy interno (sem TLS, Cloudflare termina TLS).

Trade-off: Cloudflare faz proxy. Aceitável para a maioria dos usuários
leigos.

## Update / upgrade

```bash
# atualiza imagens
docker compose pull

# roda migrations bridge
docker compose run --rm bridge-api /bridge migrate up

# upgrade Chatwoot
docker compose run --rm chatwoot bundle exec rails db:chatwoot_prepare

# reinicia
docker compose up -d
```

Documentado no `UPGRADE.md`.

## Backup automatizado

Sidecar container `postgres-backup`:
```yaml
backup:
  image: prodrigestivill/postgres-backup-local
  restart: unless-stopped
  environment:
    POSTGRES_HOST: postgres
    POSTGRES_DB: chatwoot,bridge
    POSTGRES_USER: ${POSTGRES_USER}
    POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    SCHEDULE: "@daily"
    BACKUP_KEEP_DAYS: 30
  volumes:
    - ./backups:/backups
  networks: [omni-net]
```

## Estratégia de versões

- Bridge tagueia `vMAJOR.MINOR.PATCH`.
- Chatwoot pinado a versão estável (atualização manual).
- `BRIDGE_VERSION` em `.env` — usuário controla.
- Renovate bot abre PR pra repo do bridge atualizando deps Go semanal.

## Verificação pós-instalação

`./scripts/postinstall-check.sh`:
- `docker compose ps` — todos `healthy`
- `curl https://$DOMAIN/healthz` — 200
- `curl https://$DOMAIN/readyz` — 200
- `curl https://$DOMAIN/api` — 200 (Chatwoot)
- Login admin bridge funciona
- Login Chatwoot funciona
