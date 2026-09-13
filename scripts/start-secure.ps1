# Hardened profile: access control on, secrets encrypted at rest, no private targets.
#
# This is the profile to use the moment the instance is reachable by more than one
# person. It prints the generated credentials once - there is no way to get the
# encryption key back later, because it never leaves this process.
#
# Usage:
#   ./scripts/start-secure.ps1                     # single shared token
#   ./scripts/start-secure.ps1 -Tenants alice,bob  # per tenant keys
#   ./scripts/start-secure.ps1 -Port 8080

param(
    [uint16]$Port = 8080,
    [string[]]$Tenants = @(),
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

# Accept both `-Tenants alice,bob` and `-Tenants alice -Tenants bob`; without this a
# single "alice,bob" would be joined into one "alice,bob:<key>" entry.
if ($Tenants.Count -gt 0) {
    $Tenants = $Tenants | ForEach-Object { $_ -split ',' } | Where-Object { $_.Trim() -ne '' } | ForEach-Object { $_.Trim() }
}

function New-Secret([int]$Bytes) {
    $buf = New-Object byte[] $Bytes
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($buf)

    return [Convert]::ToBase64String($buf)
}

# The master key must be exactly 32 bytes; anything else is rejected at startup.
$env:ENCRYPT_KEY = New-Secret 32

$args = @(
    'start',
    '--port', $Port,
    '--replay-timeout', '5s'
)

# The key travels in the environment, never on the command line: the flag exists, but a
# 32 byte master key in argv is readable by every user via task manager. The binary
# reads ENCRYPT_KEY on its own (--encrypt-key has an env fallback).

if ($PublicUrlRoot) {
    $args += @('--public-url-root', $PublicUrlRoot)
}

if ($Tenants.Count -gt 0) {
    $spec = @()

    foreach ($t in $Tenants) {
        $key = New-Secret 24
        $spec += "$t`:$key"
    }

    # One flag per tenant: --auth-keys is repeatable, and passing a comma joined
    # value would be split again and lose the "<tenant>:<key>" shape.
    foreach ($entry in $spec) {
        $args += @('--auth-keys', $entry)
    }
}
else {
    $token = New-Secret 24
    $args += @('--auth-token', $token)
}

Write-Host ''
Write-Host '=== credentials (shown once) ===' -ForegroundColor Yellow

if ($Tenants.Count -gt 0) {
    Write-Host ($spec -join "`n") -ForegroundColor White
    Write-Host ''
    Write-Host 'sign in from the UI: help menu -> 访问密钥 -> paste the part after the colon' -ForegroundColor DarkGray
}
else {
    Write-Host "auth-token: $token" -ForegroundColor White
    Write-Host ''
    Write-Host 'sign in from the UI: help menu -> 访问密钥' -ForegroundColor DarkGray
}

Write-Host "encrypt-key: $env:ENCRYPT_KEY   (32 bytes; losing it means stored secrets cannot be decrypted)" -ForegroundColor DarkGray
Write-Host 'private/link-local replay targets stay blocked in this profile' -ForegroundColor DarkGray
Write-Host ''

& (Join-Path $root 'wh.exe') @args
