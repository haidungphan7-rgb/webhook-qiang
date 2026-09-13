# Runs the platform in the background, so it survives closing the terminal.
#
# Why scheduled tasks and not a Windows service: `sc.exe` can only manage programs that
# implement the service control interface. A plain console binary never calls
# StartServiceCtrlDispatcher, so SCM fails the start with 1053 ("did not respond in a
# timely fashion"). Scheduled tasks are built in, need no changes to the binary, and give
# the same result: starts at logon, runs in the background, keeps running after the
# terminal is closed. On Linux use systemd instead (see docs/部署指南.md) - systemd
# manages a plain foreground process just fine.
#
# Usage (Windows; -Install / -Remove need an elevated shell):
#   pwsh ./scripts/service.ps1 -Status
#   pwsh ./scripts/service.ps1 -Install -DatabaseUrl "postgres://..."
#   pwsh ./scripts/service.ps1 -Stop
#   pwsh ./scripts/service.ps1 -Remove

param(
    [switch]$Install,
    [switch]$Remove,
    [switch]$Status,
    [switch]$Stop,
    [string]$TaskName = 'webhook-zq',
    [uint16]$Port = 8080,
    [string]$DatabaseUrl = $env:DATABASE_URL,
    [string[]]$ExtraArgs = @(),
    [string]$Exe
)

$ErrorActionPreference = 'Stop'

# schtasks writes Chinese errors on zh-CN Windows; without UTF-8 they arrive as mojibake
# and the only clue to a failed install becomes unreadable.
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$root = Split-Path -Parent $PSScriptRoot

# Loaded here and not further down: the -Status and -Stop branches exit before the point
# where this used to be dot-sourced, so they would have run without it.
#
# Stopping the server has exactly one safe implementation: never kill a process whose
# executable cannot be proven to live under this project directory.
. (Join-Path $PSScriptRoot 'lib\process.ps1')
. (Join-Path $PSScriptRoot 'lib\background.ps1')

if (-not $Install -and -not $Remove -and -not $Stop) { $Status = $true }

if (-not $Exe) {
    $Exe = Join-Path $root 'bin/webhook-zq.exe'
    if (-not (Test-Path $Exe)) { $Exe = Join-Path $root 'webhook-zq.exe' }
}

# The launcher holds the environment: a scheduled task cannot pass variables to the
# process it starts.
$launcher = Join-Path $root 'webhook-zq-background.cmd'

function Is-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()

    return (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}

# schtasks writes to stderr when a task does not exist, which under
# ErrorActionPreference=Stop becomes a terminating error. Everything goes through this
# wrapper so a missing task is simply "not there".
function Invoke-Sch([string[]]$schArgs) {
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'

    $out = & schtasks.exe @schArgs 2>&1
    $code = $LASTEXITCODE

    $ErrorActionPreference = $prev

    return @{ Code = $code; Out = ($out -join "`n") }
}

function Task-Exists {
    return ((Invoke-Sch @('/query', '/tn', $TaskName)).Code -eq 0)
}

function Running-Pid {
    $c = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
        Select-Object -First 1

    if ($c) {
        $p = Get-Process -Id $c.OwningProcess -ErrorAction SilentlyContinue
        if ($p) { return $p }
    }

    return $null
}

# ── status ──────────────────────────────────────────────────────────────────
if ($Status) {
    Write-Host '=== 后台运行状态 ===' -ForegroundColor Cyan
    Write-Host "  任务名   $TaskName"
    Write-Host "  程序     $Exe"

    Write-Host "  开机自启 $(if (Task-Exists) { '已设置' } else { '未设置' })" -ForegroundColor $(if (Task-Exists) { 'Green' } else { 'Yellow' })

    $proc = Running-Pid
    if ($proc) {
        Write-Host "  运行中   pid $($proc.Id)（$($proc.ProcessName)）" -ForegroundColor Green
    }
    else {
        Write-Host '  运行中   否' -ForegroundColor Yellow
    }

    try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec 5
        Write-Host "  健康检查 $($r.StatusCode)" -ForegroundColor Green
    }
    catch {
        Write-Host "  健康检查 端口 $Port 无响应" -ForegroundColor Yellow
    }

    Write-Host ''
    Write-Host "  打开     http://127.0.0.1:$Port"

    if (-not (Task-Exists)) {
        Write-Host ''
        Write-Host '  还没有设置开机自启。安装：' -ForegroundColor DarkGray
        Write-Host "    pwsh ./scripts/service.ps1 -Install -DatabaseUrl `"$DatabaseUrl`"（需要管理员）" -ForegroundColor DarkGray
    }

    exit 0
}

# ── stop ────────────────────────────────────────────────────────────────────
if ($Stop) {
    $proc = Running-Pid
    if (-not $proc) {
        Write-Host "端口 $Port 上没有运行中的实例" -ForegroundColor DarkGray
        exit 0
    }

    $r = Stop-AppProcess -Id $proc.Id -Root $root

    if ($r.Stopped) {
        Write-Host "已停止 pid $($r.Id)" -ForegroundColor Green
    }
    else {
        Write-Host "跳过 pid $($r.Id)（$($r.Reason)：$($r.Path)）" -ForegroundColor Yellow
    }
    Write-Host '（开机自启仍然保留，下次登录会再起来；要彻底移除请用 -Remove）' -ForegroundColor DarkGray

    exit 0
}

# ── elevation guard ─────────────────────────────────────────────────────────
if (-not (Is-Admin)) {
    Write-Host '需要管理员权限。请用「以管理员身份运行」的 PowerShell 重跑。' -ForegroundColor Yellow

    exit 1
}

# ── remove ──────────────────────────────────────────────────────────────────
if ($Remove) {
    Write-Host '=== 移除后台运行 ===' -ForegroundColor Cyan

    if (Task-Exists) {
        $res = Invoke-Sch @('/delete', '/tn', $TaskName, '/f')
        if ($res.Code -eq 0) { Write-Host '  已取消开机自启' -ForegroundColor Green }
        else { Write-Host "  取消失败：$($res.Out)" -ForegroundColor Red }
    }
    else {
        Write-Host '  开机自启本来就没设置' -ForegroundColor DarkGray
    }

    $proc = Running-Pid
    if ($proc) {
        $r = Stop-AppProcess -Id $proc.Id -Root $root

        if ($r.Stopped) {
            Write-Host "  已停止当前实例 pid $($r.Id)"
        }
        else {
            Write-Host "  跳过 pid $($r.Id)（$($r.Reason)：$($r.Path)）" -ForegroundColor Yellow
        }
    }

    # The launcher contains the database password, so it must not outlive the task.
    if (Test-Path $launcher) {
        Remove-Item $launcher -Force
        Write-Host "  已删除 $launcher（里面含数据库连接串）"
    }

    Write-Host ''
    Write-Host '  数据没有删除。要连库一起删：pwsh ./scripts/uninstall.ps1 -DropDatabase'

    exit 0
}

# ── install ─────────────────────────────────────────────────────────────────
if (-not (Test-Path $Exe)) {
    Write-Host "找不到程序：$Exe" -ForegroundColor Red
    Write-Host '  先构建：pwsh ./scripts/setup.ps1 -Mode Local -NoStart'
    exit 1
}

if (-not $DatabaseUrl) {
    Write-Host '缺少数据库连接。用 -DatabaseUrl 指定，或先设置 $env:DATABASE_URL。' -ForegroundColor Red
    exit 1
}

Write-Host '=== 设置为开机自启（后台常驻）===' -ForegroundColor Cyan

# One writer for the launcher, and it reads the same config file the GUI uses: the
# autostarted process must never end up with a different configuration than the window
# shows. (This script used to write only --port, and the last writer silently won.)
$cfg = Read-AppConfig -Path (Join-Path $root 'webhook-zq.json')
$cfg.Port = $Port
if ($DatabaseUrl) { $cfg.DatabaseUrl = $DatabaseUrl }

Write-BackgroundLauncher -Path $launcher -Exe $Exe -Config $cfg -ExtraArgs $ExtraArgs | Out-Null
Write-Host "  启动脚本 $launcher"

if (Task-Exists) {
    Invoke-Sch @('/delete', '/tn', $TaskName, '/f') | Out-Null
}

$created = Invoke-Sch @('/create', '/tn', $TaskName, '/tr', "`"$launcher`"", '/sc', 'onlogon', '/rl', 'highest', '/f')

if ($created.Code -ne 0) {
    Write-Host "  创建计划任务失败：$($created.Out)" -ForegroundColor Red
    exit 1
}

Write-Host '  已设置开机自启（登录后自动运行，不显示窗口）' -ForegroundColor Green

# Start it now as well, so the user does not have to log out and back in.
Start-Process -FilePath $launcher -WindowStyle Hidden
Start-Sleep -Seconds 5

try {
    $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec 5
    Write-Host "  健康检查 $($r.StatusCode)  →  http://127.0.0.1:$Port" -ForegroundColor Green
}
catch {
    Write-Host '  健康检查 暂无响应（用 -Status 再看一次）' -ForegroundColor Yellow
}

Write-Host ''
Write-Host '  查看     pwsh ./scripts/service.ps1 -Status'
Write-Host '  停止     pwsh ./scripts/service.ps1 -Stop'
Write-Host '  移除     pwsh ./scripts/service.ps1 -Remove'
Write-Host ''
Write-Host '  注意：它会在每次登录时自动启动。不用了记得 -Remove。' -ForegroundColor Yellow
Write-Host "        启动脚本里含数据库连接串，权限与你的账户相同。" -ForegroundColor DarkGray
