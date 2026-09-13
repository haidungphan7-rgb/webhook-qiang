#!/usr/bin/env pwsh
<#
.SYNOPSIS
    Windows entry point for the tasks in the Makefile.

.DESCRIPTION
    A stock Windows has no `make` (not even gmake), so every README instruction that starts
    with `make <target>` fails on the first command a new user runs. This script is the
    Windows twin of the Makefile: same targets, same names, no extra dependencies.

    The Makefile stays the entry point on Linux/macOS. Both must be kept in sync - if you
    add a target there, add it here.

.EXAMPLE
    ./make.ps1 build
    ./make.ps1 test
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Target = 'help'
)

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

# The official Go installer puts go on PATH, but a manual or zip install does not - and
# the failure it produces ("The term 'go' is not recognized") is exactly the kind of
# message that leaves a newcomer stuck. Look in the default location first, then say what
# to do instead of failing later with a blank red error.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $candidate = Join-Path $env:ProgramFiles 'Go\bin\go.exe'

    if (Test-Path $candidate) {
        $env:Path = (Split-Path $candidate) + [IO.Path]::PathSeparator + $env:Path
    }
}

function Assert-Tool {
    param([string]$Name, [string]$HowToInstall)

    if (Get-Command $Name -ErrorAction SilentlyContinue) {
        return
    }

    Write-Host "找不到 $Name。$HowToInstall" -ForegroundColor Red
    exit 1
}

# Stamped into the binary so a running instance can answer "which build am I?".
# Mirrors the Makefile: `git describe` gives the tag, or the short commit hash when there
# is none; -dirty catches a binary built from modified sources.
$version = 'dev'
if (Get-Command git -ErrorAction SilentlyContinue) {
    $tag = (& git describe --tags --always --dirty 2>$null)
    if ($LASTEXITCODE -eq 0 -and $tag) { $version = $tag.Trim() }
}

$buildTime = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$pkg = 'github.com/yuandzhang/webhook-zq/internal/version'
$ldflags = "-s -w -X '$pkg.version=$version' -X '$pkg.buildTime=$buildTime'"

function Invoke-Step {
    param([string]$Label, [scriptblock]$Body)

    Write-Host "==> $Label" -ForegroundColor Cyan
    & $Body

    if ($LASTEXITCODE -ne 0) {
        throw "$Label failed (exit $LASTEXITCODE)"
    }
}

switch ($Target) {
    'help' {
        Write-Host 'Available targets (same names as the Makefile):' -ForegroundColor Yellow
        @(
            '  build          Build the server binary (frontend is embedded at build time)',
            '  frontend       Build the single page application into web/dist',
            '  test           Go unit tests + frontend tests',
            '  test-go        Go unit tests (no database required)',
            '  test-web       Frontend unit tests',
            '  accept         End-to-end acceptance (needs PostgreSQL; Windows only)',
            '  dev            Start API + UI with one command',
            '  run            Run the server (set DATABASE_URL first)',
            '  migrate        Apply the schema without starting the server',
            '  migrate-check  Fail if the database is not at the expected schema version',
            '  seed           Load the demo data (needs psql and DATABASE_URL)',
            '  clean          Remove build artefacts'
        ) | ForEach-Object { Write-Host $_ }
    }

    'build' {
        Assert-Tool go '安装 Go 1.26+（https://go.dev/dl/），或把它的 bin 目录加进 PATH。'

        # web/embed.go does //go:embed dist, so a clean clone - which has no web/dist yet -
        # would fail to build. The generator writes the placeholder only when the file is
        # absent, so it is safe to run every time and never clobbers a real build.
        Invoke-Step 'go generate ./web/...' { & go generate ./web/... }
        Invoke-Step "go build (version $version)" {
            & go build -trimpath -ldflags $ldflags -o ./bin/webhook-zq.exe ./cmd/webhook-tester
        }
        Write-Host "Built ./bin/webhook-zq.exe ($version)" -ForegroundColor Green
    }

    'frontend' {
        Invoke-Step 'npm install' { & npm --prefix ./web install --no-audit --no-fund }
        Invoke-Step 'npm run build' { & npm --prefix ./web run build }
    }

    'test' {
        & $PSCommandPath test-go
        & $PSCommandPath test-web
    }

    'test-go' {
        Assert-Tool go '安装 Go 1.26+（https://go.dev/dl/），或把它的 bin 目录加进 PATH。'

        # Two deliberate differences from the Makefile's test-go:
        #  - no -race: it needs cgo, and a stock Windows has no gcc, so the Makefile target
        #    fails here with "-race requires cgo". Race detection runs in CI (Linux).
        #  - not ./...: that walks web/node_modules, which contains Go sources without a
        #    go.mod of their own, so the module treats them as part of this project.
        Invoke-Step 'go test' { & go test ./internal/... ./cmd/... }
    }

    'test-web' {
        Invoke-Step 'npm run test' { & npm --prefix ./web run test }
    }

    'accept' {
        # Windows only: the scripts use Get-NetTCPConnection. They are skipped rather than
        # failed elsewhere - a check that cannot pass is worse than no check.
        if (-not $IsWindows -and $PSVersionTable.PSVersion.Major -lt 6) {
            Write-Host 'skip: acceptance needs Windows PowerShell (Get-NetTCPConnection)' -ForegroundColor Yellow
            exit 0
        }

        foreach ($s in 'b1-inbox-capture', 'b2-replay', 'b3-retention-tenant', 'demo-path') {
            Invoke-Step $s { & pwsh "./scripts/acceptance/$s.ps1" }
        }
    }

    'dev' {
        & pwsh ./dev.ps1
    }

    'run' {
        & go run ./cmd/webhook-tester start
    }

    'migrate' {
        & go run ./cmd/webhook-tester migrate --database-url $env:DATABASE_URL
    }

    'migrate-check' {
        & go run ./cmd/webhook-tester migrate --check --database-url $env:DATABASE_URL
    }

    'seed' {
        & psql $env:DATABASE_URL -f scripts/sample-data.sql
    }

    'clean' {
        Remove-Item -Recurse -Force ./bin, ./web/dist -ErrorAction SilentlyContinue
    }

    default {
        Write-Host "Unknown target '$Target'. Run './make.ps1 help'." -ForegroundColor Red
        exit 1
    }
}
