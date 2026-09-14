# Acceptance B1: inboxes + capture (B01-B22).
#
# Executable, self-checking version of the acceptance list: every case prints PASS or
# FAIL with the value that was actually observed, so a failure can be diagnosed without
# re-running anything by hand.
#
# Usage:
#   pwsh ./scripts/acceptance/b1-inbox-capture.ps1
#   pwsh ./scripts/acceptance/b1-inbox-capture.ps1 -Base http://127.0.0.1:8080
#
# The server must run with the demo profile (local replay allowed) for B2/B3, but B1
# only needs the API. Set PGPASSWORD or pass -Psql if your setup differs.

param(
    [string]$Base = 'http://127.0.0.1:8080',
    [string]$Psql = '',
    [string]$DbUrl = $(if ($env:DATABASE_URL) { $env:DATABASE_URL } else { 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable' }),
    [string]$ResultFile = "$env:TEMP\whq-b1-result.txt"
)

$ProgressPreference = 'SilentlyContinue'
$pass = 0; $fail = 0; $log = @()

# psql is resolved like everywhere else in the project (scripts/lib/tools.ps1): PATH
# first, then C:\Program Files\PostgreSQL\*\bin - a hardcoded "16" breaks on every
# machine that installed a different major.
. (Join-Path $PSScriptRoot '..\lib\tools.ps1')
if (-not $Psql) { $Psql = Find-Tool 'psql' @('C:\Program Files\PostgreSQL\*\bin\psql.exe') }
if (-not $env:PGPASSWORD) { $env:PGPASSWORD = 'postgres' }
$env:PGCONNECT_TIMEOUT = '10'

if (-not $Psql -or -not (Test-Path $Psql)) {
    Write-Host "FAIL  setup  -> psql not found (install PostgreSQL or pass -Psql)" -ForegroundColor Red
    exit 1
}

function Req {
    param([string]$M = 'GET', [string]$U, [hashtable]$H = @{}, [string]$B, [string]$InFile)
    $p = @{ Method = $M; Uri = $U; UseBasicParsing = $true; TimeoutSec = 120 }
    if ($H.Count -gt 0) { $p.Headers = $H }
    if ($InFile) { $p.ContentType = 'application/octet-stream'; $p.InFile = $InFile }
    elseif ($B) { $p.ContentType = 'application/json'; $p.Body = $B }
    try {
        $r = Invoke-WebRequest @p
        return @{ Code = [int]$r.StatusCode; Body = [string]$r.Content }
    }
    catch {
        # PowerShell 7 wraps errors in HttpResponseMessage (which has no
        # GetResponseStream); the body is in ErrorDetails instead.
        $code = 0; $body = ''
        if ($_.Exception.Response) { $code = [int]$_.Exception.Response.StatusCode }
        if ($_.ErrorDetails.Message) { $body = $_.ErrorDetails.Message }
        return @{ Code = $code; Body = $body }
    }
}

function J($r) { try { $r.Body | ConvertFrom-Json } catch { $null } }
function ErrCode($r) { $j = J $r; if ($j -and $j.error) { $j.error.code } else { '' } }
function ErrMsg($r) { $j = J $r; if ($j -and $j.error) { $j.error.message } else { '' } }
# The options must come BEFORE the database: psql treats a leading conninfo/URI as the
# whole connection and ignores every later argument ("extra command-line argument -c
# ignored"), silently returning nothing.
#
# Hang-proof by construction:
#   * PGPASSWORD is exported above, and -w forbids the password prompt, so a missing
#     credential fails fast instead of parking on stdin (that is what used to hang the
#     background runs).
#   * PGCONNECT_TIMEOUT bounds the connection attempt.
#   * WaitForExit is a hard cap on the whole call, and the process tree is killed when
#     it expires - no psql may outlive this script.
function Sql($q) {
    $tmp = [IO.Path]::GetTempFileName()
    $err = [IO.Path]::GetTempFileName()
    # Start-Process does NOT quote array elements, so a query with spaces would arrive
    # as a pile of "extra command-line argument ignored" warnings and an empty result.
    # Quote it ourselves (CommandLineToArgvW rules: embedded quotes escaped with \").
    $argv = '-w -U postgres -d webhook_rd -tA -c "' + $q.Replace('"', '\"') + '"'
    $p = Start-Process -FilePath $Psql -ArgumentList $argv `
        -NoNewWindow -PassThru -RedirectStandardOutput $tmp -RedirectStandardError $err
    if (-not $p.WaitForExit(20000)) {
        $p.Kill($true)
        Remove-Item $tmp, $err -Force -ErrorAction SilentlyContinue
        Write-Host "psql timed out after 20s: $q" -ForegroundColor Red
        return ''
    }
    $out = ((Get-Content $tmp -Raw -ErrorAction SilentlyContinue) -join "`n").Trim()
    if ($p.ExitCode -ne 0) {
        $e = (Get-Content $err -Raw -ErrorAction SilentlyContinue).Trim()
        Write-Host "psql exited $($p.ExitCode): $e" -ForegroundColor Red
    }
    Remove-Item $tmp, $err -Force -ErrorAction SilentlyContinue
    return $out
}

# Results are collected as well as printed: a CI job (or a caller that redirects the
# output) needs the verdict without parsing coloured host output.
function Chk($id, $ok, $detail) {
    if ($ok) { $script:pass++; $script:log += "PASS  $id" }
    else { $script:fail++; $script:log += "FAIL  $id  -> $detail" }
}

Write-Host "=== B1 inboxes + capture ($Base) ===" -ForegroundColor Cyan

# ── create ──────────────────────────────────────────────────────────────────
$c = Req -M POST -U "$Base/api/v1/inboxes" -B '{"name":"b1"}'
$inb = J $c

# -cnotmatch is case sensitive on purpose: -notmatch would flag "i" as "I" and fail.
Chk 'B01 create 201, token 51 chars, no ambiguous letters' `
    ($c.Code -eq 201 -and $inb.token.Length -eq 51 -and $inb.token -cnotmatch '[01loOI]') `
    "code=$($c.Code) len=$($inb.token.Length)"

$ID = $inb.id; $T = $inb.token

# ── name validation ─────────────────────────────────────────────────────────
$r = Req -M POST -U "$Base/api/v1/inboxes" -B '{"name":"   "}'
Chk 'B02a blank name -> 422 validation_error' ($r.Code -eq 422 -and (ErrCode $r) -eq 'validation_error') "code=$($r.Code) $(ErrCode $r)"

$r = Req -M POST -U "$Base/api/v1/inboxes" -B ('{"name":"' + ('x' * 121) + '"}')
Chk 'B02b 121 chars -> 422' ($r.Code -eq 422) "code=$($r.Code) $(ErrMsg $r)"

$r = Req -M POST -U "$Base/api/v1/inboxes" -B ('{"name":"' + ('x' * 120) + '"}')
Chk 'B02b2 120 chars -> 201' ($r.Code -eq 201) "code=$($r.Code)"

$r = Req -M POST -U "$Base/api/v1/inboxes" -B '{'
Chk 'B02c malformed json -> 422' ($r.Code -eq 422) "code=$($r.Code) $(ErrCode $r)"

# ── pagination clamp ────────────────────────────────────────────────────────
$r = Req -U "$Base/api/v1/inboxes?limit=0"; $j = J $r
Chk 'B03a limit=0 -> 20' ($j.limit -eq 20) "limit=$($j.limit)"
$r = Req -U "$Base/api/v1/inboxes?limit=9999"; $j = J $r
Chk 'B03b limit=9999 -> 100' ($j.limit -eq 100) "limit=$($j.limit)"
$r = Req -U "$Base/api/v1/inboxes?limit=-5&offset=-1"; $j = J $r
Chk 'B03c negatives clamped' ($j.limit -eq 20 -and $j.offset -eq 0) "limit=$($j.limit) offset=$($j.offset)"

# ── partial update (one UPDATE, untouched columns must survive) ─────────────
Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"name":"renamed"}' | Out-Null
$row = Sql "select name||'|'||response_code||'|'||enabled||'|'||signature_scheme||'|'||retention_max_events from inbox where id='$ID'"
# psql renders booleans as "t" (and as "true" with some output settings), so accept
# both rather than pinning the formatting.
Chk 'B05 partial update keeps other columns' ($row -match '^renamed\|200\|(t|true)\|hmac-sha256-hex\|500$') "row=$row"

# ── response_code / scheme / delay validation ───────────────────────────────
foreach ($code in 199, 600, 204, 304) {
    $r = Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B "{`"response_code`":$code}"
    Chk "B06 reject response_code=$code" ($r.Code -eq 422) "code=$($r.Code) $(ErrMsg $r)"
}
$r = Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"response_code":418}'
Chk 'B06 accept 418' ($r.Code -eq 200) "code=$($r.Code)"

$r = Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"signature_scheme":"md5"}'
Chk 'B07 reject unknown scheme' ($r.Code -eq 422) "code=$($r.Code) $(ErrMsg $r)"
$r = Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"signature_scheme":"sha256-prefixed"}'
Chk 'B07 accept sha256-prefixed' ($r.Code -eq 200) "code=$($r.Code)"

$r = Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"response_delay_ms":-1}'
Chk 'B08 reject delay -1' ($r.Code -eq 422) "code=$($r.Code)"
$r = Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"response_delay_ms":30001}'
Chk 'B08 reject delay 30001' ($r.Code -eq 422) "code=$($r.Code)"

# ── signing secret needs an encryption key ──────────────────────────────────
$r = Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"signing_secret":"s3cr3t"}'
Chk 'B09 secret without encrypt key -> 409' ($r.Code -eq 409) "code=$($r.Code) $(ErrMsg $r)"
$r = Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"signing_secret":"   "}'
Chk 'B09b blank secret clears (200)' ($r.Code -eq 200) "code=$($r.Code) $(ErrMsg $r)"

# ── invalid id ──────────────────────────────────────────────────────────────
$r = Req -U "$Base/api/v1/inboxes/not-a-uuid"
Chk 'B12 invalid uuid -> 400' ($r.Code -eq 400) "code=$($r.Code) $(ErrCode $r)"

# ── capture ─────────────────────────────────────────────────────────────────
Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"response_code":200,"enabled":true,"signature_scheme":"hmac-sha256-hex","response_delay_ms":0}' | Out-Null

$r = Req -M POST -U "$Base/hooks/$T/orders?src=b1" -H @{ 'authorization' = 'Bearer top-secret' } -B '{"event":"order.paid"}'
$ev = J $r
Chk 'B13 capture 200 + returns event id' ($r.Code -eq 200 -and $ev.ok -and $ev.id) "code=$($r.Code)"
$row = Sql "select method||'|'||path||'|'||query||'|'||content_type||'|'||body_size from event where id='$($ev.id)'"

# The stored path is the full incoming path, /hooks/<token>/orders - that is what
# "record the request path" means for a capture tool.
Chk 'B13b stored fields' ($row -match '^POST\|/hooks/[^|]+/orders\|src=b1\|application/json\|22$') "row=$row"

$r = Req -M POST -U "$Base/hooks/deadbeefdeadbeef" -B '{}'
Chk 'B14 unknown token -> 404 unknown_token' ($r.Code -eq 404 -and (ErrCode $r) -eq 'unknown_token') "code=$($r.Code) $(ErrCode $r)"

# ── sensitive headers (14) ──────────────────────────────────────────────────
$hdrs = @('authorization: Bearer top-secret', 'proxy-authorization: Basic zzz', 'cookie: sid=1', 'set-cookie: c=1',
    'x-api-key: k1', 'x-auth-token: t1', 'x-csrf-token: c1', 'api-key: k2', 'authentication: a1',
    'x-access-token: a2', 'x-session-token: s1', 'x-refresh-token: r1',
    'x-amz-security-token: aws1', 'x-goog-iam-authorization-token: g1', 'x-trace-id: trace-abc')
$curlArgs = @()
foreach ($h in $hdrs) { $curlArgs += @('-H', $h) }
$out = & curl.exe -s -X POST "$Base/hooks/$T/sec" @curlArgs -H 'content-type: application/json' -d '{}'
$EID2 = ($out | ConvertFrom-Json).id
$d = J (Req -U "$Base/api/v1/events/$EID2")
$sens = @($d.headers | Where-Object sensitive)
$trace = @($d.headers | Where-Object { $_.name -eq 'X-Trace-Id' })
Chk 'B18 exactly 14 headers masked' ($sens.Count -eq 14 -and ($sens.value | Select-Object -Unique) -eq '***redacted***') "count=$($sens.Count)"
Chk 'B18 non-sensitive header stays clear' ($trace[0].value -eq 'trace-abc') "trace=$($trace[0].value)"
$dbh = Sql "select headers::text from event where id='$EID2'"
Chk 'B18 no plaintext in the database' ($dbh -notmatch 'top-secret|aws1|g1') "matched=$($dbh -match 'top-secret')"

# ── client ip cannot be spoofed ─────────────────────────────────────────────
Req -M POST -U "$Base/hooks/$T/ip" -H @{ 'X-Forwarded-For' = '8.8.8.8' } -B '{}' | Out-Null
$ip = Sql 'select client_ip from event order by created_at desc limit 1'
Chk 'B21 client_ip ignores X-Forwarded-For' ($ip -eq '127.0.0.1') "ip=$ip"

# ── custom response ─────────────────────────────────────────────────────────
$b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes('teapot'))
Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B "{`"response_code`":418,`"response_delay_ms`":800,`"response_body_base64`":`"$b64`"}" | Out-Null
$sw = [Diagnostics.Stopwatch]::StartNew()
$r = Req -M POST -U "$Base/hooks/$T/teapot" -B '{}'
$sw.Stop()
Chk 'B22 custom 418 + body' ($r.Code -eq 418 -and $r.Body -eq 'teapot') "code=$($r.Code) body=$($r.Body)"
Chk 'B22 delay honoured (>=800ms)' ($sw.Elapsed.TotalMilliseconds -ge 800) "elapsed=$([int]$sw.Elapsed.TotalMilliseconds)ms"

# ── rotate then delete ──────────────────────────────────────────────────────
Req -M PATCH -U "$Base/api/v1/inboxes/$ID" -B '{"response_code":200,"response_delay_ms":0}' | Out-Null
$old = $T
$new = (J (Req -M POST -U "$Base/api/v1/inboxes/$ID/token/rotate")).token
Chk 'B11 rotate returns a different token' ($new -and $new -ne $old) "new=$new"
$r = Req -M POST -U "$Base/hooks/$old" -B '{}'
Chk 'B11 old token -> 404' ($r.Code -eq 404) "code=$($r.Code)"
$r = Req -M POST -U "$Base/hooks/$new" -B '{}'
Chk 'B11 new token -> 200' ($r.Code -eq 200) "code=$($r.Code)"

$r = Req -M DELETE -U "$Base/api/v1/inboxes/$ID"
Chk 'B10 delete -> 204' ($r.Code -eq 204) "code=$($r.Code)"
$r = Req -U "$Base/api/v1/inboxes/$ID"
Chk 'B10 get after delete -> 404' ($r.Code -eq 404) "code=$($r.Code)"

$script:log += "PASS=$script:pass  FAIL=$script:fail"
Set-Content -Path $ResultFile -Value $script:log
Write-Output ($script:log -join "`n")
Write-Output "result file: $ResultFile"
if ($script:fail -gt 0) { exit 1 }
