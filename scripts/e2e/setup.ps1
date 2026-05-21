# scripts/e2e/setup.ps1
# Orquestra E2E real (SCR-69 / bd 96l.15).
#
# WHY: a única forma de validar texto + mídia bidirecional contra megaAPI/
# Chatwoot reais é com (1) bridge stack up, (2) tunnel público apontando pro
# bridge e (3) tenant provisionado. Este script automatiza tudo até o ponto
# onde o user só precisa colar 2 URLs nas UIs externas (megaAPI dashboard +
# Chatwoot Inbox webhook).
#
# Idempotente: rodar 2x detecta tenant existente e reusa, detecta cloudflared
# já rodando e reusa, detecta stack já up e não reinicia.
#
# Requisitos verificados:
#   - docker (CLI + Engine running)
#   - cloudflared (>= 2024.x)
#   - scripts/e2e/.env.e2e (copiado de .env.e2e.example)
#   - Chatwoot dev stack rodando (containers chatwoot-dev-*)

[CmdletBinding()]
param(
    [switch]$ReuseTunnel,
    [int]$HealthTimeoutSec = 90
)

$ErrorActionPreference = 'Stop'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Resolve-Path (Join-Path $ScriptDir '..\..')
$EnvFile = Join-Path $ScriptDir '.env.e2e'
$TunnelLog = Join-Path $ScriptDir 'cloudflared.log'
$TunnelUrlFile = Join-Path $ScriptDir 'tunnel-url.txt'
$CredsFile = Join-Path $ScriptDir 'tenant-creds.json'

function Write-Step($msg) { Write-Host ""; Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok($msg)   { Write-Host "    [OK] $msg" -ForegroundColor Green }
function Write-Warn2($msg){ Write-Host "    [!]  $msg" -ForegroundColor Yellow }
function Write-Err2($msg) { Write-Host "    [X]  $msg" -ForegroundColor Red }

function Fail($msg) {
    Write-Err2 $msg
    exit 1
}

# ----- 1. Pré-flight: deps + arquivos -----

Write-Step "Pré-flight: validando dependências e arquivos"

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Fail "docker CLI não encontrado no PATH. Instale Docker Desktop."
}
try { docker info --format '{{.ServerVersion}}' | Out-Null }
catch { Fail "Docker Engine não está respondendo. Inicie o Docker Desktop." }
Write-Ok "docker disponível"

if (-not (Get-Command cloudflared -ErrorAction SilentlyContinue)) {
    Fail "cloudflared não encontrado no PATH. Instale com `winget install --id Cloudflare.cloudflared`."
}
Write-Ok "cloudflared disponível"

if (-not (Test-Path $EnvFile)) {
    Fail ".env.e2e não encontrado. Copie:`n  cp scripts/e2e/.env.e2e.example scripts/e2e/.env.e2e`n  e preencha as credenciais reais."
}
Write-Ok ".env.e2e presente"

# ----- 2. Carrega .env.e2e -----

$envMap = @{}
Get-Content $EnvFile | ForEach-Object {
    $line = $_.Trim()
    if ($line -eq '' -or $line.StartsWith('#')) { return }
    $eq = $line.IndexOf('=')
    if ($eq -lt 1) { return }
    $k = $line.Substring(0, $eq).Trim()
    $v = $line.Substring($eq + 1).Trim()
    $envMap[$k] = $v
}

$required = @('MEGAAPI_HOST','MEGAAPI_INSTANCE','MEGAAPI_TOKEN',
              'CHATWOOT_URL','CHATWOOT_TOKEN','CHATWOOT_ACCOUNT','CHATWOOT_INBOX',
              'TENANT_SLUG','BRIDGE_HOST_PORT','POSTGRES_HOST_PORT','MASTER_KEY')
$missing = @()
foreach ($k in $required) {
    if (-not $envMap.ContainsKey($k) -or [string]::IsNullOrWhiteSpace($envMap[$k]) -or $envMap[$k].StartsWith('replace-me')) {
        $missing += $k
    }
}
if ($missing.Count -gt 0) {
    Fail "Variáveis ausentes ou com placeholder em .env.e2e:`n  - $($missing -join "`n  - ")"
}
Write-Ok "todas as variáveis obrigatórias presentes"

# Slug sanity check (mesma regex do CHECK em migrations/0001_init.sql)
if ($envMap['TENANT_SLUG'] -notmatch '^[a-z0-9][a-z0-9-]{2,63}$') {
    Fail "TENANT_SLUG '$($envMap['TENANT_SLUG'])' inválido. Regex: ^[a-z0-9][a-z0-9-]{2,63}$"
}

# ----- 3. Chatwoot dev reachable? -----

Write-Step "Verificando Chatwoot dev stack"
$cwRails = docker ps --filter 'name=chatwoot-dev-rails-1' --format '{{.Names}}'
if (-not $cwRails) {
    Fail "Container chatwoot-dev-rails-1 não está up. Suba com:`n  docker compose -f deploy/chatwoot.docker-compose.yml --env-file deploy/chatwoot.env up -d"
}
Write-Ok "chatwoot-dev-rails-1 up"

try {
    $cw = Invoke-WebRequest -Uri 'http://localhost:3000' -UseBasicParsing -TimeoutSec 5 -MaximumRedirection 0 -ErrorAction Stop
    Write-Ok "Chatwoot HTTP $($cw.StatusCode) em :3000"
} catch {
    # 302 redirect to /app é normal — qualquer resposta serve
    if ($_.Exception.Response.StatusCode.value__ -ge 200) {
        Write-Ok "Chatwoot respondeu (redirect/auth)"
    } else {
        Write-Warn2 "Chatwoot não respondeu em http://localhost:3000 — verifique container/porta"
    }
}

# ----- 4. Sincroniza .env do bridge na raiz com host ports / MASTER_KEY -----

Write-Step "Preparando .env do bridge stack"
$bridgeEnv = Join-Path $RepoRoot '.env'
$envLines = @(
    "MASTER_KEY=$($envMap['MASTER_KEY'])",
    "POSTGRES_USER=bridge",
    "POSTGRES_PASSWORD=bridge",
    "POSTGRES_DB=bridge",
    "POSTGRES_PORT=$($envMap['POSTGRES_HOST_PORT'])",
    "BRIDGE_PORT=8080",
    "BRIDGE_HOST_PORT=$($envMap['BRIDGE_HOST_PORT'])",
    "BRIDGE_IMAGE=bridge:latest",
    "LOG_LEVEL=info"
)
Set-Content -Path $bridgeEnv -Value $envLines -Encoding ASCII
Write-Ok ".env escrito em $bridgeEnv"

# ----- 5. Sobe bridge stack -----

Write-Step "Subindo bridge stack (docker compose up -d)"
Push-Location $RepoRoot
try {
    docker compose up -d --build 2>&1 | ForEach-Object { Write-Host "    $_" }
    if ($LASTEXITCODE -ne 0) { Fail "docker compose up falhou" }
} finally {
    Pop-Location
}

# ----- 6. Aguarda /healthz -----

Write-Step "Aguardando bridge /healthz (timeout ${HealthTimeoutSec}s)"
$bridgeBase = "http://localhost:$($envMap['BRIDGE_HOST_PORT'])"
$deadline = (Get-Date).AddSeconds($HealthTimeoutSec)
$healthy = $false
while ((Get-Date) -lt $deadline) {
    try {
        $h = Invoke-WebRequest -Uri "$bridgeBase/healthz" -UseBasicParsing -TimeoutSec 3 -ErrorAction Stop
        if ($h.StatusCode -eq 200) { $healthy = $true; break }
    } catch { Start-Sleep -Seconds 2 }
}
if (-not $healthy) {
    Write-Err2 "Bridge não ficou saudável. Logs recentes:"
    Push-Location $RepoRoot
    try { docker compose logs --tail 50 bridge | ForEach-Object { Write-Host "    $_" } }
    finally { Pop-Location }
    Fail "Abortando"
}
Write-Ok "Bridge healthy em $bridgeBase"

# ----- 7. Migrate -----

Write-Step "Aplicando migrations"
Push-Location $RepoRoot
try {
    docker compose exec -T bridge /bridge migrate 2>&1 | ForEach-Object { Write-Host "    $_" }
    if ($LASTEXITCODE -ne 0) { Fail "bridge migrate falhou" }
} finally {
    Pop-Location
}
Write-Ok "migrations aplicadas"

# ----- 8. Tenant: verifica se já existe -----

Write-Step "Provisionando tenant '$($envMap['TENANT_SLUG'])'"
$slug = $envMap['TENANT_SLUG']
$existing = docker compose --project-directory $RepoRoot exec -T db psql -U bridge -d bridge -tA -c "SELECT id FROM tenants WHERE slug='$slug';" 2>$null
$existing = ($existing | Out-String).Trim()

if ($existing) {
    Write-Warn2 "Tenant slug '$slug' já existe (id=$existing)"
    if (Test-Path $CredsFile) {
        $creds = Get-Content $CredsFile -Raw | ConvertFrom-Json
        if ($creds.slug -eq $slug) {
            $webhookBearer = $creds.webhookBearer
            $hmacSecret = $creds.hmacSecret
            $tenantId = $creds.tenantId
            Write-Ok "Reusando credenciais salvas em tenant-creds.json"
        } else {
            Fail "tenant-creds.json é de outro slug ('$($creds.slug)'). Apague-o ou rode com slug diferente."
        }
    } else {
        Fail "Tenant '$slug' existe na DB mas tenant-creds.json não foi encontrado. Webhook bearer + HMAC secret estão criptografados e não são recuperáveis. Apague o tenant:`n  docker compose exec db psql -U bridge -d bridge -c `"DELETE FROM tenants WHERE slug='$slug';`"`ne rode setup.ps1 novamente."
    }
} else {
    $tenantArgs = @(
        'compose', 'exec', '-T', 'bridge', '/bridge', 'tenant', 'add',
        '--slug', $slug,
        '--megaapi-host', $envMap['MEGAAPI_HOST'],
        '--megaapi-instance', $envMap['MEGAAPI_INSTANCE'],
        '--megaapi-token', $envMap['MEGAAPI_TOKEN'],
        '--chatwoot-url', $envMap['CHATWOOT_URL'],
        '--chatwoot-token', $envMap['CHATWOOT_TOKEN'],
        '--chatwoot-account', $envMap['CHATWOOT_ACCOUNT'],
        '--chatwoot-inbox', $envMap['CHATWOOT_INBOX'],
        '--skip-reach-check'
    )
    Push-Location $RepoRoot
    try {
        $tenantOut = & docker @tenantArgs 2>&1
        $tenantExit = $LASTEXITCODE
    } finally {
        Pop-Location
    }
    $tenantOut | ForEach-Object { Write-Host "    $_" }
    if ($tenantExit -ne 0) { Fail "bridge tenant add falhou" }

    # Parse CLI output: "Tenant created: <uuid>\nWebhook Bearer: <b>\nHMAC Secret: <s>"
    $tenantId = ($tenantOut | Select-String -Pattern 'Tenant created:\s*(\S+)').Matches.Groups[1].Value
    $webhookBearer = ($tenantOut | Select-String -Pattern 'Webhook Bearer:\s*(\S+)').Matches.Groups[1].Value
    $hmacSecret = ($tenantOut | Select-String -Pattern 'HMAC Secret:\s*(\S+)').Matches.Groups[1].Value

    if (-not $webhookBearer -or -not $hmacSecret) {
        Fail "Não consegui parsear Webhook Bearer / HMAC Secret da saída do CLI. Veja log acima."
    }

    # Salva pra reuso em re-runs (gitignored)
    $credsObj = [pscustomobject]@{
        slug          = $slug
        tenantId      = $tenantId
        webhookBearer = $webhookBearer
        hmacSecret    = $hmacSecret
        createdAt     = (Get-Date).ToString('o')
    }
    $credsObj | ConvertTo-Json | Set-Content -Path $CredsFile -Encoding ASCII
    Write-Ok "tenant criado (id=$tenantId), credenciais salvas em tenant-creds.json"
}

# ----- 9. Cloudflared quick tunnel -----

Write-Step "Iniciando Cloudflare quick tunnel"

$existingTunnel = Get-Process -Name 'cloudflared' -ErrorAction SilentlyContinue
$tunnelUrl = $null

if ($existingTunnel -and $ReuseTunnel -and (Test-Path $TunnelUrlFile)) {
    $tunnelUrl = (Get-Content $TunnelUrlFile -Raw).Trim()
    Write-Ok "Reusando tunnel existente: $tunnelUrl"
} else {
    if ($existingTunnel) {
        Write-Warn2 "Encontrado cloudflared rodando — encerrando antes de iniciar novo (use -ReuseTunnel para evitar)"
        $existingTunnel | Stop-Process -Force
        Start-Sleep -Seconds 2
    }

    if (Test-Path $TunnelLog) { Remove-Item $TunnelLog -Force }
    $tunnelProc = Start-Process -FilePath 'cloudflared' `
        -ArgumentList @('tunnel','--no-autoupdate','--url',$bridgeBase) `
        -RedirectStandardOutput $TunnelLog `
        -RedirectStandardError "$TunnelLog.err" `
        -WindowStyle Hidden -PassThru

    # Captura URL do log (cloudflared escreve em stderr — combinamos depois)
    $urlRegex = 'https://[a-z0-9-]+\.trycloudflare\.com'
    $deadline = (Get-Date).AddSeconds(30)
    while ((Get-Date) -lt $deadline -and -not $tunnelUrl) {
        Start-Sleep -Seconds 1
        $logContent = ''
        if (Test-Path $TunnelLog) { $logContent += (Get-Content $TunnelLog -Raw -ErrorAction SilentlyContinue) }
        if (Test-Path "$TunnelLog.err") { $logContent += (Get-Content "$TunnelLog.err" -Raw -ErrorAction SilentlyContinue) }
        $m = [regex]::Match($logContent, $urlRegex)
        if ($m.Success) { $tunnelUrl = $m.Value }
    }

    if (-not $tunnelUrl) {
        Write-Err2 "Tunnel não emitiu URL pública em 30s. Log:"
        if (Test-Path $TunnelLog) { Get-Content $TunnelLog | ForEach-Object { Write-Host "    $_" } }
        if (Test-Path "$TunnelLog.err") { Get-Content "$TunnelLog.err" | ForEach-Object { Write-Host "    $_" } }
        Fail "Abortando"
    }
    Set-Content -Path $TunnelUrlFile -Value $tunnelUrl -Encoding ASCII
    Write-Ok "Tunnel ativo: $tunnelUrl (pid=$($tunnelProc.Id))"
}

# Sanity: tunnel atinge bridge
try {
    $tunnelHealth = Invoke-WebRequest -Uri "$tunnelUrl/healthz" -UseBasicParsing -TimeoutSec 10
    Write-Ok "Tunnel -> bridge /healthz $($tunnelHealth.StatusCode)"
} catch {
    Write-Warn2 "Tunnel não alcançou /healthz ainda — pode levar mais alguns segundos para propagar"
}

# ----- 10. Bloco final de instruções -----

$webhookWa = "$tunnelUrl/v1/wa/$slug"
$webhookCw = "$tunnelUrl/v1/cw/$slug"

Write-Host ""
Write-Host "════════════════════════════════════════════════════════════" -ForegroundColor Magenta
Write-Host " E2E READY — configure os webhooks abaixo nas UIs externas" -ForegroundColor Magenta
Write-Host "════════════════════════════════════════════════════════════" -ForegroundColor Magenta
Write-Host ""
Write-Host " [1] megaAPI dashboard (instance $($envMap['MEGAAPI_INSTANCE']))" -ForegroundColor White
Write-Host "     URL:    $webhookWa"
Write-Host "     Header: Authorization: Bearer $webhookBearer"
Write-Host "     Eventos: message, message.upsert (whatever sua versão expõe)"
Write-Host ""
Write-Host " [2] Chatwoot Inbox $($envMap['CHATWOOT_INBOX']) → Settings → Integrations → Webhooks" -ForegroundColor White
Write-Host "     URL:         $webhookCw"
Write-Host "     HMAC Secret: $hmacSecret"
Write-Host "     (cole o secret em Inbox → Configuration → 'HMAC Verification')"
Write-Host ""
Write-Host " Tenant ID    : $tenantId"
Write-Host " Tunnel URL   : $tunnelUrl"
Write-Host " Bridge local : $bridgeBase"
Write-Host ""
Write-Host " Próximos passos:" -ForegroundColor Cyan
Write-Host "   .\scripts\e2e\watch.ps1                      # tail logs + DB"
Write-Host "   .\scripts\e2e\smoke.ps1 -Phone <e164> -Text 'oi'   # teste outbound"
Write-Host "   .\scripts\e2e\teardown.ps1                   # parar tunnel"
Write-Host ""
Write-Host "════════════════════════════════════════════════════════════" -ForegroundColor Magenta
