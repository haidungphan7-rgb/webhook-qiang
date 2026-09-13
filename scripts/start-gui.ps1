# Graphical launcher.
#
# setup.ps1 already knows how to check the environment, create the database, build the
# frontend and start the server. This window does NOT re-implement any of that: it runs
# setup.ps1 and shows its output, so there is exactly one copy of "how to start".
#
#   pwsh ./scripts/start-gui.ps1
#   pwsh ./scripts/start-gui.ps1 -ProbeOnly   # print the state and exit (no window)

param(
    [switch]$ProbeOnly
)

$ErrorActionPreference = 'Continue'

# go / npm / psql output is UTF-8; the console code page is GBK on zh-CN Windows.
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# "D:\webhook-zq-evil\wh.exe" starts with "D:\webhook-zq" as well, so the separator is
# required - otherwise stopping the server would kill a neighbouring project's process.
$rootPrefix = $root.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) +
[IO.Path]::DirectorySeparatorChar

# One definition of the defaults, of the launcher file format, of "is the server up" and
# of "is this tool installed".
. (Join-Path $PSScriptRoot 'lib\background.ps1')
. (Join-Path $PSScriptRoot 'lib\process.ps1')

$settingsFile = Join-Path $root 'webhook-zq.json'

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()

# ── state ───────────────────────────────────────────────────────────────────

function Find-Tool([string]$name, [string[]]$candidates) {
    $found = Get-Command $name -ErrorAction SilentlyContinue
    if ($found) { return $found.Source }

    foreach ($pattern in $candidates) {
        $hit = Get-ChildItem -Path $pattern -ErrorAction SilentlyContinue |
            Sort-Object FullName -Descending | Select-Object -First 1
        if ($hit) { return $hit.FullName }
    }

    return $null
}

function Get-ServerPid([uint16]$port) {
    $conn = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if (-not $conn) { return $null }

    return $conn.OwningProcess
}

function Test-Healthy([uint16]$port) {
    try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$port/healthz" -UseBasicParsing -TimeoutSec 2
        return $r.StatusCode -eq 200
    }
    catch {
        return $false
    }
}

function Get-AppState {
    param([uint16]$Port)

    # One source of truth for "what is running": the binary itself. This window only
    # renders the answer - which is the point of `webhook-zq status --json`, since the
    # tray, the manager and this launcher used to each derive it differently and then
    # disagree. The local probe below is only the fallback for a machine that has not
    # been built yet.
    $exeProbe = Join-Path $root 'bin/webhook-zq.exe'
    if (-not (Test-Path $exeProbe)) { $exeProbe = Join-Path $root 'webhook-zq.exe' }

    if (Test-Path $exeProbe) {
        try {
            $raw = & $exeProbe status --port $Port --json 2>$null | Out-String

            if ($raw.Trim().StartsWith('{')) {
                $s = $raw | ConvertFrom-Json

                return [pscustomobject]@{
                    Port     = if ($s.port) { [uint16]$s.port } else { $Port }
                    Running  = [bool]$s.running
                    Pid      = $s.pid
                    Healthy  = [bool]$s.healthy
                    Go       = [bool](Find-Tool 'go' @('C:\Program Files\Go\bin\go.exe'))
                    Node     = [bool](Find-Tool 'node' @())
                    Psql     = [bool](Find-Tool 'psql' @('C:\Program Files\PostgreSQL\*\bin\psql.exe'))
                    Exe      = if ($s.exe_path) { $s.exe_path } else { Split-Path -Leaf $exeProbe }
                    Frontend = [bool]$s.frontend_embedded
                    Source   = 'status --json'
                }
            }
        }
        catch { }
    }

    $pid1 = Get-ServerPid $Port

    return [pscustomobject]@{
        Port       = $Port
        Running    = [bool]$pid1
        Pid        = $pid1
        Healthy    = if ($pid1) { Test-Healthy $Port } else { $false }
        Go         = [bool](Find-Tool 'go' @('C:\Program Files\Go\bin\go.exe'))
        Node       = [bool](Find-Tool 'node' @())
        Psql       = [bool](Find-Tool 'psql' @('C:\Program Files\PostgreSQL\*\bin\psql.exe'))
        Exe        = if (Test-Path (Join-Path $root 'bin/webhook-zq.exe')) { 'bin\webhook-zq.exe' }
        elseif (Test-Path (Join-Path $root 'webhook-zq.exe')) { 'webhook-zq.exe' } else { $null }
        Frontend   = Test-Path (Join-Path $root 'web/dist/index.html')
    }
}

if ($ProbeOnly) {
    $s = Get-AppState -Port 8080

    Write-Host ''
    Write-Host "服务   : $(if ($s.Running) { "运行中 pid $($s.Pid)，健康检查 $(if ($s.Healthy) { '通过' } else { '失败' })" } else { '未运行' })"
    Write-Host "后端   : $(if ($s.Exe) { $s.Exe } else { '未构建' })"
    Write-Host "前端   : $(if ($s.Frontend) { '已构建' } else { '未构建' })"
    Write-Host "依赖   : go=$(if ($s.Go) { 'ok' } else { '缺' }) node=$(if ($s.Node) { 'ok' } else { '缺' }) psql=$(if ($s.Psql) { 'ok' } else { '缺' })"
    Write-Host ''

    exit 0
}

# ── the window ──────────────────────────────────────────────────────────────

$form = New-Object System.Windows.Forms.Form
$form.Text = 'webhook 重放平台'
$form.Size = New-Object System.Drawing.Size(620, 560)
$form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false

$font = New-Object System.Drawing.Font('Segoe UI', 9)

# status block
$grpState = New-Object System.Windows.Forms.GroupBox
$grpState.Text = '当前状态'
$grpState.Location = New-Object System.Drawing.Point(16, 12)
$grpState.Size = New-Object System.Drawing.Size(572, 118)
$form.Controls.Add($grpState)

$statusLabels = @{}

$rows = @(
    @{ Key = 'server'; Text = '服务' },
    @{ Key = 'exe'; Text = '后端' },
    @{ Key = 'frontend'; Text = '前端' },
    @{ Key = 'deps'; Text = '依赖' }
)

$y = 26
foreach ($r in $rows) {
    $name = New-Object System.Windows.Forms.Label
    $name.Text = $r.Text
    $name.Location = New-Object System.Drawing.Point(16, $y)
    $name.Size = New-Object System.Drawing.Size(50, 20)
    $grpState.Controls.Add($name)

    $value = New-Object System.Windows.Forms.Label
    $value.Text = '…'
    $value.Location = New-Object System.Drawing.Point(74, $y)
    $value.Size = New-Object System.Drawing.Size(490, 20)
    $value.Font = $font
    $grpState.Controls.Add($value)

    $statusLabels[$r.Key] = $value
    $y += 24
}

# options
$lblPort = New-Object System.Windows.Forms.Label
$lblPort.Text = '端口'
$lblPort.Location = New-Object System.Drawing.Point(20, 146)
$lblPort.Size = New-Object System.Drawing.Size(40, 20)
$form.Controls.Add($lblPort)

$txtPort = New-Object System.Windows.Forms.TextBox
# Read the configured port instead of hard coding 8080: the settings window can change it,
# and a launcher that ignores it would start a second, differently configured instance.
$txtPort.Text = [string](Read-AppConfig -Path $settingsFile).Port
$txtPort.Location = New-Object System.Drawing.Point(64, 143)
$txtPort.Size = New-Object System.Drawing.Size(70, 24)
$form.Controls.Add($txtPort)

$chkDemo = New-Object System.Windows.Forms.CheckBox
# Not "演示模式": replaying into the service you are developing on localhost is an
# everyday need, not a presentation trick. Say what it actually does.
$chkDemo.Text = '本机模式（可以把事件重放到你本机上的服务）'
$chkDemo.Location = New-Object System.Drawing.Point(150, 144)
$chkDemo.Size = New-Object System.Drawing.Size(300, 22)
# Off by default. This checkbox opens the local replay gate (--replay-allow-private),
# which is the only SSRF switch in the system - it must never be on unless someone
# deliberately asks for a demo.
$chkDemo.Checked = $false
$form.Controls.Add($chkDemo)

# buttons
function New-AppButton($text, $x, $y, $w, $handler) {
    $b = New-Object System.Windows.Forms.Button
    $b.Text = $text
    $b.Location = New-Object System.Drawing.Point($x, $y)
    $b.Size = New-Object System.Drawing.Size($w, 32)
    $b.Add_Click($handler)

    return $b
}

$btnStart = New-AppButton '启动' 16 180 110 {
    Start-App
}
$form.Controls.Add($btnStart)

$btnStop = New-AppButton '停止' 134 180 110 {
    Stop-App
}
$form.Controls.Add($btnStop)

$btnBrowser = New-AppButton '打开网页' 252 180 110 {
    Start-Process "http://127.0.0.1:$(Get-Port)"
}
$form.Controls.Add($btnBrowser)

$btnScan = New-AppButton '重新检查' 370 180 110 {
    Update-State
}
$form.Controls.Add($btnScan)

$btnDoctor = New-AppButton '运行自检' 16 220 110 {
    Run-Doctor
}
$form.Controls.Add($btnDoctor)

$btnBuild = New-AppButton '构建前端' 134 220 110 {
    Run-Build
}
$form.Controls.Add($btnBuild)

$btnManager = New-AppButton '高级设置' 252 220 110 {
    $p = Join-Path $root 'scripts/server-manager.ps1'
    if (Test-Path $p) { Start-Process pwsh -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $p }
}
$form.Controls.Add($btnManager)

$btnUninstall = New-AppButton '卸载' 370 220 110 {
    $p = Join-Path $root 'scripts/uninstaller.ps1'
    if (Test-Path $p) { Start-Process pwsh -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $p }
}
$form.Controls.Add($btnUninstall)

# Not "演示": the always-on instance refuses to replay to private addresses, and that is
# correct - but it also means you cannot replay into the service running on your own
# machine. This opens a second, throwaway instance that does, which is an everyday need,
# not a presentation trick.
# Deliberately NOT the brand blue filled button. This opens a security exemption
# (--replay-allow-private), so it must never be the most prominent thing on the window -
# "启动" is. The name says exactly what it relaxes rather than "调试", which is too vague.
$btnDemo = New-AppButton '本机重放实例' 16 258 228 {
    Start-LocalReplay
}
$form.Controls.Add($btnDemo)

$btnStopDemo = New-AppButton '停止本机重放实例' 252 258 228 {
    Stop-LocalReplay
}
$form.Controls.Add($btnStopDemo)

# log
$log = New-Object System.Windows.Forms.TextBox
$log.Location = New-Object System.Drawing.Point(16, 300)
$log.Size = New-Object System.Drawing.Size(572, 190)
$log.Multiline = $true
$log.ScrollBars = 'Vertical'
$log.ReadOnly = $true
$log.WordWrap = $false
$log.Font = New-Object System.Drawing.Font('Consolas', 9)
$log.BackColor = [System.Drawing.Color]::FromArgb(250, 250, 250)
$form.Controls.Add($log)

function Write-Log($text) {
    $log.AppendText("$(Get-Date -Format 'HH:mm:ss')  $text`r`n")
    $log.ScrollToCaret()
}

function Get-Port {
    $p = 0
    if ([uint16]::TryParse($txtPort.Text.Trim(), [ref]$p)) { return $p }

    return 8080
}

function Update-State {
    $s = Get-AppState -Port (Get-Port)

    if ($s.Running) {
        $statusLabels.server.Text = if ($s.Healthy) { "运行中（端口 $($s.Port)，健康检查通过）" } else { "端口被占用，但服务没响应（pid $($s.Pid)）" }
        $statusLabels.server.ForeColor = if ($s.Healthy) { [System.Drawing.Color]::FromArgb(0, 130, 60) } else { [System.Drawing.Color]::FromArgb(190, 120, 0) }
    }
    else {
        $statusLabels.server.Text = '未运行'
        $statusLabels.server.ForeColor = [System.Drawing.Color]::Gray
    }

    $statusLabels.exe.Text = if ($s.Exe) { "已构建（$($s.Exe)）" } else { '未构建，启动时会自动构建（需要 Go）' }
    $statusLabels.exe.ForeColor = if ($s.Exe) { [System.Drawing.Color]::Black } else { [System.Drawing.Color]::FromArgb(190, 120, 0) }

    $statusLabels.frontend.Text = if ($s.Frontend) { '已构建' } else { '未构建，启动时会自动构建（需要 Node）' }
    $statusLabels.frontend.ForeColor = if ($s.Frontend) { [System.Drawing.Color]::Black } else { [System.Drawing.Color]::FromArgb(190, 120, 0) }

    $missing = @()
    if (-not $s.Go -and -not $s.Exe) { $missing += 'Go' }
    if (-not $s.Node) { $missing += 'Node' }
    if (-not $s.Psql) { $missing += 'PostgreSQL' }

    if ($missing.Count -eq 0) {
        $statusLabels.deps.Text = '齐全（Go / Node / PostgreSQL）'
        $statusLabels.deps.ForeColor = [System.Drawing.Color]::FromArgb(0, 130, 60)
    }
    else {
        $statusLabels.deps.Text = "缺少：$($missing -join '、')"
        $statusLabels.deps.ForeColor = [System.Drawing.Color]::FromArgb(190, 40, 40)
    }

    $btnStart.Enabled = -not $s.Running
    $btnStop.Enabled = $s.Running
}

function Stop-App {
    $port = Get-Port
    $target = Get-ServerPid $port

    if (-not $target) {
        Write-Log "端口 $port 上没有运行中的服务"

        return
    }

    # Only stop what we can prove is ours: a two letter name like "wh" is far too
    # generic to kill on trust.
    $exePath = $null
    try { $exePath = (Get-Process -Id $target -ErrorAction SilentlyContinue).Path } catch { }

    if (-not $exePath -or -not $exePath.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        Write-Log "端口 $port 上的进程不属于本项目（$exePath），已跳过"

        return
    }

    Stop-Process -Id $target -Force -ErrorAction SilentlyContinue
    Write-Log "已停止服务（pid $target）"
    Start-Sleep -Milliseconds 600
    Update-State
}

function Start-App {
    $port = Get-Port
    $setup = Join-Path $root 'scripts/setup.ps1'

    if (-not (Test-Path $setup)) {
        Write-Log "找不到 scripts/setup.ps1，无法启动"

        return
    }

    $args = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $setup, '-Mode', 'Local', '-Port', $port)

    if ($chkDemo.Checked) {
        $args += '-Demo'
        Write-Log '本机模式：已放行 127.0.0.1 等本机地址（带这个开关的实例不要暴露到公网）'
    }

    $outFile = Join-Path $root 'logs/start-gui.out.log'
    $errFile = Join-Path $root 'logs/start-gui.err.log'
    New-Item -ItemType Directory -Force -Path (Split-Path $outFile) | Out-Null
    Remove-Item $outFile, $errFile -Force -ErrorAction SilentlyContinue

    Write-Log "正在启动（端口 $port）… 这一步会检查环境、建库、构建前端，请稍候"

    $btnStart.Enabled = $false
    $form.Refresh()

    Start-Process pwsh -ArgumentList $args -WindowStyle Hidden `
        -RedirectStandardOutput $outFile -RedirectStandardError $errFile

    # setup.ps1 blocks while the server runs, so "done" is decided by the health
    # endpoint rather than by the process exiting.
    $deadline = (Get-Date).AddMinutes(5)
    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 800
        Update-LogFromFile $outFile $errFile

        if (Test-Healthy $port) {
            Write-Log "服务已就绪：http://127.0.0.1:$port"
            Update-State
            Start-Process "http://127.0.0.1:$port"

            return
        }

        [System.Windows.Forms.Application]::DoEvents()
    }

    Write-Log '启动超时（5 分钟）。上面的输出里有原因，常见问题：PostgreSQL 没启动、密码不是 postgres、端口被占用。'
    $btnStart.Enabled = $true
}

$script:logOffset = @{}

function Update-LogFromFile($outFile, $errFile) {
    foreach ($f in $outFile, $errFile) {
        if (-not (Test-Path $f)) { continue }

        $len = (Get-Item $f).Length
        $off = if ($script:logOffset.ContainsKey($f)) { $script:logOffset[$f] } else { 0 }

        if ($len -le $off) { continue }

        $stream = [System.IO.File]::Open($f, 'Open', 'Read', 'ReadWrite')
        try {
            $null = $stream.Seek($off, 'Begin')
            $reader = New-Object System.IO.StreamReader($stream)
            $chunk = $reader.ReadToEnd()
            $script:logOffset[$f] = $stream.Position
        }
        finally {
            $stream.Close()
        }

        foreach ($line in ($chunk -split "`r?`n")) {
            if ($line.Trim()) { Write-Log $line }
        }
    }
}

$script:localReplay = $null

function Start-LocalReplay {
    if ($script:localReplay) {
        Write-Log "本机重放实例已经在运行：http://127.0.0.1:$($script:localReplay.Port)"

        return
    }

    $port = 8081
    while (Get-ServerPid $port) { $port++ }

    $exe = Join-Path $root 'bin/webhook-zq.exe'
    if (-not (Test-Path $exe)) { $exe = Join-Path $root 'webhook-zq.exe' }

    if (-not (Test-Path $exe)) {
        Write-Log '还没有后端二进制，请先点「启动」构建一次'

        return
    }

    if (-not $env:DATABASE_URL) {
        $env:DATABASE_URL = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
    }

    # A local receiver to replay into. Without one there is nothing to demonstrate
    # "replayed successfully" against.
    $receiver = Join-Path $root 'scripts/test-receiver.mjs'
    if (Test-Path $receiver) {
        Start-Process node -ArgumentList $receiver, '9099' -WorkingDirectory $root -WindowStyle Hidden `
            -RedirectStandardOutput (Join-Path $root 'logs/local-receiver.log') `
            -RedirectStandardError (Join-Path $root 'logs/local-receiver.err.log')
        Write-Log '已启动本地接收端 :9099'
    }

    # The demo instance is deliberately separate: it opens the local replay gate that the
    # always-on instance keeps shut. It is a different port, so closing it loses nothing.
    $outLog = Join-Path $root 'logs/local-replay.log'
    Start-Process $exe -ArgumentList 'start', '--port', $port, '--replay-allow-host', '127.0.0.1',
    '--replay-allow-private', '--replay-timeout', '3s' -WindowStyle Hidden `
        -RedirectStandardOutput $outLog -RedirectStandardError (Join-Path $root 'logs/local-replay.err.log')

    Write-Log "正在启动本机重放实例 :$port …"

    $ready = $false
    for ($i = 0; $i -lt 40; $i++) {
        Start-Sleep -Milliseconds 700
        if (Test-Healthy $port) { $ready = $true; break }
        [System.Windows.Forms.Application]::DoEvents()
    }

    if (-not $ready) {
        Write-Log "本机重放实例没起来，看 logs/local-replay.err.log（多半是数据库没连上）"

        return
    }

    $script:localReplay = [pscustomobject]@{ Port = $port }

    # Deliberately does NOT create an inbox. An earlier version created one called
    # "演示" on every run, and a few debugging sessions later the list was full of them -
    # a tool that litters the user's own data is not helping. The always-on instance is
    # untouched, so their existing inboxes and events are all there.
    Write-Log "本机重放实例已就绪：http://127.0.0.1:$port"
    Write-Log '它比常驻实例多一件事：放行了 127.0.0.1 等本机地址，可以把事件重放到你正在开发的服务。'
    Write-Log '与常驻实例共用同一个数据库，所以你的收件箱和事件都在；停止它不会丢数据。'

    Start-Process "http://127.0.0.1:$port"

    Update-State
}

function Stop-LocalReplay {
    if (-not $script:localReplay) {
        Write-Log '本机重放实例没有在运行'

        return
    }

    $port = $script:localReplay.Port
    $target = Get-ServerPid $port

    if ($target) {
        Stop-Process -Id $target -Force -ErrorAction SilentlyContinue
        Write-Log "已停止本机重放实例 :$port"
    }

    # The receiver is a node process on 9099; stop only what listens there.
    $rx = Get-NetTCPConnection -LocalPort 9099 -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($rx) {
        Stop-Process -Id $rx.OwningProcess -Force -ErrorAction SilentlyContinue
        Write-Log '已停止本地接收端 :9099'
    }

    # Say it explicitly: the exemption was tied to this process, so it is gone with it.
    # Otherwise the user may assume the instance still replays to private addresses.
    Write-Log '本机重放实例已停止，放行策略随之失效（常驻实例不受影响，仍然拒绝内网地址）。'

    $script:localReplay = $null
    Update-State
}

function Run-Doctor {
    $exe = Join-Path $root 'bin/webhook-zq.exe'
    if (-not (Test-Path $exe)) { $exe = Join-Path $root 'webhook-zq.exe' }

    if (-not (Test-Path $exe)) {
        Write-Log '还没有后端二进制，先点「启动」构建一次'

        return
    }

    Write-Log '运行自检…'
    $out = & $exe doctor 2>&1 | Out-String
    Write-Log $out.Trim()
}

function Run-Build {
    $node = Find-Tool 'node' @()

    if (-not $node) {
        Write-Log '没有找到 Node.js，无法构建前端（winget install -e --id OpenJS.NodeJS.LTS）'

        return
    }

    Write-Log '正在构建前端…'
    $out = & npm --prefix (Join-Path $root 'web') run build 2>&1 | Out-String
    $last = ($out -split "`r?`n" | Where-Object { $_.Trim() } | Select-Object -Last 3) -join ' / '
    Write-Log $last
    Update-State
}

Update-State
[void]$form.ShowDialog()
