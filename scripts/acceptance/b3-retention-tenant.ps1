# Acceptance B3: retention + multi tenancy.
#
# Unlike B1/B2 this one needs its own server instances, because the demo profile on 8080
# has neither a fast retention ticker nor --auth-keys:
#
#   * 8090 - retention (--retention-interval 10s)
#   * 8083 - multi tenancy (--auth-keys alice:keyA,bob:keyB --encrypt-key <32 bytes>)
#
# The script starts both, waits for them, runs the assertions and stops what it started.
# Set -KeepServers to leave them running.
#
# Usage:
#   pwsh ./scripts/acceptance/b3-retention-tenant.ps1
#   pwsh ./scripts/acceptance/b3-retention-tenant.ps1 -KeepServers

param(
    [switch]$KeepServers,
    [string]$Psql = 'C:\Program Files\PostgreSQL\16\bin\psql.exe'
)

$ErrorActionPreference = 'Continue'
$env:PGPASSWORD = 'postgres'
$env:DATABASE_URL = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'

$pass = 0; $fail = 0
$started = @()

function Req {
    param([string]$M = 'GET', [string]$U, [hashtable]$H = @{}, [string]$B)
    $p = @{ Method = $M; Uri = $U; UseBasicParsing = $true; TimeoutSec = 60 }
    if ($H.Count -gt 0) { $p.Headers = $H }
    if ($B) { $p.ContentType = 'application/json'; $p.Body = $B }
    try { $r = Invoke-WebRequest @p; return @{ Code = [int]$r.StatusCode; Body = [string]$r.Content } }
    catch {
        $c = 0; $b = ''
        if ($_.Exception.Response) { $c = [int]$_.Exception.Response.StatusCode }
        if ($_.ErrorDetails.Message) { $b = $_.ErrorDetails.Message }
        return @{ Code = $c; Body = $b }
    }
}
function J($r) { try { $r.Body | ConvertFrom-Json } catch { $null } }
function Sql($q) { (& $Psql -U postgres -d webhook_rd -tA -c $q) -join '' }
function Chk($id, $ok, $detail) {
    if ($ok) { $script:pass++; Write-Host "PASS  $id" -ForegroundColor Green }
    else { $script:fail++; Write-Host "FAIL  $id  -> $detail" -ForegroundColor Red }
}
function Wait-Ready($url, $seconds = 40) {
    for ($i = 0; $i -lt $seconds; $i++) {
        try {
            $r = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
            if ($r.StatusCode -eq 200) { return $true }
        }
        catch { }
        Start-Sleep -Seconds 1
    }
    return $false
}
function Start-Server($port, [string[]]$extra) {
    $args = @('start', '--port', $port) + $extra
    $p = Start-Process -FilePath (Join-Path $PSScriptRoot '..\..\bin\webhook-zq.exe') `
        -ArgumentList $args -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput "$env:TEMP\whq-$port.out.log" -RedirectStandardError "$env:TEMP\whq-$port.err.log"
    $script:started += $p
    return $p
}

Write-Host '=== B3 retention + multi tenancy ===' -ForegroundColor Cyan

# ── retention (8090) ────────────────────────────────────────────────────────
Write-Host 'starting 8090 (retention, 10s ticker)...' -ForegroundColor DarkGray
Start-Server 8090 @('--retention-interval', '10s', '--retention-max-events', '500') | Out-Null

if (-not (Wait-Ready 'http://127.0.0.1:8090/healthz')) {
    Write-Host '8090 did not come up - skipping retention checks' -ForegroundColor Red
    $script:fail++
}
else {
    $r = Req -M POST -U 'http://127.0.0.1:8090/api/v1/inboxes' -B '{"name":"b3-ret"}'
    $inb = J $r
    $ID = $inb.id
    Req -M PATCH -U "http://127.0.0.1:8090/api/v1/inboxes/$ID" -B '{"retention_max_events":20}' | Out-Null

    # 30 events straight into the database: 30 HTTP calls would be slow and would not
    # exercise the cleaner any differently.
    Sql "insert into event (id, inbox_id, method, path, content_type, body, body_size, body_text, client_ip, created_at)
         select gen_random_uuid(), '$ID'::uuid, 'POST', '/x', 'application/json', '{}', 2, '{}', '127.0.0.1', now()
         from generate_series(1,30)" | Out-Null

    $before = [int](Sql "select count(*) from event where inbox_id='$ID'")
    Chk 'B47a seeded 30 events' ($before -eq 30) "count=$before"

    Write-Host 'waiting for the retention ticker (<=20s)...' -ForegroundColor DarkGray
    $after = 0
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Seconds 1
        $after = [int](Sql "select count(*) from event where inbox_id='$ID'")
        if ($after -eq 20) { break }
    }
    Chk 'B47 retention prunes to the per inbox limit' ($after -eq 20) "before=$before after=$after (want 20)"

    # An inbox with a different limit must not be affected by the other one.
    $r2 = Req -M POST -U 'http://127.0.0.1:8090/api/v1/inboxes' -B '{"name":"b3-ret2"}'
    $inb2 = J $r2
    Req -M PATCH -U "http://127.0.0.1:8090/api/v1/inboxes/$($inb2.id)" -B '{"retention_max_events":5}' | Out-Null
    Sql "insert into event (id, inbox_id, method, path, content_type, body, body_size, body_text, client_ip, created_at)
         select gen_random_uuid(), '$($inb2.id)'::uuid, 'POST', '/x', 'application/json', '{}', 2, '{}', '127.0.0.1', now()
         from generate_series(1,10)" | Out-Null
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Seconds 1
        $c2 = [int](Sql "select count(*) from event where inbox_id='$($inb2.id)'")
        if ($c2 -eq 5) { break }
    }
    Chk 'B50 per inbox limits stay independent' ($c2 -eq 5) "second inbox=$c2 (want 5)"

    Req -M DELETE -U "http://127.0.0.1:8090/api/v1/inboxes/$ID" | Out-Null
    Req -M DELETE -U "http://127.0.0.1:8090/api/v1/inboxes/$($inb2.id)" | Out-Null
}

# ── multi tenancy (8083) ────────────────────────────────────────────────────
$key = [Convert]::ToBase64String((1..32 | ForEach-Object { 7 }))
Write-Host 'starting 8083 (multi tenant + encryption)...' -ForegroundColor DarkGray
Start-Server 8083 @('--auth-keys', 'alice:keyA', '--auth-keys', 'bob:keyB', '--encrypt-key', $key) | Out-Null

if (-not (Wait-Ready 'http://127.0.0.1:8083/healthz')) {
    Write-Host '8083 did not come up - skipping tenancy checks' -ForegroundColor Red
    $script:fail++
}
else {
    $A = @{ Authorization = 'Bearer keyA' }
    $B = @{ Authorization = 'Bearer keyB' }
    $t = 'http://127.0.0.1:8083'

    $ia = J (Req -M POST -U "$t/api/v1/inboxes" -H $A -B '{"name":"alice-box"}')
    $ib = J (Req -M POST -U "$t/api/v1/inboxes" -H $B -B '{"name":"bob-box"}')

    $listA = J (Req -U "$t/api/v1/inboxes" -H $A)
    $names = ($listA.items | ForEach-Object { $_.name }) -join ','
    Chk 'B57a alice sees only her own inbox' ($names -notmatch 'bob-box' -and $names -match 'alice-box') "alice sees: $names"

    $st = J (Req -U "$t/api/v1/settings" -H $A)
    Chk 'B57b auth_enabled is true for --auth-keys' ($st.auth_enabled -eq $true) "auth_enabled=$($st.auth_enabled)"
    Chk 'B57c encryption_enabled is true' ($st.encryption_enabled -eq $true) "encryption_enabled=$($st.encryption_enabled)"

    $cross = Req -U "$t/api/v1/inboxes/$($ia.id)" -H $B
    Chk 'B58 another tenant gets 404 (not 403)' ($cross.Code -eq 404) "code=$($cross.Code)"

    # alice captures an event with a sensitive header, then reveals it.
    $ev = J (Req -M POST -U "$t/hooks/$($ia.token)/sec" -H @{ 'authorization' = 'Bearer s3cr3t' } -B '{"a":1}')
    $detail = J (Req -U "$t/api/v1/events/$($ev.id)?reveal=1" -H $A)
    $sens = @($detail.headers | Where-Object sensitive)[0]
    Chk 'B60a reveal returns the plaintext for the owner' ($sens.value -eq 'Bearer s3cr3t' -and $sens.revealed -eq $true) "value=$($sens.value) revealed=$($sens.revealed)"

    $plain = @($detail.headers | Where-Object { -not $_.sensitive })
    Chk 'B60b non sensitive headers are never flagged revealed' (@($plain | Where-Object { $_.revealed -eq $true }).Count -eq 0) "flagged=$((@($plain | Where-Object { $_.revealed -eq $true }).Count))"

    $bobEv = Req -U "$t/api/v1/events/$($ev.id)?reveal=1" -H $B
    Chk 'B59 another tenant cannot read the event' ($bobEv.Code -eq 404) "code=$($bobEv.Code)"

    Req -M DELETE -U "$t/api/v1/inboxes/$($ia.id)" -H $A | Out-Null
    Req -M DELETE -U "$t/api/v1/inboxes/$($ib.id)" -H $B | Out-Null
}

# ── cleanup ─────────────────────────────────────────────────────────────────
$orphanEvents = [int](Sql "select count(*) from event e left join inbox i on i.id=e.inbox_id where i.id is null")
$orphanReplays = [int](Sql "select count(*) from replay_attempt r left join event e on e.id=r.event_id where e.id is null")
Chk 'no orphan events after cleanup' ($orphanEvents -eq 0) "orphans=$orphanEvents"
Chk 'no orphan replay attempts after cleanup' ($orphanReplays -eq 0) "orphans=$orphanReplays"

if (-not $KeepServers) {
    foreach ($p in $started) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
}

Write-Host ''
Write-Host "PASS=$script:pass  FAIL=$script:fail" -ForegroundColor $(if ($script:fail -eq 0) { 'Green' } else { 'Red' })
if ($script:fail -gt 0) { exit 1 }
