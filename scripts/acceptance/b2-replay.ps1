# Acceptance B2: replay outcomes (B35-B46).
#
# Self-contained: starts its own server instance and its own test receivers, and stops
# everything it started. Nothing is shared with the 8080 demo instance, which is what
# makes the run repeatable:
#
#   * the replay rate limiter is an in-process fixed window (internal/ratelimit), so a
#     fresh instance means a fresh window - running b2 twice in a row no longer fails
#     the second time on a 429 left over from the first;
#   * the receivers on 9098 (broken body) and 9097 (oversized body) are started by this
#     script, so B40/B41 cannot fail just because nobody happened to be listening.
#
# Usage:
#   pwsh ./scripts/acceptance/b2-replay.ps1
#
# Requires bin/webhook-zq.exe (or webhook-zq.exe in the repo root), PostgreSQL on
# 127.0.0.1:5432 and node on PATH.

$ErrorActionPreference = 'Continue'

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
# For any psql a helper might spawn: without it a password prompt parks on stdin forever.
if (-not $env:PGPASSWORD) { $env:PGPASSWORD = 'postgres' }

$pass = 0; $fail = 0
$started = @()

function Req {
    param([string]$M = 'GET', [string]$U, [hashtable]$H = @{}, [string]$B)
    $p = @{ Method = $M; Uri = $U; UseBasicParsing = $true; TimeoutSec = 120 }
    if ($H.Count -gt 0) { $p.Headers = $H }
    if ($B) { $p.ContentType = 'application/json'; $p.Body = $B }
    try {
        $r = Invoke-WebRequest @p
        return @{ Code = [int]$r.StatusCode; Body = [string]$r.Content }
    }
    catch {
        $code = 0; $body = ''
        if ($_.Exception.Response) { $code = [int]$_.Exception.Response.StatusCode }
        if ($_.ErrorDetails.Message) { $body = $_.ErrorDetails.Message }
        return @{ Code = $code; Body = $body }
    }
}
function J($r) { try { $r.Body | ConvertFrom-Json } catch { $null } }
function Chk($id, $ok, $detail) {
    if ($ok) { $script:pass++; Write-Host "PASS  $id" -ForegroundColor Green }
    else { $script:fail++; Write-Host "FAIL  $id  -> $detail" -ForegroundColor Red }
}

function Wait-Port([int]$Port, [int]$Seconds = 15) {
    for ($i = 0; $i -lt $Seconds; $i++) {
        if (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) { return $true }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

Write-Host '=== B2 replay ===' -ForegroundColor Cyan

# ── receivers: 9099 normal, 9098 partial body, 9097 oversized body ──────────
$receivers = @(
    @{ Port = 9099; Mode = 'normal' },
    @{ Port = 9098; Mode = 'partial' },
    @{ Port = 9097; Mode = 'big' }
)
$rx = Join-Path $root 'scripts/test-receiver.mjs'
$logDir = Join-Path $root 'logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null

foreach ($r in $receivers) {
    if (Get-NetTCPConnection -LocalPort $r.Port -State Listen -ErrorAction SilentlyContinue) {
        Write-Host "FAIL: port $($r.Port) is already taken - another receiver left running?" -ForegroundColor Red
        exit 1
    }
    $p = Start-Process node -ArgumentList $rx, $r.Port, $r.Mode `
        -WorkingDirectory $root -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $logDir "b2-rx-$($r.Port).log") `
        -RedirectStandardError (Join-Path $logDir "b2-rx-$($r.Port).err.log")
    $script:started += $p
    if (-not (Wait-Port $r.Port)) {
        Write-Host "FAIL: receiver on $($r.Port) ($($r.Mode)) did not come up" -ForegroundColor Red
        foreach ($q in $script:started) { Stop-Process -Id $q.Id -Force -ErrorAction SilentlyContinue }
        exit 1
    }
}

# ── server: free port, local replay allowed, 3s timeout, explicit rate limit ─
$port = 8095
while (Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue) { $port++ }

$server = Start-Process $exe -ArgumentList @('start', '--port', $port,
    '--replay-allow-host', '127.0.0.1', '--replay-allow-private',
    '--replay-timeout', '3s', '--replay-rate-limit', '30') `
    -WorkingDirectory $root -WindowStyle Hidden -PassThru `
    -RedirectStandardOutput (Join-Path $logDir 'b2-server.log') `
    -RedirectStandardError (Join-Path $logDir 'b2-server.err.log')
$script:started += $server

$base = "http://127.0.0.1:$port"
$up = $false
for ($i = 0; $i -lt 40; $i++) {
    Start-Sleep -Milliseconds 500
    try {
        $r = Invoke-WebRequest "$base/healthz" -UseBasicParsing -TimeoutSec 2
        if ($r.StatusCode -eq 200) { $up = $true; break }
    }
    catch { }
}

try {
    Chk 'server starts' $up "port $port"
    if (-not $up) { Write-Host 'PASS=0 FAIL=1'; exit 1 }

    $s = Invoke-RestMethod "$base/api/v1/settings"
    $to = $s.replay_timeout_ms
    Write-Host "settings: timeout=$to preview=$($s.replay_max_preview) rate=$($s.replay_rate_limit)"

    $inb = J (Req -M POST -U "$base/api/v1/inboxes" -B '{"name":"b2"}')
    $ID = $inb.id; $T = $inb.token
    $ev = J (Req -M POST -U "$base/hooks/$T/sec" -H @{ 'authorization' = 'Bearer top-secret' } -B '{"event":"paid","amount":42.5}')
    $EID = $ev.id
    $RP = "$base/api/v1/events/$EID/replay"

    # ── B35 success
    $r = Req -M POST -U $RP -B '{ "target_url":"http://127.0.0.1:9099/ok" }'
    $j = J $r
    Chk 'B35 success 200 + outcome=success' ($r.Code -eq 200 -and $j.last.outcome -eq 'success' -and $j.last.status_code -eq 200 -and $j.last.duration_ms -gt 0) "code=$($r.Code) outcome=$($j.last.outcome) sc=$($j.last.status_code)"

    # ── B37 http_error
    $r = Req -M POST -U $RP -B '{ "target_url":"http://127.0.0.1:9099/fail" }'
    $j = J $r
    Chk 'B37 http_error keeps status 500' ($r.Code -eq 200 -and $j.ok -eq $false -and $j.last.outcome -eq 'http_error' -and $j.last.status_code -eq 500) "code=$($r.Code) ok=$($j.ok) outcome=$($j.last.outcome) sc=$($j.last.status_code)"

    # ── B38 timeout
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $r = Req -M POST -U $RP -B '{ "target_url":"http://127.0.0.1:9099/slow" }'
    $sw.Stop()
    $j = J $r
    $d = $j.last.duration_ms
    Chk 'B38 timeout + duration near the configured timeout' ($j.last.outcome -eq 'timeout' -and $d -ge $to * 0.8 -and $d -le $to * 1.5) "outcome=$($j.last.outcome) duration=$d (timeout=$to)"

    # ── B39 network_error
    $r = Req -M POST -U $RP -B '{ "target_url":"http://127.0.0.1:9999/nope" }'
    $j = J $r
    Chk 'B39 network_error (nothing listening)' ($j.last.outcome -eq 'network_error') "outcome=$($j.last.outcome) err=$($j.last.error)"

    # ── B40 2xx but the body never arrived
    $r = Req -M POST -U $RP -B '{ "target_url":"http://127.0.0.1:9098/" }'
    $j = J $r
    $l = $j.last
    Chk 'B40 2xx + unreadable body -> network_error' ($l.status_code -eq 200 -and $l.outcome -eq 'network_error') "sc=$($l.status_code) outcome=$($l.outcome)"
    Chk 'B40 error explains the read failure' ($l.error -match 'cannot read response body') "err=$($l.error)"
    Chk 'B40 partial body kept + truncated' ($l.preview_truncated -eq $true -and $l.response_preview -eq 'partial') "trunc=$($l.preview_truncated) preview=$($l.response_preview)"
    Chk 'B40 ok=false' ($j.ok -eq $false) "ok=$($j.ok)"

    # ── B41 preview truncation
    $r = Req -M POST -U $RP -B '{ "target_url":"http://127.0.0.1:9097/big" }'
    $l = (J $r).last
    Chk 'B41 preview cut at replay_max_preview' ($l.outcome -eq 'success' -and $l.preview_truncated -eq $true -and $l.response_preview.Length -eq $s.replay_max_preview) "len=$($l.response_preview.Length) expected=$($s.replay_max_preview) trunc=$($l.preview_truncated)"

    # ── B42 blocked, and the refusal is recorded
    $r = Req -M POST -U $RP -B '{ "target_url":"http://169.254.169.254/latest/meta-data/" }'
    $j = J $r
    $attempt = if ($j.attempts) { $j.attempts[0].outcome } else { '' }
    Chk 'B42 blocked -> 403 with the attempt in the body' ($r.Code -eq 403 -and $attempt -eq 'blocked') "code=$($r.Code) attempt=$attempt"
    $hist = (Invoke-RestMethod "$base/api/v1/events/$EID/replays").items
    $hasBlocked = @($hist | Where-Object { $_.outcome -eq 'blocked' }).Count
    Chk 'B42 refusal is persisted (the policy is verifiable)' ($hasBlocked -ge 1) "blocked_records=$hasBlocked"

    # ── B44 invalid targets
    $r = Req -M POST -U $RP -B '{ "target_url":"ftp://example.com/h" }'
    Chk 'B44a ftp -> 400 invalid_target' ($r.Code -eq 400 -and (J $r).error.code -eq 'invalid_target') "code=$($r.Code) $((J $r).error.message)"
    $r = Req -M POST -U $RP -B '{ "target_url":"http://u:p@example.com/" }'
    Chk 'B44b userinfo -> 400' ($r.Code -eq 400 -and (J $r).error.message -match 'user info') "code=$($r.Code) $((J $r).error.message)"
    $r = Req -M POST -U $RP -B '{ "target_url":"   " }'
    Chk 'B44c blank -> 422 validation_error' ((J $r).error.code -eq 'validation_error') "code=$($r.Code) $((J $r).error.message)"
    $r = Req -M POST -U $RP -B '{ "target_url":"http://127.0.0.1:9099/ok","body_base64":"!!!" }'
    Chk 'B44d bad base64 -> 422' ($r.Code -eq 422) "code=$($r.Code) $((J $r).error.message)"

    # ── B45 edited replay
    $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes('{"a":999}'))
    $hdrs = '{"name":"X-Trace-Id","value":"t2"},{"name":"authorization","value":"Bearer nope"}'
    $r = Req -M POST -U $RP -B "{ `"target_url`":`"http://127.0.0.1:9099/ok`",`"body_base64`":`"$b64`",`"headers`":[$hdrs] }"
    $j = J $r
    Chk 'B45 edited flag set' ($j.last.edited -eq $true -and $j.ok -eq $true) "edited=$($j.last.edited) ok=$($j.ok)"
    $orig = (J (Req -U "$base/api/v1/events/$EID")).body_base64
    Chk 'B45 original event body untouched' ($orig -ne $b64) "unchanged=$($orig -ne $b64)"

    # ── B46 rate limit (run last: it exhausts the window)
    if ($s.replay_rate_limit -gt 0 -and $s.replay_rate_limit -le 60) {
        $limit = $s.replay_rate_limit
        $seen429 = $false
        for ($i = 1; $i -le $limit + 2; $i++) {
            $rr = Req -M POST -U $RP -B '{ "target_url":"http://169.254.169.254/" }'
            if ($rr.Code -eq 429) { $seen429 = $true; break }
        }
        Chk "B46a eventual 429" $seen429 "last=$($rr.Code)"
        $before = (Invoke-RestMethod "$base/api/v1/events/$EID/replays").items.Count
        Req -M POST -U $RP -B '{ "target_url":"http://169.254.169.254/" }' | Out-Null
        $after = (Invoke-RestMethod "$base/api/v1/events/$EID/replays").items.Count
        Chk 'B46b a 429 records no attempt' ($after -eq $before) "before=$before after=$after"
    }
}
finally {
    foreach ($p in $script:started) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
}

Write-Host ''
Write-Host "PASS=$script:pass  FAIL=$script:fail" -ForegroundColor $(if ($script:fail -eq 0) { 'Green' } else { 'Red' })
if ($script:fail -gt 0) { exit 1 }
