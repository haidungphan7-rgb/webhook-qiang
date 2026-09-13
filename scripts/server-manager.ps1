# Server manager window (opened from the tray icon).
#
# WHAT THIS IS: a start-command editor. Its only output is a set of command line
# arguments, and the only way it applies a change is: stop the process, start it again
# with the new arguments. Nothing is hot-reloaded.
#
# That constraint is the whole point. A web admin panel is different: it would let
# anyone who can reach port 8080 change the policy the running process uses. This window
# can only be driven by someone already logged into this machine, which is the same
# privilege as editing the command by hand.
#
# Two rules follow from it and are enforced below:
#   * Nothing that widens the SSRF gate can be switched on here (see the read-only
#     section). The window may only tighten it.
#   * The status area shows what the SERVER reports, never what was typed here.
#
# Usage:
#   pwsh ./scripts/server-manager.ps1
#   pwsh ./scripts/server-manager.ps1 -Port 8080

param(
    [uint16]$Port = 8080,
    [string]$DatabaseUrl = $env:DATABASE_URL,
    [string]$Exe
)

$ErrorActionPreference = 'Continue'

# Same encoding trap as tray.ps1: doctor and pg_dump emit UTF-8, the console reads GBK.
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$root = Split-Path -Parent $PSScriptRoot

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

if (-not $Exe) {
    $Exe = Join-Path $root 'bin/webhook-zq.exe'
    if (-not (Test-Path $Exe)) { $Exe = Join-Path $root 'webhook-zq.exe' }
}

$settingsFile = Join-Path $root 'webhook-zq.json'
$launcher = Join-Path $root 'webhook-zq-background.cmd'

# ── settings ────────────────────────────────────────────────────────────────
# Only this window reads and writes this file. The binary never sees it: everything is
# expanded into command line arguments (or a process level environment variable for
# secrets) before the process starts, so the binary still has exactly one source of
# configuration.
# One definition of the defaults, in scripts/lib/launcher.ps1. This window used to carry
# its own copy (17 keys, including an EncryptKey the library did not have), so the two
# drifted apart - the same class of bug as the two writers of
# webhook-zq-background.cmd, and the reason that file is fixed.
$cfg = Read-AppConfig -Path $settingsFile
$cfg.Port = $Port

# Secrets are read from the environment, never from the file.
if ($env:ENCRYPT_KEY) { $cfg.EncryptKey = $env:ENCRYPT_KEY }
if ($env:DATABASE_URL) { $cfg.DatabaseUrl = $env:DATABASE_URL }

function Get-Server {
    $c = Get-NetTCPConnection -LocalPort ([uint16]$cfg.Port) -State Listen -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($c) { return Get-Process -Id $c.OwningProcess -ErrorAction SilentlyContinue }

    return $null
}

function Mask-Dsn($dsn) {
    try {
        $u = [System.Uri]::new($dsn)
        if ($u.UserInfo) { return $dsn.Replace($u.UserInfo, ($u.UserInfo.Split(':')[0] + ':***')) }
    }
    catch { }

    return $dsn
}

# Building the argument list and writing the launcher both live in
# scripts/lib/background.ps1 now. They used to be duplicated: service.ps1 wrote only
# --port while this window wrote the full list, so whichever ran last silently decided
# what the autostarted process did.
. (Join-Path $PSScriptRoot 'lib\background.ps1')

# Stopping the server has exactly one safe implementation: never kill a process whose
# executable cannot be proven to live under this project directory.
. (Join-Path $PSScriptRoot 'lib\process.ps1')

function Build-Command {
    return (ConvertTo-StartArgs $cfg)
}

function Build-Launcher {
    Write-BackgroundLauncher -Path $launcher -Exe $Exe -Config $cfg | Out-Null
}

function Start-Server {
    Build-Launcher

    # Secrets go through the environment so they never appear in the process command
    # line (visible to every user via task manager / ps).
    $prevDb = $env:DATABASE_URL
    $env:DATABASE_URL = $cfg.DatabaseUrl

    Start-Process -FilePath $launcher -WindowStyle Hidden | Out-Null

    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Milliseconds 500
        try {
            $null = Invoke-WebRequest -Uri "http://127.0.0.1:$($cfg.Port)/healthz" -UseBasicParsing -TimeoutSec 2
            $env:DATABASE_URL = $prevDb

            return $true
        }
        catch { }
    }

    $env:DATABASE_URL = $prevDb

    return $false
}

function Stop-Server {
    $proc = Get-Server
    if (-not $proc) { return }

    $r = Stop-AppProcess -Id $proc.Id -Root $root

    if (-not $r.Stopped) {
        Write-Host "跳过 pid $($r.Id)（$($r.Reason)：$($r.Path)）" -ForegroundColor Yellow
    }
}

function Save-Settings {
    # The master encryption key is never written to disk. It comes from the environment
    # (that is a deployment decision), and a plaintext key in a file next to the source
    # is one "git add ." away from being published.
    $toSave = @{}
    foreach ($k in $cfg.Keys) {
        if ($k -ne 'EncryptKey') { $toSave[$k] = $cfg[$k] }
    }

    $toSave | ConvertTo-Json | Set-Content $settingsFile -Encoding UTF8
}

# ── form ────────────────────────────────────────────────────────────────────
# Same bolt as the tray icon and the web header, so the taskbar and Alt+Tab show the
# product and not a generic window.
function New-AppIcon {
    $bmp = New-Object System.Drawing.Bitmap 64, 64
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode = 'AntiAlias'
    $g.Clear([System.Drawing.Color]::Transparent)

    $brush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 0, 82, 217))
    $g.FillRectangle($brush, 0, 0, 64, 64)

    $g.FillPolygon([System.Drawing.Brushes]::White, @(
            [System.Drawing.Point]::new(42, 8), [System.Drawing.Point]::new(42, 28),
            [System.Drawing.Point]::new(56, 28), [System.Drawing.Point]::new(28, 56),
            [System.Drawing.Point]::new(28, 36), [System.Drawing.Point]::new(14, 36)
        ))

    $g.Dispose(); $brush.Dispose()

    return [System.Drawing.Icon]::FromHandle($bmp.GetHicon())
}

$form = New-Object System.Windows.Forms.Form
$form.Text = 'Webhook 重放平台 · 服务器设置'
$form.Icon = New-AppIcon
$form.Size = New-Object System.Drawing.Size(600, 680)
$form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false
$form.Font = New-Object System.Drawing.Font('Microsoft YaHei UI', 9)

function New-Label($text, $x, $y, $w, $h = 20) {
    $l = New-Object System.Windows.Forms.Label
    $l.Text = $text
    $l.Location = New-Object System.Drawing.Point($x, $y)
    $l.Size = New-Object System.Drawing.Size($w, $h)

    return $l
}

$dirty = New-Object System.Collections.ArrayList

# ── header ──────────────────────────────────────────────────────────────────
$lblBanner = New-Label '这个窗口只编辑「启动命令」：任何修改都要重启服务后才生效。' 12 12 560 20
$lblBanner.ForeColor = [System.Drawing.Color]::FromArgb(255, 0, 82, 217)

$lblDirty = New-Label '' 12 34 560 20
$lblDirty.ForeColor = [System.Drawing.Color]::FromArgb(255, 200, 90, 0)

# ── status ──────────────────────────────────────────────────────────────────
$grpStatus = New-Object System.Windows.Forms.GroupBox
$grpStatus.Text = '状态（这里显示的是服务端实际生效的值）'
$grpStatus.Location = New-Object System.Drawing.Point(12, 60)
$grpStatus.Size = New-Object System.Drawing.Size(560, 110)

$lblState = New-Label '—' 16 26 528 22
$lblState.Font = New-Object System.Drawing.Font('Microsoft YaHei UI', 10, [System.Drawing.FontStyle]::Bold)
$lblDetail = New-Label '' 16 52 528 20
$lblEffective = New-Label '' 16 76 528 24
$grpStatus.Controls.AddRange(@($lblState, $lblDetail, $lblEffective))

# ── read only security facts ────────────────────────────────────────────────
$grpSecurity = New-Object System.Windows.Forms.GroupBox
$grpSecurity.Text = '安全相关（只读；这里只能收紧，不能放宽）'
$grpSecurity.Location = New-Object System.Drawing.Point(12, 178)
$grpSecurity.Size = New-Object System.Drawing.Size(560, 118)

$lblEnc = New-Label '' 16 24 528 20
$lblReplay = New-Label '' 16 46 528 20
$lblHeaders = New-Label '' 16 68 528 20

$btnTighten = New-Object System.Windows.Forms.Button
$btnTighten.Text = '恢复安全默认（清空内网放行）'
$btnTighten.Location = New-Object System.Drawing.Point(16, 88)
$btnTighten.Size = New-Object System.Drawing.Size(220, 24)
$btnTighten.Enabled = $false
$btnTighten.add_Click({
        $cfg.AllowHosts = @()
        $cfg.AllowPrivate = $false
        Save-Settings
        Stop-Server
        Start-Sleep -Seconds 1
        [void](Start-Server)
        Update-Status
        [System.Windows.Forms.MessageBox]::Show('已清空内网放行名单并重启。')
    })

$grpSecurity.Controls.AddRange(@($lblEnc, $lblReplay, $lblHeaders, $btnTighten))

# ── editable settings ───────────────────────────────────────────────────────
$grpSettings = New-Object System.Windows.Forms.GroupBox
$grpSettings.Text = '启动参数'
$grpSettings.Location = New-Object System.Drawing.Point(12, 304)
$grpSettings.Size = New-Object System.Drawing.Size(560, 250)

$y = 26

function New-Field($label, $value, $key, $w = 120) {
    $script:yRef = $script:y

    $l = New-Label $label 16 $script:y 240 20
    $t = New-Object System.Windows.Forms.TextBox
    $t.Text = $value
    $t.Location = New-Object System.Drawing.Point(260, ($script:y - 2))
    $t.Size = New-Object System.Drawing.Size($w, 22)

    $mark = New-Label '' 390 ($script:y) 90 20
    $mark.ForeColor = [System.Drawing.Color]::FromArgb(255, 200, 90, 0)

    $t.add_TextChanged({
            param($s, $e)
            $mark.Text = '● 待重启'
            if (-not $script:dirty.Contains($key)) { [void]$script:dirty.Add($key) }
            Update-Dirty
        }.GetNewClosure())

    $script:y = $script:y + 28

    return @($l, $t, $mark)
}

function Update-Dirty {
    if ($script:dirty.Count -gt 0) {
        $lblDirty.Text = "有 $($script:dirty.Count) 项已修改，需要重启才会生效。"
        $btnSave.Text = '保存并重启'
        $btnSave.Enabled = $true
    }
    else {
        $lblDirty.Text = ''
        $btnSave.Text = '关闭'
        $btnSave.Enabled = $false
    }
}

$script:y = 26

foreach ($c in (New-Field '端口 --port' $cfg.Port 'Port' 80)) { $grpSettings.Controls.Add($c) }
$txtPort = $grpSettings.Controls[$grpSettings.Controls.Count - 2]

foreach ($c in (New-Field '监听地址 --addr（127.0.0.1 仅本机 / 0.0.0.0 局域网）' $cfg.Addr 'Addr' 110)) { $grpSettings.Controls.Add($c) }
$txtAddr = $grpSettings.Controls[$grpSettings.Controls.Count - 2]

foreach ($c in (New-Field '对外根地址 --public-url-root' $cfg.PublicUrlRoot 'PublicUrlRoot' 250)) { $grpSettings.Controls.Add($c) }
$txtPublic = $grpSettings.Controls[$grpSettings.Controls.Count - 2]

foreach ($c in (New-Field '访问密钥 --auth-token（不进命令行）' $cfg.AuthToken 'AuthToken' 200)) { $grpSettings.Controls.Add($c) }
$txtAuth = $grpSettings.Controls[$grpSettings.Controls.Count - 2]
$txtAuth.UseSystemPasswordChar = $true

foreach ($c in (New-Field '重放超时 --replay-timeout' $cfg.Timeout 'Timeout' 80)) { $grpSettings.Controls.Add($c) }
$txtTimeout = $grpSettings.Controls[$grpSettings.Controls.Count - 2]

foreach ($c in (New-Field '重放限流 --replay-rate-limit（0 不限）' $cfg.RateLimit 'RateLimit' 80)) { $grpSettings.Controls.Add($c) }
$txtRate = $grpSettings.Controls[$grpSettings.Controls.Count - 2]

foreach ($c in (New-Field '每箱保留事件数' $cfg.KeepEvents 'KeepEvents' 80)) { $grpSettings.Controls.Add($c) }
$txtKeep = $grpSettings.Controls[$grpSettings.Controls.Count - 2]

# ── buttons ─────────────────────────────────────────────────────────────────
$btnSave = New-Object System.Windows.Forms.Button
$btnSave.Text = '关闭'
$btnSave.Location = New-Object System.Drawing.Point(12, 566)
$btnSave.Size = New-Object System.Drawing.Size(140, 34)
$btnSave.Enabled = $false

$btnSave.add_Click({
        if ($script:dirty.Count -eq 0) { $form.Close(); return }

        if ($txtAddr.Text.Trim() -eq '0.0.0.0' -and -not $txtAuth.Text.Trim()) {
            [System.Windows.Forms.MessageBox]::Show(
                '监听 0.0.0.0 时必须先设置访问密钥，否则局域网内任何人都能打开这个界面。',
                '不允许保存', 'OK', 'Warning')
            return
        }

        $backup = $cfg.Clone()

        $cfg.Port = [uint16]$txtPort.Text
        $cfg.Addr = $txtAddr.Text.Trim()
        $cfg.PublicUrlRoot = $txtPublic.Text.Trim()
        $cfg.AuthToken = $txtAuth.Text.Trim()
        $cfg.Timeout = $txtTimeout.Text.Trim()
        $cfg.RateLimit = $txtRate.Text.Trim()
        $cfg.KeepEvents = $txtKeep.Text.Trim()

        Save-Settings
        Stop-Server
        Start-Sleep -Seconds 1

        if (-not (Start-Server)) {
            foreach ($k in $backup.Keys) { $cfg[$k] = $backup[$k] }
            Save-Settings
            Stop-Server
            Start-Sleep -Seconds 1
            [void](Start-Server)

            [System.Windows.Forms.MessageBox]::Show('新配置启动失败，已恢复到修改前的设置。', '已回滚', 'OK', 'Warning')
            $script:dirty.Clear()
            Update-Dirty
            Update-Status

            return
        }

        # Read back what the server actually reports - never trust what was typed.
        $applied = @()
        $notApplied = @()

        try {
            $s = Invoke-RestMethod "http://127.0.0.1:$($cfg.Port)/api/v1/settings"
            if ($s.replay_timeout_ms -eq [int]($cfg.Timeout.TrimEnd('s')) * 1000) { $applied += '超时' } else { $notApplied += "超时（服务端 $($s.replay_timeout_ms)ms）" }
            if ($s.replay_rate_limit -eq [int]$cfg.RateLimit) { $applied += '限流' } else { $notApplied += "限流（服务端 $($s.replay_rate_limit)）" }
            if ($s.retention_max_events -eq [int]$cfg.KeepEvents) { $applied += '保留条数' } else { $notApplied += "保留条数（服务端 $($s.retention_max_events)）" }
        }
        catch { }

        $msg = "已重启。"
        if ($applied.Count -gt 0) { $msg += "`n已生效：" + ($applied -join '、') }
        if ($notApplied.Count -gt 0) { $msg += "`n未按预期生效：" + ($notApplied -join '、') }

        [System.Windows.Forms.MessageBox]::Show($msg, '完成')

        $script:dirty.Clear()
        Update-Dirty
        Update-Status
    })

$btnCopy = New-Object System.Windows.Forms.Button
$btnCopy.Text = '复制启动命令'
$btnCopy.Location = New-Object System.Drawing.Point(160, 566)
$btnCopy.Size = New-Object System.Drawing.Size(130, 34)
$btnCopy.add_Click({
        $text = "webhook-zq " + ((Build-Command | ForEach-Object { if ($_ -match '\s') { '"' + $_ + '"' } else { $_ } }) -join ' ')
        [System.Windows.Forms.Clipboard]::SetText($text)
        [System.Windows.Forms.MessageBox]::Show($text, '已复制到剪贴板（这就是这个窗口的全部产出）')
    })

$btnBackup = New-Object System.Windows.Forms.Button
$btnBackup.Text = '备份数据库'
$btnBackup.Location = New-Object System.Drawing.Point(298, 566)
$btnBackup.Size = New-Object System.Drawing.Size(110, 34)

# The PostgreSQL installer does not add its bin directory to PATH, so "pg_dump is on the
# PATH" fails on almost every default installation. Probe the usual locations first,
# newest version wins.
function Resolve-PgDump {
    $cmd = Get-Command pg_dump -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    $installed = Get-ChildItem 'C:\Program Files\PostgreSQL\*\bin\pg_dump.exe' -ErrorAction SilentlyContinue |
        Sort-Object { $_.Directory.Parent.Name } -Descending

    if ($installed) { return $installed[0].FullName }

    return $null
}

$btnBackup.add_Click({
        $path = Join-Path $root ("backup-" + (Get-Date -Format 'yyyyMMdd-HHmmss') + ".sql")

        # Honour the configured DSN instead of assuming a local default, and judge by the
        # exit code plus a non-empty file: pg_dump -f creates the file even when it fails,
        # so "the file exists" used to report success for an empty backup.
        $user = 'postgres'; $host1 = '127.0.0.1'; $port = '5432'; $db = 'webhook_rd'; $pass = ''
        try {
            $u = [System.Uri]::new($cfg.DatabaseUrl)
            if ($u.UserInfo) {
                $parts = $u.UserInfo.Split(':', 2)
                $user = $parts[0]
                if ($parts.Count -gt 1) { $pass = [System.Uri]::UnescapeDataString($parts[1]) }
            }
            if ($u.Host) { $host1 = $u.Host }
            if ($u.Port -gt 0) { $port = $u.Port }
            $db = $u.AbsolutePath.TrimStart('/')
        }
        catch { }

        $prevPass = $env:PGPASSWORD
        $env:PGPASSWORD = $pass

        try {
            $pgDump = Resolve-PgDump

            if (-not $pgDump) {
                $out = "找不到 pg_dump。PostgreSQL 安装后其 bin 目录通常不在 PATH 上；`n请安装 PostgreSQL，或把其 bin 目录加入 PATH。"
                $code = 1
            }
            else {
                $out = & $pgDump -U $user -h $host1 -p $port -d $db -f $path 2>&1 | Out-String
                $code = $LASTEXITCODE
            }
        }
        finally {
            $env:PGPASSWORD = $prevPass
        }

        $ok = ($code -eq 0) -and (Test-Path $path) -and ((Get-Item $path).Length -gt 0)

        if ($ok) {
            [System.Windows.Forms.MessageBox]::Show("已备份到：`n$path")
        }
        else {
            if (Test-Path $path) { Remove-Item $path -Force -ErrorAction SilentlyContinue }
            [System.Windows.Forms.MessageBox]::Show("备份失败。$out`n`n请确认 pg_dump 可用（已自动探测常见安装目录），且数据库连接可用。", '备份失败', 'OK', 'Error')
        }
    })

$btnDoctor = New-Object System.Windows.Forms.Button
$btnDoctor.Text = '运行自检'
$btnDoctor.Location = New-Object System.Drawing.Point(416, 566)
$btnDoctor.Size = New-Object System.Drawing.Size(100, 34)
$btnDoctor.add_Click({
        # The binary emits UTF-8; without this PowerShell decodes it with the console
        # code page (GBK on zh-CN Windows) and the report turns into mojibake.
        $prev = [Console]::OutputEncoding
        try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

        $out = & $Exe doctor --port $cfg.Port 2>&1 | Out-String

        try { [Console]::OutputEncoding = $prev } catch { }
        [System.Windows.Forms.MessageBox]::Show($out, '自检结果')
    })

$btnOpen = New-Object System.Windows.Forms.Button
$btnOpen.Text = '打开界面'
$btnOpen.Location = New-Object System.Drawing.Point(462, 320)
$btnOpen.Size = New-Object System.Drawing.Size(100, 28)
$btnOpen.add_Click({ Start-Process "http://127.0.0.1:$($cfg.Port)" })

$grpSettings.Controls.Add($btnOpen)

# ── more actions ────────────────────────────────────────────────────────────
$btnRefresh = New-Object System.Windows.Forms.Button
$btnRefresh.Text = '刷新'
$btnRefresh.Location = New-Object System.Drawing.Point(462, 90)
$btnRefresh.Size = New-Object System.Drawing.Size(100, 24)
$btnRefresh.add_Click({ Update-Status })

$btnLog = New-Object System.Windows.Forms.Button
$btnLog.Text = '打开日志'
$btnLog.Location = New-Object System.Drawing.Point(462, 118)
$btnLog.Size = New-Object System.Drawing.Size(100, 24)
$btnLog.add_Click({
        $log = Join-Path $root 'logs/server.log'
        if (Test-Path $log) { Start-Process notepad $log }
        else { [System.Windows.Forms.MessageBox]::Show('还没有日志；用托盘图标启动一次服务后就会生成。') }
    })

$btnStop = New-Object System.Windows.Forms.Button
$btnStop.Text = '停止服务'
$btnStop.Location = New-Object System.Drawing.Point(462, 146)
$btnStop.Size = New-Object System.Drawing.Size(100, 24)
$btnStop.add_Click({ Stop-Server; Update-Status })

$btnStartNow = New-Object System.Windows.Forms.Button
$btnStartNow.Text = '启动服务'
$btnStartNow.Location = New-Object System.Drawing.Point(462, 174)
$btnStartNow.Size = New-Object System.Drawing.Size(100, 24)
$btnStartNow.add_Click({ [void](Start-Server); Update-Status })

$grpStatus.Controls.AddRange(@($btnRefresh, $btnLog, $btnStop, $btnStartNow))

$form.Controls.AddRange(@($lblBanner, $lblDirty, $grpStatus, $grpSecurity, $grpSettings, $btnSave, $btnCopy, $btnBackup, $btnDoctor))

# Closing the window must not look like stopping the server.
$form.add_FormClosing({
        param($s, $e)
        if ($script:dirty.Count -gt 0) {
            $answer = [System.Windows.Forms.MessageBox]::Show(
                "有 $($script:dirty.Count) 项修改还没生效（需要重启）。`n`n要现在保存并重启吗？",
                '未生效的修改', 'YesNoCancel', 'Question')

            if ($answer -eq 'Cancel') { $e.Cancel = $true; return }
            if ($answer -eq 'Yes') { $btnSave.PerformClick() }
        }
    })

# ── status refresh ──────────────────────────────────────────────────────────
function Update-Status {
    $proc = Get-Server

    if ($proc) {
        $lblState.Text = '运行中'
        $lblState.ForeColor = [System.Drawing.Color]::FromArgb(255, 0, 130, 90)
        $lblDetail.Text = "pid $($proc.Id)   ·   http://$($cfg.Addr):$($cfg.Port)"

        try {
            $s = Invoke-RestMethod "http://127.0.0.1:$($cfg.Port)/api/v1/settings"
            $inboxes = Invoke-RestMethod "http://127.0.0.1:$($cfg.Port)/api/v1/inboxes?limit=100"

            # Event totals only when one page covers every inbox: a partial sum would be
            # a made up number, and a wrong number here is worse than a dash.
            if ($inboxes.total -le 100) {
                $events = ($inboxes.items | Measure-Object -Property event_count -Sum).Sum
                $counts = "收件箱 $($inboxes.total) 个 · 事件 $events 条"
            }
            else {
                $counts = "收件箱 $($inboxes.total) 个 · 事件 —（过多，请打开界面查看）"
            }

            $lblEffective.Text = "超时 $($s.replay_timeout_ms)ms · 限流 $($s.replay_rate_limit)/min · 保留 $($s.retention_max_events) 条 · $counts"

            $lblEnc.Text = if ($s.encryption_enabled) { '敏感头加密：已启用（密钥不在此处显示，也不能在这里更换）' } else { '敏感头加密：未启用 —— 敏感头按掩码存储，不可恢复' }
            $lblHeaders.Text = "敏感头 $($s.sensitive_headers.Count) 个 · 访问控制：$(if ($s.auth_enabled) { '已启用' } else { '未启用' })"

            # @($null).Count is 1 in PowerShell, so an empty allow list used to look like
            # a widened configuration - a warning about a risk that was not there.
            $allowList = @(@($s.replay_allow_hosts) | Where-Object { $_ })
            $widened = ($allowList.Count -gt 0) -or $s.replay_allow_private

            if ($widened) {
                $lblReplay.Text = "⚠ 当前实例放行了内网重放目标（名单：$($allowList -join ', ')），这不是常规配置"
                $lblReplay.ForeColor = [System.Drawing.Color]::FromArgb(255, 200, 30, 30)
                $btnTighten.Enabled = $true
            }
            else {
                $lblReplay.Text = '内网重放：已禁止（默认，安全）'
                $lblReplay.ForeColor = [System.Drawing.Color]::FromArgb(255, 0, 130, 90)
                $btnTighten.Enabled = $false
            }

            if ($cfg.Addr -eq '0.0.0.0' -and -not $s.auth_enabled) {
                $lblDirty.Text = '⚠ 监听在 0.0.0.0 且未设置访问密钥：局域网内任何人都能打开这个界面。'
                $lblDirty.ForeColor = [System.Drawing.Color]::FromArgb(255, 200, 90, 0)
            }
        }
        catch {
            $lblEffective.Text = '（无法读取服务端设置）'
        }
    }
    else {
        $lblState.Text = '已停止'
        $lblState.ForeColor = [System.Drawing.Color]::FromArgb(255, 150, 150, 150)
        $lblDetail.Text = '用托盘图标启动，或点「保存并重启」'
        $lblEffective.Text = ''
        $lblEnc.Text = ''
        $lblReplay.Text = ''
        $lblHeaders.Text = ''
    }
}

# No timer: a background HTTP poll would make the numbers look like they change on their
# own. Refresh on open and on demand.
$form.add_Shown({ Update-Status; Update-Dirty })
[void]$form.ShowDialog()
