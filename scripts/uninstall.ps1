# Removes the platform from this machine.
#
# Deliberately asks before destroying anything: the database holds captured events and
# replay history that cannot be recreated. Dropping it is a separate, explicit opt-in.
#
# Usage:
#   pwsh ./scripts/uninstall.ps1                 # stop the server, keep everything
#   pwsh ./scripts/uninstall.ps1 -RemoveService  # also unregister the Windows service
#   pwsh ./scripts/uninstall.ps1 -DropDatabase   # also drop the database (asks again)
#   pwsh ./scripts/uninstall.ps1 -RemoveBuild    # also delete bin\ and web\dist
#   pwsh ./scripts/uninstall.ps1 -All            # everything above (still confirms the DB)

param(
    [switch]$RemoveService,
    [switch]$DropDatabase,
    [switch]$RemoveBuild,
    [switch]$All,
    [string]$ServiceName = 'webhook-zq',
    [string]$DbName = 'webhook_rd',
    [string]$Psql
)

$ErrorActionPreference = 'Continue'

# psql / dropdb / schtasks messages are UTF-8; the console code page is GBK on zh-CN.
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# See uninstaller.ps1: the trailing separator is what stops "D:\webhook-zq-evil" from
# matching "D:\webhook-zq".
$rootPrefix = $root.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) +
[IO.Path]::DirectorySeparatorChar

if ($All) { $RemoveService = $true; $DropDatabase = $true; $RemoveBuild = $true }

function Write-Step($text) { Write-Host "`n=== $text ===" -ForegroundColor Cyan }

# psql is often not on PATH; find it the same way setup.ps1 does.
if (-not $Psql) {
    $cmd = Get-Command psql -ErrorAction SilentlyContinue
    if ($cmd) {
        $Psql = $cmd.Source
    }
    else {
        $hit = Get-ChildItem 'C:\Program Files\PostgreSQL\*\bin\psql.exe' -ErrorAction SilentlyContinue |
            Sort-Object FullName -Descending | Select-Object -First 1
        if ($hit) { $Psql = $hit.FullName }
    }
}

if ($Psql) { $env:PATH += ';' + (Split-Path $Psql) }
if (-not $env:PGPASSWORD) { $env:PGPASSWORD = 'postgres' }

# ── 1. stop the running server ──────────────────────────────────────────────
Write-Step '1/4 停止服务'

$stopped = 0

foreach ($name in 'webhook-zq', 'wh') {
    foreach ($p in (Get-Process $name -ErrorAction SilentlyContinue)) {
        # "wh" is a two letter name, so only stop it when the executable is known to live
        # in this project. If the path cannot be read, DO NOT KILL: that is exactly the
        # case where we cannot prove it is ours. (An earlier version fell through to
        # Stop-Process when $path was empty - the protection was inverted.)
        $path = $null
        try { $path = $p.Path } catch { $path = $null }

        if (-not $path -or -not $path.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
            Write-Host "  跳过 pid $($p.Id)（无法确认属于本项目：$path）" -ForegroundColor Yellow
            continue
        }

        Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
        $stopped++
        Write-Host "  已停止进程 $($p.ProcessName) (pid $($p.Id))"
    }
}

if ($stopped -eq 0) { Write-Host '  没有正在运行的实例' -ForegroundColor DarkGray }

# The tray and the manager window are PowerShell processes, not webhook-zq.exe. Leaving
# them alive means the tray notices the server is gone and starts it again - the
# uninstall reports success while the app is still running.
$closed = 0

foreach ($p in (Get-CimInstance Win32_Process -Filter "Name = 'powershell.exe' OR Name = 'pwsh.exe'" -ErrorAction SilentlyContinue)) {
    if ($p.CommandLine -match 'tray\.ps1|server-manager\.ps1|start-gui\.ps1') {
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
        $closed++
        Write-Host "  已关闭桌面端窗口 (pid $($p.ProcessId))"
    }
}

if ($closed -eq 0) { Write-Host '  没有运行中的托盘或管理窗口' -ForegroundColor DarkGray }

# ── 2. windows service ──────────────────────────────────────────────────────
Write-Step '2/4 Windows 服务'

if ($RemoveService) {
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($svc) {
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
        & sc.exe delete $ServiceName | Out-Null
        Write-Host "  已注销服务 $ServiceName" -ForegroundColor Green
    }
    else {
        Write-Host "  服务 $ServiceName 不存在（跳过）" -ForegroundColor DarkGray
    }
}
else {
    # Do not remove, but do report: a service keeps running after the window closes,
    # and "I uninstalled it" would otherwise be wrong.
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($svc) {
        Write-Host "  检测到 Windows 服务 $ServiceName 仍在运行（加 -RemoveService 注销）" -ForegroundColor Yellow
    }
    else {
        Write-Host '  没有注册的 Windows 服务' -ForegroundColor DarkGray
    }
}

# ── 3. database ─────────────────────────────────────────────────────────────
Write-Step '3/4 数据库'

if (-not $DropDatabase) {
    Write-Host "  保留数据库 $DbName（删除请加 -DropDatabase）" -ForegroundColor DarkGray
}
elseif (-not $Psql) {
    Write-Host '  找不到 psql，无法删库。手工执行：dropdb -U postgres webhook_rd' -ForegroundColor Yellow
}
else {
    $exists = (& psql -U postgres -d postgres -tA -c "select 1 from pg_database where datname='$DbName'" 2>$null) -join ''
    if ($exists.Trim() -ne '1') {
        Write-Host "  数据库 $DbName 不存在（跳过）" -ForegroundColor DarkGray
    }
    else {
        Write-Host "  即将删除数据库 $DbName —— 里面的收件箱、事件与重放历史都会消失，不可恢复。" -ForegroundColor Red
        Write-Host "  想留个备份先执行：pg_dump -U postgres $DbName > backup.sql" -ForegroundColor DarkGray

        if (Get-Command docker -ErrorAction SilentlyContinue) {
            Write-Host '  如果当初是用 Docker 起的，数据在容器卷里，需执行：docker compose down -v' -ForegroundColor DarkGray
        }

        $answer = Read-Host "  确认请输入 yes"
        if ($answer -ne 'yes') {
            Write-Host '  已取消，数据库保留' -ForegroundColor Yellow
        }
        else {
            & dropdb -U postgres $DbName 2>$null
            $gone = (& psql -U postgres -d postgres -tA -c "select 1 from pg_database where datname='$DbName'" 2>$null) -join ''
            if ($gone.Trim() -eq '1') {
                Write-Host '  删库失败。手工执行：dropdb -U postgres webhook_rd' -ForegroundColor Red
            }
            else {
                Write-Host '  已删除' -ForegroundColor Green
            }
        }
    }
}

# ── 4. build artefacts ──────────────────────────────────────────────────────
Write-Step '4/4 构建产物'

if (-not $RemoveBuild) {
    Write-Host '  保留（删除请加 -RemoveBuild）' -ForegroundColor DarkGray
}
else {
    # bin\ and web\dist are the build output; logs\ and a stray wh.exe in the repo root
    # (left behind by an older launcher) would otherwise be left behind and make people
    # wonder whether the uninstall actually worked.
    $targets = @(
        (Join-Path $root 'bin'),
        (Join-Path $root 'web/dist'),
        (Join-Path $root 'logs')
    )

    foreach ($name in 'wh.exe', 'webhook-zq-background.cmd', 'webhook-zq.json') {
        $p = Join-Path $root $name
        if (Test-Path $p) { $targets += $p }
    }

    # The launcher holds the database connection string and webhook-zq.json holds the
    # master encryption key - both must not outlive the app.
    $targets += @(Get-ChildItem -Path $root -Filter 'backup-*.sql' -File -ErrorAction SilentlyContinue)

    foreach ($path in $targets) {
        if ($path -and (Test-Path $path)) {
            Remove-Item -Path $path -Recurse -Force -ErrorAction SilentlyContinue
            Write-Host "  已删除 $path"
        }
    }
}

# A scheduled task survives logging off and comes back on the next logon, so uninstalling
# without removing it means the app quietly returns.
$prevEnc = [Console]::OutputEncoding
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }
$task = & schtasks.exe /query /tn 'webhook-zq' 2>&1
try { [Console]::OutputEncoding = $prevEnc } catch { }

if ($LASTEXITCODE -eq 0) {
    $prevEnc = [Console]::OutputEncoding
    try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }
    & schtasks.exe /delete /tn 'webhook-zq' /f 2>&1 | Out-Null
    try { [Console]::OutputEncoding = $prevEnc } catch { }

    Write-Host '  已取消开机自启（计划任务 webhook-zq）' -ForegroundColor Green
}

Write-Host ''
Write-Host '完成。下面是**我们没有动**的东西，需要的话自行处理：' -ForegroundColor Cyan
Write-Host '  · PostgreSQL 本体与它的数据目录 —— 在「应用和功能」里单独卸载（不是我们装的）'
Write-Host '  · web\node_modules —— 属于源码目录，随目录一起删'
Write-Host ''

# "Uninstalled" should not leave people guessing whether the project directory counts.
Write-Host '想连程序一起彻底删除？退出本目录后执行：' -ForegroundColor Cyan
Write-Host ('  Remove-Item -Recurse -Force "' + $root + '"') -ForegroundColor White
Write-Host ''
Write-Host '只想重新开始？不用卸载，直接：pwsh ./scripts/setup.ps1' -ForegroundColor DarkGray
