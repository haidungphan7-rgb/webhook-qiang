# Runs the platform in the background with an icon in the system tray.
#
# Closing the icon stops the server - the tray is the switch, so there is no state left
# running that the user cannot see.
#
# If an instance is already listening on the port, the tray adopts it instead of starting
# a second one (a second one would just fail with "address already in use").
#
# Usage:
#   pwsh ./scripts/tray.ps1                       # tray icon, start if needed
#   pwsh ./scripts/tray.ps1 -Port 8080 -DatabaseUrl "postgres://..."
#   pwsh ./scripts/tray.ps1 -SelfTest             # exit after a few seconds (for testing)

param(
    [uint16]$Port = 8080,
    [string]$DatabaseUrl = $env:DATABASE_URL,
    [string]$Exe,
    [switch]$SelfTest
)

$ErrorActionPreference = 'Stop'

# Every subprocess we capture (doctor, schtasks, psql) emits UTF-8, while the console
# code page on zh-CN Windows is GBK - without this every Chinese message is mojibake.
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$root = Split-Path -Parent $PSScriptRoot

# Stopping the server has exactly one safe implementation: never kill a process whose
# executable cannot be proven to live under this project directory.
. (Join-Path $PSScriptRoot 'lib\process.ps1')

$logDir = Join-Path $root 'logs'
$logOut = Join-Path $logDir 'server.log'
$logErr = Join-Path $logDir 'server.err'

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

if (-not $Exe) {
    $Exe = Join-Path $root 'bin/webhook-zq.exe'
    if (-not (Test-Path $Exe)) { $Exe = Join-Path $root 'webhook-zq.exe' }
}

if (-not (Test-Path $Exe)) {
    [System.Windows.Forms.MessageBox]::Show(
        "找不到程序：`n$Exe`n`n请先运行 pwsh ./scripts/setup.ps1",
        'Webhook 重放平台', 'OK', 'Error') | Out-Null
    exit 1
}

if (-not $DatabaseUrl) {
    $DatabaseUrl = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
}

New-Item -ItemType Directory -Force -Path $logDir | Out-Null

# ── icon ────────────────────────────────────────────────────────────────────
# Same mark as the web header: a solid bolt on the brand blue (#0052d9). Drawn at
# runtime so there is no icon file to keep in sync with the frontend.
#
# Only two states, on purpose: running (blue) and stopped (grey). "Is it running" is a
# fact we can determine reliably; "is it healthy" is not - that belongs to the tooltip
# and to 服务器设置, not to 16 pixels.
function New-TrayIcon([bool]$running) {
    $size = 64
    $bmp = New-Object System.Drawing.Bitmap $size, $size
    $g = [System.Drawing.Graphics]::FromImage($bmp)

    $g.SmoothingMode = 'AntiAlias'
    $g.Clear([System.Drawing.Color]::Transparent)

    $back = if ($running) {
        [System.Drawing.Color]::FromArgb(255, 0, 82, 217)
    }
    else {
        [System.Drawing.Color]::FromArgb(255, 134, 142, 150)
    }

    $brush = New-Object System.Drawing.SolidBrush $back
    $g.FillRectangle($brush, 0, 0, $size, $size)

    $points = @(
        [System.Drawing.Point]::new(42, 8),
        [System.Drawing.Point]::new(42, 28),
        [System.Drawing.Point]::new(56, 28),
        [System.Drawing.Point]::new(28, 56),
        [System.Drawing.Point]::new(28, 36),
        [System.Drawing.Point]::new(14, 36)
    )

    $g.FillPolygon([System.Drawing.Brushes]::White, $points)

    $g.Dispose()
    $brush.Dispose()

    return [System.Drawing.Icon]::FromHandle($bmp.GetHicon())
}

# ── the server process ──────────────────────────────────────────────────────
function Get-Server {
    $c = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($c) { return Get-Process -Id $c.OwningProcess -ErrorAction SilentlyContinue }

    return $null
}

function Start-Server {
    if (Get-Server) { return $true }

    $env:DATABASE_URL = $DatabaseUrl

    # Redirected so 服务器设置 -> 打开日志 has something to show.
    Start-Process -FilePath $Exe -ArgumentList @('start', '--port', $Port) `
        -WindowStyle Hidden -RedirectStandardOutput $logOut -RedirectStandardError $logErr | Out-Null

    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Milliseconds 500
        try {
            $null = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec 2

            return $true
        }
        catch { }
    }

    return $false
}

function Stop-Server {
    $proc = Get-Server
    if (-not $proc) { return }

    $r = Stop-AppProcess -Id $proc.Id -Root $root

    if ($r.Stopped) {
        Write-Host "已停止服务 pid $($r.Id)"
    }
    else {
        Write-Host "跳过 pid $($r.Id)（$($r.Reason)：$($r.Path)）" -ForegroundColor Yellow
    }
}

function Open-Manager {
    # The manager must see the SAME database and binary this tray started. Without
    # this, it falls back to its default DSN while still being able to kill the
    # process by port - "save and restart" would silently switch databases.
    Start-Process powershell -ArgumentList @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File',
        (Join-Path $PSScriptRoot 'server-manager.ps1'), '-Port', $Port,
        '-DatabaseUrl', $DatabaseUrl
    )
}

# ── tray ────────────────────────────────────────────────────────────────────
$icon = New-Object System.Windows.Forms.NotifyIcon
$icon.Icon = New-TrayIcon $true
$icon.Text = "Webhook 重放平台 · 运行中 :$Port"
$icon.Visible = $true

$menu = New-Object System.Windows.Forms.ContextMenuStrip

$mOpen = $menu.Items.Add('打开界面')
$mOpen.add_Click({ Start-Process "http://127.0.0.1:$Port" })

$menu.Items.Add('-') | Out-Null

$mManage = $menu.Items.Add('服务器设置…')
$mManage.Font = New-Object System.Drawing.Font($mManage.Font, [System.Drawing.FontStyle]::Bold)
$mManage.add_Click({ Open-Manager })

$mToggle = $menu.Items.Add('停止')
$mToggle.add_Click({
        if (Get-Server) { Stop-Server; $mToggle.Text = '启动' }
        else { [void](Start-Server); $mToggle.Text = '停止' }
        Update-Icon
    })

$mLog = $menu.Items.Add('打开日志')
$mLog.add_Click({
        if (Test-Path $logOut) { Start-Process notepad $logOut }
        else { [System.Windows.Forms.MessageBox]::Show('还没有日志文件，先启动一次服务。') }
    })

$mDoctor = $menu.Items.Add('运行自检')
$mDoctor.add_Click({
        # Same encoding trap as in server-manager.ps1: without this the Chinese report
        # arrives as mojibake.
        $prev = [Console]::OutputEncoding
        try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

        $out = & $Exe doctor --port $Port 2>&1 | Out-String

        try { [Console]::OutputEncoding = $prev } catch { }
        [System.Windows.Forms.MessageBox]::Show($out, '自检结果')
    })

$menu.Items.Add('-') | Out-Null

$mExit = $menu.Items.Add('退出（停止服务）')
$mExit.add_Click({
        $icon.Visible = $false
        Stop-Server
        [System.Windows.Forms.Application]::Exit()
    })

$icon.ContextMenuStrip = $menu
$icon.add_DoubleClick({ Start-Process "http://127.0.0.1:$Port" })

$lastRunning = $null

function Update-Icon {
    $running = [bool](Get-Server)

    # Only rebuild when the state actually changes: every call creates a bitmap and an
    # icon handle, and at a 10s tick that is ~8k handles a day - enough to wedge the
    # tray. The tooltip below still refreshes on every tick.
    if ($running -ne $script:lastRunning) {
        $old = $icon.Icon
        $icon.Icon = New-TrayIcon $running

        if ($old -and -not [object]::ReferenceEquals($old, $icon.Icon)) { $old.Dispose() }

        $script:lastRunning = $running
    }

    # The tooltip carries the detail: colour only says "running or not".
    if ($running) {
        $ok = $true
        try {
            $null = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec 2
        }
        catch { $ok = $false }

        $icon.Text = if ($ok) {
            "Webhook 重放平台 · 运行中 :$Port"
        }
        else {
            "Webhook 重放平台 · 端口无响应（打开服务器设置看自检）"
        }
    }
    else {
        $icon.Text = 'Webhook 重放平台 · 已停止'
    }

    $mToggle.Text = if ($running) { '停止' } else { '启动' }
}

$context = New-Object System.Windows.Forms.ApplicationContext
[void](Start-Server)

Write-Host "Webhook 重放平台 已在后台运行：http://127.0.0.1:$Port"
Write-Host '托盘图标：双击打开界面；右键可打开服务器设置、启动/停止、查看日志或退出。'

if ($SelfTest) {
    Start-Sleep -Seconds 5
    $icon.Visible = $false
    Stop-Server
    $icon.Dispose()

    exit 0
}

$timer = New-Object System.Windows.Forms.Timer
$timer.Interval = 10000
$timer.add_Tick({ Update-Icon })
$timer.Start()

Update-Icon

[System.Windows.Forms.Application]::Run($context)

$timer.Stop()
$icon.Visible = $false
Stop-Server
$icon.Dispose()
