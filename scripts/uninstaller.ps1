# Graphical uninstaller.
#
# The command line script (uninstall.ps1) works, but it asks you to remember -All and to
# know that the project directory is yours to delete. This shows what is actually on the
# machine, lets you tick what to remove, and refuses to guess about the database.
#
#   pwsh ./scripts/uninstaller.ps1            # the window
#   pwsh ./scripts/uninstaller.ps1 -ScanOnly  # print what would be removed and exit

param(
    [switch]$ScanOnly
)

$ErrorActionPreference = 'Continue'

# psql / schtasks output is UTF-8, the console reads GBK.
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# A bare StartsWith($root) also matches sibling directories: "D:\webhook-zq-evil\wh.exe"
# starts with "D:\webhook-zq" too. The trailing separator is what makes it mean "inside
# this project" - without it the uninstaller would stop (and later delete) processes
# belonging to D:\webhook-zq-old, D:\webhook-zq2 and friends.
$rootPrefix = $root.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) +
[IO.Path]::DirectorySeparatorChar

# ── locate the PostgreSQL tools ─────────────────────────────────────────────
# The installer does not add its bin directory to PATH, so "psql is missing" is almost
# always wrong - it is just not on PATH.
$psql = (Get-Command psql -ErrorAction SilentlyContinue).Source
if (-not $psql) {
    $hit = Get-ChildItem 'C:\Program Files\PostgreSQL\*\bin\psql.exe' -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending | Select-Object -First 1
    if ($hit) { $psql = $hit.FullName }
}
if ($psql) { $env:PATH += ';' + (Split-Path $psql) }
if (-not $env:PGPASSWORD) { $env:PGPASSWORD = 'postgres' }

$script:items = [System.Collections.ArrayList]::new()

function Add-Item {
    param(
        [string]$Key,
        [string]$Label,
        [string]$Detail,
        [bool]$Selected,
        [bool]$Dangerous,
        [scriptblock]$Action
    )

    [void]$script:items.Add([pscustomobject]@{
            Key       = $Key
            Label     = $Label
            Detail    = $Detail
            Selected  = $Selected
            Dangerous = $Dangerous
            Action    = $Action
        })
}

function Get-Size([string]$path) {
    if (-not (Test-Path $path)) { return 0 }

    if ((Get-Item $path) -is [System.IO.DirectoryInfo]) {
        return (Get-ChildItem $path -Recurse -File -ErrorAction SilentlyContinue |
            Measure-Object -Property Length -Sum).Sum
    }

    return (Get-Item $path).Length
}

function Format-Size([double]$bytes) {
    if ($bytes -gt 1GB) { return '{0:N1} GB' -f ($bytes / 1GB) }
    if ($bytes -gt 1MB) { return '{0:N1} MB' -f ($bytes / 1MB) }
    if ($bytes -gt 1KB) { return '{0:N0} KB' -f ($bytes / 1KB) }

    return '{0:N0} B' -f $bytes
}

# ── scan ────────────────────────────────────────────────────────────────────

# 1. running server processes
$appProcs = @()
foreach ($name in 'webhook-zq', 'wh') {
    foreach ($p in (Get-Process $name -ErrorAction SilentlyContinue)) {
        # Only claim processes we can prove belong to this project: a two letter name like
        # "wh" is far too generic to kill on trust.
        $path = $null
        try { $path = $p.Path } catch { $path = $null }
        if ($path -and $path.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
            $appProcs += $p
        }
    }
}

if ($appProcs.Count -gt 0) {
    Add-Item 'process' "停止服务进程" "$($appProcs.Count) 个正在运行" $true $false {
        foreach ($p in $appProcs) {
            Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
        }
        return "已停止 $($appProcs.Count) 个进程"
    }
}

# 2. tray / manager windows (PowerShell processes - killing the exe leaves them alive,
#    and the tray will simply start the server again)
$winProcs = @(Get-CimInstance Win32_Process -Filter "Name = 'powershell.exe' OR Name = 'pwsh.exe'" -ErrorAction SilentlyContinue |
    Where-Object { $_.CommandLine -match 'tray\.ps1|server-manager\.ps1|start-gui\.ps1' })

if ($winProcs.Count -gt 0) {
    Add-Item 'windows' "关闭托盘与管理窗口" "$($winProcs.Count) 个窗口（否则托盘会重启服务）" $true $false {
        foreach ($p in $winProcs) {
            Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
        }
        return "已关闭 $($winProcs.Count) 个窗口"
    }
}

# 3. Windows service
$svc = Get-Service -Name 'webhook-zq' -ErrorAction SilentlyContinue
if ($svc) {
    Add-Item 'service' "注销 Windows 服务" "webhook-zq（当前状态：$($svc.Status)）" $true $false {
        Stop-Service -Name 'webhook-zq' -Force -ErrorAction SilentlyContinue
        & sc.exe delete 'webhook-zq' | Out-Null

        return '已注销服务'
    }
}

# 4. scheduled task (autostart)
$prevEnc = [Console]::OutputEncoding
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }
$null = & schtasks.exe /query /tn 'webhook-zq' 2>&1
$taskExists = ($LASTEXITCODE -eq 0)
try { [Console]::OutputEncoding = $prevEnc } catch { }

if ($taskExists) {
    Add-Item 'autostart' "取消开机自启" "计划任务 webhook-zq（不取消的话登录后会自动回来）" $true $false {
        & schtasks.exe /delete /tn 'webhook-zq' /f 2>&1 | Out-Null

        return '已取消开机自启'
    }
}

# 5. database - reported with its contents, never selected by default
if ($psql) {
    $row = (& psql -U postgres -d postgres -tA -c "select pg_database_size('webhook_rd')" 2>$null) -join ''
    if ($row.Trim() -match '^\d+$') {
        $counts = (& psql -U postgres -d webhook_rd -tA -c "select (select count(*) from inbox), (select count(*) from event), (select count(*) from replay_attempt)" 2>$null) -join ''
        $parts = $counts.Split('|')

        Add-Item 'database' "删除数据库" "webhook_rd · $(Format-Size ([double]$row)) · $($parts[0]) 个收件箱 / $($parts[1]) 条事件" $false $true {
            & dropdb -U postgres webhook_rd 2>$null
            $gone = (& psql -U postgres -d postgres -tA -c "select 1 from pg_database where datname='webhook_rd'" 2>$null) -join ''

            if ($gone.Trim() -eq '1') { return '删库失败，可手工执行：dropdb -U postgres webhook_rd' }

            return '已删除数据库'
        }
    }
}

# 6. build output and runtime state
$dirs = @()
foreach ($d in 'bin', 'web/dist', 'logs') {
    $full = Join-Path $root $d
    if (Test-Path $full) { $dirs += $full }
}

$files = @()
foreach ($f in 'webhook-zq.json', 'webhook-zq-background.cmd', 'wh.exe') {
    $full = Join-Path $root $f
    if (Test-Path $full) { $files += $full }
}

$files += @(Get-ChildItem -Path $root -Filter 'backup-*.sql' -File -ErrorAction SilentlyContinue | ForEach-Object { $_.FullName })

if (($dirs.Count + $files.Count) -gt 0) {
    $bytes = 0
    foreach ($p in (@($dirs) + @($files))) { $bytes += (Get-Size $p) }

    Add-Item 'build' "删除构建产物与运行配置" "$($dirs.Count) 个目录、$($files.Count) 个文件 · $(Format-Size $bytes)（含数据库连接串与加密密钥）" $true $false {
        foreach ($p in (@($dirs) + @($files))) {
            if (Test-Path $p) { Remove-Item -Path $p -Recurse -Force -ErrorAction SilentlyContinue }
        }

        return '已删除构建产物与配置'
    }
}

if ($ScanOnly) {
    Write-Host "`n在 $root 扫描到以下内容：`n" -ForegroundColor Cyan

    foreach ($it in $script:items) {
        $mark = if ($it.Selected) { '[默认勾选]' } else { '[默认保留]' }
        Write-Host "  $mark $($it.Label) —— $($it.Detail)"
    }

    if ($script:items.Count -eq 0) { Write-Host '  没有发现任何残留，这台机器是干净的。' -ForegroundColor Green }

    Write-Host "`n没有列出来的：PostgreSQL 本体、项目源码目录（都不是本程序该替你删的）。`n"

    exit 0
}

# ── the window ──────────────────────────────────────────────────────────────
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()

$form = New-Object System.Windows.Forms.Form
$form.Text = '卸载 webhook 重放平台'
$form.Size = New-Object System.Drawing.Size(660, 540)
$form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false

$lblIntro = New-Object System.Windows.Forms.Label
$lblIntro.Text = "下面是在这台机器上找到的内容。勾选要删除的项，然后点「开始卸载」。`n数据库默认不勾选 —— 里面的事件删了就找不回来。"
$lblIntro.Location = New-Object System.Drawing.Point(16, 14)
$lblIntro.Size = New-Object System.Drawing.Size(610, 40)
$form.Controls.Add($lblIntro)

$list = New-Object System.Windows.Forms.ListView
$list.Location = New-Object System.Drawing.Point(16, 60)
$list.Size = New-Object System.Drawing.Size(610, 210)
$list.View = 'Details'
$list.CheckBoxes = $true
$list.FullRowSelect = $true
$list.GridLines = $true
$list.HideSelection = $false
[void]$list.Columns.Add('清理项', 190)
[void]$list.Columns.Add('说明', 400)

foreach ($it in $script:items) {
    $row = New-Object System.Windows.Forms.ListViewItem($it.Label)
    [void]$row.SubItems.Add($it.Detail)
    $row.Checked = $it.Selected
    $row.Tag = $it
    if ($it.Dangerous) { $row.ForeColor = [System.Drawing.Color]::FromArgb(190, 40, 40) }
    [void]$list.Items.Add($row)
}

if ($script:items.Count -eq 0) {
    $empty = New-Object System.Windows.Forms.ListViewItem('（没有发现残留）')
    [void]$empty.SubItems.Add('这台机器上没有找到 webhook-zq 的服务、自启或构建产物。')
    $empty.ForeColor = [System.Drawing.Color]::Gray
    [void]$list.Items.Add($empty)
}

$form.Controls.Add($list)

$log = New-Object System.Windows.Forms.TextBox
$log.Location = New-Object System.Drawing.Point(16, 282)
$log.Size = New-Object System.Drawing.Size(610, 150)
$log.Multiline = $true
$log.ScrollBars = 'Vertical'
$log.ReadOnly = $true
$log.BackColor = [System.Drawing.Color]::FromArgb(250, 250, 250)
$form.Controls.Add($log)

function Write-Log($text) {
    $log.AppendText("· $text`r`n")
    $form.Refresh()
}

$btnRun = New-Object System.Windows.Forms.Button
$btnRun.Text = '开始卸载'
$btnRun.Location = New-Object System.Drawing.Point(400, 448)
$btnRun.Size = New-Object System.Drawing.Size(110, 34)
$btnRun.BackColor = [System.Drawing.Color]::FromArgb(200, 60, 60)
$btnRun.ForeColor = [System.Drawing.Color]::White
$btnRun.Add_Click({
        $chosen = @($list.Items | Where-Object { $_.Checked -and $_.Tag })

        if ($chosen.Count -eq 0) {
            [System.Windows.Forms.MessageBox]::Show('没有勾选任何项目。', '提示')
            return
        }

        # The database is the only thing that cannot be recreated, so it gets its own
        # confirmation even after being ticked.
        $dbRow = $chosen | Where-Object { $_.Tag.Key -eq 'database' }
        if ($dbRow) {
            $answer = [System.Windows.Forms.MessageBox]::Show(
                "数据库 webhook_rd 里的收件箱、事件与重放历史将被永久删除，不可恢复。`n`n确定要删除数据库吗？",
                '确认删除数据库', 'YesNo', 'Warning')

            if ($answer -ne 'Yes') {
                $dbRow.Checked = $false
                $chosen = @($chosen | Where-Object { $_.Tag.Key -ne 'database' })
                Write-Log '已取消对数据库的删除'
            }
        }

        $btnRun.Enabled = $false
        $btnOpen.Enabled = $false

        foreach ($row in $chosen) {
            $it = $row.Tag

            try {
                $result = & $it.Action
                Write-Log "$($it.Label)：$result"
            }
            catch {
                Write-Log "$($it.Label)：失败 —— $($_.Exception.Message)"
            }
        }

        $btnRun.Enabled = $true
        $btnOpen.Enabled = $true

        Write-Log '完成。'
        Write-Log '下面的东西我们没有动：PostgreSQL 本体、项目源码目录。'

        [System.Windows.Forms.MessageBox]::Show(
            "卸载完成。`n`nPostgreSQL 本体与项目源码目录我们没有删除 —— 点「打开项目目录」可自行处理。",
            '卸载完成')
    })
$form.Controls.Add($btnRun)

$btnOpen = New-Object System.Windows.Forms.Button
$btnOpen.Text = '打开项目目录'
$btnOpen.Location = New-Object System.Drawing.Point(268, 448)
$btnOpen.Size = New-Object System.Drawing.Size(120, 34)
$btnOpen.Add_Click({ Start-Process explorer.exe $root })
$form.Controls.Add($btnOpen)

$btnCancel = New-Object System.Windows.Forms.Button
$btnCancel.Text = '关闭'
$btnCancel.Location = New-Object System.Drawing.Point(516, 448)
$btnCancel.Size = New-Object System.Drawing.Size(110, 34)
$btnCancel.Add_Click({ $form.Close() })
$form.Controls.Add($btnCancel)

[void]$form.ShowDialog()
