# Verifies the "one click demo" path end to end.
#
# The point of this test: on an instance started with --replay-allow-host 127.0.0.1
# --replay-allow-private, "replay to my machine works" AND "replay to a cloud metadata
# address is refused" must both hold. If either breaks, the demo cannot be delivered,
# and the SSRF gate is broken in one direction or the other.
#
#   pwsh ./scripts/acceptance/demo-path.ps1

$ErrorActionPreference = 'Continue'
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$exe = Join-Path $root 'bin/webhook-zq.exe'
if (-not (Test-Path $exe)) { $exe = Join-Path $root 'webhook-zq.exe' }

if (-not (Test-Path $exe)) {
    Write-Host 'FAIL: no binary - run the installer or `go build` first' -ForegroundColor Red
    exit 1
}

if (-not $env:DATABASE_URL) {
    $env:DATABASE_URL = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
}
# For any psql the server or a helper script might spawn: without it a password prompt
# parks on stdin forever in a hidden window.
if (-not $env:PGPASSWORD) { $env:PGPASSWORD = 'postgres' }

$script:pass = 0
$script:fail = 0

function Chk($name, $ok, $detail) {
    if ($ok) {
        $script:pass++
        Write-Host "PASS  $name  $detail" -ForegroundColor Green
    }
    else {
        $script:fail++
        Write-Host "FAIL  $name  $detail" -ForegroundColor Red
    }
}

# Pick a free port so repeated runs never collide with a real instance.
$port = 8081
while (Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue) { $port++ }

New-Item -ItemType Directory -Force -Path (Join-Path $root 'logs') | Out-Null

Start-Process node -ArgumentList (Join-Path $root 'scripts/test-receiver.mjs'), '9099' `
    -WorkingDirectory $root -WindowStyle Hidden `
    -RedirectStandardOutput (Join-Path $root 'logs/demo-rx.log') `
    -RedirectStandardError (Join-Path $root 'logs/demo-rx.err.log')

Start-Process $exe -ArgumentList 'start', '--port', $port, '--replay-allow-host', '127.0.0.1',
'--replay-allow-private', '--replay-timeout', '3s' -WindowStyle Hidden `
    -RedirectStandardOutput (Join-Path $root 'logs/demo-server.log') `
    -RedirectStandardError (Join-Path $root 'logs/demo-server.err.log')

try {
    $up = $false
    for ($i = 0; $i -lt 40; $i++) {
        Start-Sleep -Milliseconds 700
        try {
            $r = Invoke-WebRequest "http://127.0.0.1:$port/healthz" -UseBasicParsing -TimeoutSec 2
            if ($r.StatusCode -eq 200) { $up = $true; break }
        }
        catch { }
    }

    Chk 'demo instance starts' $up "port $port"

    if (-not $up) {
        Write-Host 'PASS=0 FAIL=1'
        exit 1
    }

    $inbox = Invoke-RestMethod -Method Post "http://127.0.0.1:$port/api/v1/inboxes" `
        -ContentType 'application/json' -Body '{"name":"演示"}'
    Chk 'demo inbox created' ($inbox.id -ne $null) "token len $($inbox.token.Length)"

    $ev = Invoke-RestMethod -Method Post "http://127.0.0.1:$port/hooks/$($inbox.token)/demo" `
        -ContentType 'application/json' -Body '{"hello":"demo"}'
    Chk 'capture works (能收)' ($ev.id -ne $null) "event $($ev.id)"

    # Built with ConvertTo-Json rather than a hand written JSON string: quoting a body for
    # a native command differs between PowerShell versions and silently produced
    # invalid_json on 5.1. The reply of a refused replay (403) is read from
    # ErrorDetails - the same shape used by the other acceptance scripts.
    function Invoke-Replay($target) {
        try {
            $r = Invoke-WebRequest -Method Post "http://127.0.0.1:$port/api/v1/events/$($ev.id)/replay" `
                -ContentType 'application/json' -Body (ConvertTo-Json @{ target_url = $target }) `
                -UseBasicParsing -TimeoutSec 30

            return $r.Content
        }
        catch {
            return $_.ErrorDetails.Message
        }
    }

    $ok1 = (Invoke-Replay 'http://127.0.0.1:9099/ok') | ConvertFrom-Json
    Chk 'replay to local receiver (能放)' ($ok1.last.outcome -eq 'success' -and $ok1.last.status_code -eq 200) `
        "$($ok1.last.outcome)/$($ok1.last.status_code) in $($ok1.last.duration_ms)ms"

    $ok2 = (Invoke-Replay 'http://169.254.169.254/latest/meta-data/') | ConvertFrom-Json
    Chk 'replay to metadata address refused (能挡)' ($ok2.error.code -eq 'target_blocked') `
        "$($ok2.error.code)"

    $hist = Invoke-RestMethod "http://127.0.0.1:$port/api/v1/events/$($ev.id)/replays"
    $outcomes = ($hist.items | ForEach-Object { $_.outcome }) -join ','
    Chk 'both attempts recorded (限制可验证)' ($outcomes -match 'blocked' -and $outcomes -match 'success') $outcomes
}
finally {
    foreach ($p in 9099, $port) {
        $c = Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue
        if ($c) { Stop-Process -Id $c.OwningProcess -Force -ErrorAction SilentlyContinue }
    }

    Start-Sleep -Seconds 1
}

Write-Host "PASS=$script:pass  FAIL=$script:fail"
if ($script:fail -gt 0) { exit 1 }
