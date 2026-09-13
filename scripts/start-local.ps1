# Local local profile: everything needed to show the replay flow on this machine.
#
# The only thing it changes from the defaults is the replay policy: 127.0.0.1 is
# allow-listed so events can be replayed into scripts/test-receiver.mjs. Keep this
# profile off anything that is reachable from outside the machine.
#
# Usage:
#   ./scripts/start-local.ps1            # http://127.0.0.1:8080
#   ./scripts/start-local.ps1 -Port 8081
#   ./scripts/start-local.ps1 -PublicUrlRoot http://30.23.54.80:8080

param(
    [uint16]$Port = 8080,
    [string]$PublicUrlRoot = ''
)

$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

if (-not $env:DATABASE_URL) {
    $env:DATABASE_URL = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
}

if (-not (Test-Path (Join-Path $root 'wh.exe'))) {
    Write-Host 'building wh.exe ...' -ForegroundColor DarkGray
    & go build -o wh.exe ./cmd/webhook-tester
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

$args = @(
    'start',
    '--port', $Port,
    '--replay-allow-host', '127.0.0.1',
    '--replay-allow-private',
    '--replay-timeout', '3s',
    '--replay-max-retries', '2',
    '--retention-interval', '10m'
)

if ($PublicUrlRoot) {
    $args += @('--public-url-root', $PublicUrlRoot)
}

Write-Host "local profile on http://127.0.0.1:$Port (local replay allowed)" -ForegroundColor Cyan
Write-Host "receiver:  node scripts/test-receiver.mjs 9099" -ForegroundColor DarkGray
Write-Host "script:    pwsh ./scripts/local.ps1" -ForegroundColor DarkGray
Write-Host ''

& (Join-Path $root 'wh.exe') @args
