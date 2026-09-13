#Requires -Version 7
<#
.SYNOPSIS
    Starts the webhook replay platform (database + API + UI) with one command.

.DESCRIPTION
    * Production-like mode (default): builds/serves the embedded SPA from web/dist.
    * Development mode (-Dev): runs the Vite dev server on 8081 with an API proxy and the
      Go server on 8080, so frontend changes hot reload.

.EXAMPLE
    ./dev.ps1
    ./dev.ps1 -Dev -Port 8080
#>
param(
    [int]$Port = 8080,
    [int]$DevPort = 8081,
    [switch]$Dev,
    [string]$DatabaseUrl = $env:DATABASE_URL
)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot

if (-not $DatabaseUrl) {
    $DatabaseUrl = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
    Write-Host "DATABASE_URL is not set, using the local default: $DatabaseUrl" -ForegroundColor Yellow
}

$env:DATABASE_URL = $DatabaseUrl

$go = 'go'
if (-not (Get-Command $go -ErrorAction SilentlyContinue)) {
    $go = 'C:\Program Files\Go\bin\go.exe'
}

Push-Location $root

try {
    Write-Host 'Building the frontend (embedded into the binary)...' -ForegroundColor Cyan

    if (-not (Test-Path (Join-Path $root 'web/node_modules'))) {
        & npm --prefix (Join-Path $root 'web') install --no-audit --no-fund
    }

    & npm --prefix (Join-Path $root 'web') run build
    if ($LASTEXITCODE -ne 0) { throw 'frontend build failed' }

    if ($Dev) {
        Write-Host "Starting API on :$Port and Vite dev server on :$DevPort ..." -ForegroundColor Cyan

        $vite = Start-Process -FilePath 'npm' -ArgumentList '--prefix', (Join-Path $root 'web'), 'run', 'serve', '--', '--port', $DevPort -PassThru

        try {
            & $go run ./cmd/webhook-tester start --port $Port --use-live-frontend
        }
        finally {
            Stop-Process -Id $vite.Id -Force -ErrorAction SilentlyContinue
        }
    }
    else {
        Write-Host "Starting on http://127.0.0.1:$Port (API + UI)" -ForegroundColor Cyan

        & $go run ./cmd/webhook-tester start --port $Port
    }
}
finally {
    Pop-Location
}
