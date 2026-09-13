# Shared helpers for the desktop tooling: one reader for the config file and one writer
# for the background launcher.
#
# Why this file exists: `webhook-zq-background.cmd` used to be written by BOTH
# service.ps1 and server-manager.ps1 with different rules (one wrote only --port, the
# other wrote the full argument list). Whoever ran last silently won, so the autostarted
# process could be running a completely different configuration from the one the GUI was
# showing. There is now exactly one writer.
#
# Usage from a script:
#   . (Join-Path $PSScriptRoot '..\lib\launcher.ps1')      # adjust the relative path
#   $cfg = Read-AppConfig -Path $settingsFile
#   Write-BackgroundLauncher -Path $launcher -Exe $Exe -Config $cfg

$script:AppConfigDefaults = @{
    Port          = 8080
    Addr          = '127.0.0.1'
    DatabaseUrl   = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
    PublicUrlRoot = ''
    AuthToken     = ''
    Timeout       = '5s'
    RateLimit     = '30'
    Retries       = '0'
    MaxPreview    = '4096'
    KeepEvents    = '500'
    KeepDays      = '30'
    MaxBody       = '1048576'
    LogLevel      = 'info'
    AllowHosts    = @()
    AllowPrivate  = $false
    TrustProxy    = $false

    # Held in memory for the manager window only - never written to the file (the File
    # struct has no such field), and never printed.
    EncryptKey = ''
}

function Read-AppConfig {
    param([string]$Path)

    $cfg = @{}
    foreach ($k in $script:AppConfigDefaults.Keys) { $cfg[$k] = $script:AppConfigDefaults[$k] }

    if ((Test-Path $Path)) {
        try {
            $saved = Get-Content $Path -Raw | ConvertFrom-Json
            foreach ($p in $saved.PSObject.Properties) { $cfg[$p.Name] = $p.Value }
        }
        catch {
            Write-Host "  配置文件无法解析，将使用默认值：$Path" -ForegroundColor Yellow
        }
    }

    # Secrets never live in the file; they come from the environment (a deployment
    # decision) and are only ever passed to the child process.
    if ($env:DATABASE_URL) { $cfg.DatabaseUrl = $env:DATABASE_URL }

    return $cfg
}

function Get-EncryptKey {
    param($Config)

    # Not persisted: written into a file next to the source it is one "git add ." away
    # from being published.
    if ($Config.EncryptKey) { return $Config.EncryptKey }
    if ($env:ENCRYPT_KEY) { return $env:ENCRYPT_KEY }

    return ''
}

function ConvertTo-StartArgs {
    param($Config)

    $a = [System.Collections.ArrayList]::new()
    [void]$a.Add('start')
    [void]$a.Add('--port'); [void]$a.Add([string]$Config.Port)
    [void]$a.Add('--addr'); [void]$a.Add($Config.Addr)
    [void]$a.Add('--replay-timeout'); [void]$a.Add($Config.Timeout)
    [void]$a.Add('--replay-rate-limit'); [void]$a.Add($Config.RateLimit)
    [void]$a.Add('--replay-max-retries'); [void]$a.Add($Config.Retries)
    [void]$a.Add('--replay-max-preview'); [void]$a.Add($Config.MaxPreview)
    [void]$a.Add('--retention-max-events'); [void]$a.Add($Config.KeepEvents)
    [void]$a.Add('--retention-max-days'); [void]$a.Add($Config.KeepDays)
    [void]$a.Add('--max-request-body-size'); [void]$a.Add($Config.MaxBody)
    [void]$a.Add('--log-level'); [void]$a.Add($Config.LogLevel)

    if ($Config.PublicUrlRoot) { [void]$a.Add('--public-url-root'); [void]$a.Add($Config.PublicUrlRoot) }
    if ($Config.AuthToken) { [void]$a.Add('--auth-token'); [void]$a.Add($Config.AuthToken) }

    # Only ever re-apply what is already configured. Nothing here can widen the SSRF
    # gate: the window may clear these, never set them.
    if ($Config.AllowPrivate) { [void]$a.Add('--replay-allow-private') }
    foreach ($h in @($Config.AllowHosts)) { if ($h) { [void]$a.Add('--replay-allow-host'); [void]$a.Add($h) } }
    if ($Config.TrustProxy) { [void]$a.Add('--trust-proxy-headers') }

    return $a
}

function Format-Arg {
    param([string]$Value)

    if ($Value -match '[\s"]') { return '"' + ($Value -replace '"', '\"') + '"' }

    return $Value
}

function Write-BackgroundLauncher {
    param(
        [string]$Path,
        [string]$Exe,
        $Config,
        [string[]]$ExtraArgs = @()
    )

    $lines = @('@echo off')
    $lines += "set DATABASE_URL=$($Config.DatabaseUrl)"

    $key = Get-EncryptKey $Config
    if ($key) { $lines += "set ENCRYPT_KEY=$key" }
    if ($Config.AuthToken) { $lines += "set AUTH_TOKEN=$($Config.AuthToken)" }

    # The secret travels in the environment, never on the command line.
    $all = @(ConvertTo-StartArgs $Config)
    foreach ($extra in $ExtraArgs) { $all += $extra }

    $argsText = (($all) | ForEach-Object { Format-Arg $_ }) -join ' '
    $lines += "`"$Exe`" $argsText"

    # cmd.exe reads .bat/.cmd with the ANSI code page, so UTF-8 or ASCII here would
    # corrupt a non-ASCII path or password.
    $gbk = [System.Text.Encoding]::GetEncoding(936)
    [System.IO.File]::WriteAllText($Path, ($lines -join "`r`n"), $gbk)

    return $Path
}
