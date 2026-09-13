#Requires -Version 7
<#
.SYNOPSIS
    Four step demo: create an inbox, send a webhook, look at it, replay it.

.DESCRIPTION
    1. create an inbox                -> prints the receive URL
    2. send a webhook                 -> prints the event id
    3. open the event in the browser  -> prints the URL (and opens it)
    4. replay it to a local receiver  -> prints the replay outcome

    Step 4 targets 127.0.0.1, which the replay policy blocks by default (that is the
    point). Start the server with the two explicit demo switches to allow it:

        --replay-allow-host 127.0.0.1 --replay-allow-private

    If the server was started without them, the script shows the expected 403 and tells
    you how to restart - which doubles as a live demonstration of the protection.
#>
param(
    [string]$Base = 'http://127.0.0.1:8080',
    [int]$ReceiverPort = 9099,
    [switch]$NoBrowser
)

$ErrorActionPreference = 'Continue'

function Step($n, $text) { Write-Host "`n[$n] $text" -ForegroundColor Cyan }

Step 1 'create an inbox'

$inbox = Invoke-RestMethod -Method POST -Uri "$Base/api/v1/inboxes" `
    -ContentType 'application/json' -Body '{"name":"demo"}'

$token = $inbox.token
Write-Host "  inbox id : $($inbox.id)"
Write-Host "  token    : $token"
Write-Host "  receive  : $($inbox.receive_url)"

Step 2 'send a webhook'

$payload = '{"event":"order.paid","amount":42.5,"user":{"id":7}}'

$hook = Invoke-RestMethod -Method POST -Uri "$Base/hooks/$token/orders?src=demo" `
    -ContentType 'application/json' -Body $payload `
    -Headers @{ 'authorization' = 'Bearer top-secret'; 'x-trace-id' = 'demo-1' }

Write-Host "  event id : $($hook.id)"

Step 3 'look at it in the UI'

$url = "$Base/inboxes/$($inbox.id)/events/$($hook.id)"
Write-Host "  $url"

if (-not $NoBrowser) {
    Start-Process $url
}

Step 4 'replay it to a local test receiver'

$receiver = Start-Process -FilePath 'node' -ArgumentList (Join-Path $PSScriptRoot 'test-receiver.mjs'), $ReceiverPort -PassThru -NoNewWindow
Start-Sleep -Seconds 2

try {
    $body = @{ target_url = "http://127.0.0.1:$ReceiverPort/ok" } | ConvertTo-Json

    try {
        $res = Invoke-RestMethod -Method POST -Uri "$Base/api/v1/events/$($hook.id)/replay" `
            -ContentType 'application/json' -Body $body

        Write-Host "  outcome  : $($res.last.outcome) (status $($res.last.status_code), $($res.last.duration_ms) ms)" -ForegroundColor Green
        Write-Host "  preview  : $($res.last.response_preview)"
    }
    catch {
        $code = [int]$_.Exception.Response.StatusCode

        if ($code -eq 403) {
            Write-Host '  403 target_blocked - this is the protection working.' -ForegroundColor Yellow
            Write-Host '  To replay into a local receiver restart the server with:' -ForegroundColor Yellow
            Write-Host '    --replay-allow-host 127.0.0.1 --replay-allow-private' -ForegroundColor Yellow
        }
        else {
            Write-Host "  replay failed: $($_.Exception.Message)" -ForegroundColor Red
        }
    }

    Start-Sleep -Seconds 1
}
finally {
    Stop-Process -Id $receiver.Id -Force -ErrorAction SilentlyContinue
}

Write-Host "`nreceiver log is above; note that Authorization was NOT forwarded." -ForegroundColor DarkGray
