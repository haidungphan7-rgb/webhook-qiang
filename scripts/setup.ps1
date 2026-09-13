# Deployment launcher: takes a fresh machine from "nothing installed" to a running
# instance.
#
# It checks what is available, tells you exactly how to install what is missing, then
# prepares and starts the application. It does NOT ask which security switches to flip -
# those stay in the start command on purpose (see README "常见组合速查表").
#
# Usage:
#   pwsh ./scripts/setup.ps1                       # interactive
#   pwsh ./scripts/setup.ps1 -Mode Local           # local PostgreSQL + binary
#   pwsh ./scripts/setup.ps1 -Mode Docker          # docker compose
#   pwsh ./scripts/setup.ps1 -Mode Local -NoStart  # prepare only, do not run
#   pwsh ./scripts/setup.ps1 -Mode Local -Demo     # local replay allowed (this machine only)
#
# A double click on ./启动.bat (or ./start.bat) runs the interactive mode.

param(
    # 'Check' only reports what is installed and exits. It was missing from this set, so
    # the -Mode Check branch below could never run - passing it produced a parameter
    # binding error instead of the environment report.
    [ValidateSet('', 'Local', 'Docker', 'Check')][string]$Mode = '',
    [uint16]$Port = 8080,
    [switch]$NoStart,
    [switch]$Demo,
    [switch]$SkipFrontend,
    [string]$DbPassword = 'postgres'
)

$ErrorActionPreference = 'Continue'

# psql / createdb / go / npm output is UTF-8; the console code page is GBK on zh-CN,
# which turns every Chinese error message into mojibake.
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$dbName = 'webhook_rd'
$defaultDsn = "postgres://postgres:$DbPassword@127.0.0.1:5432/$dbName?sslmode=disable"

# NOTE: run this from a terminal or by double clicking 启动.bat. psql and createdb wait
# on stdin when there is no console, so a fully headless invocation (Start-Process with
# a hidden window) will hang at the database step. Use -NoStart in that case and start
# the binary yourself.

# Tools are frequently installed but not on PATH (psql especially: the PostgreSQL
# installer does not add its bin directory by default). Look in the usual places before
# declaring something missing.
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

$logDir = Join-Path $root 'logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$logFile = Join-Path $logDir 'setup.log'

# Every step is echoed to logs/setup.log as well: when the launcher is started by a
# double click the window may close before anyone reads it.
function Write-Step($text) {
    Write-Host "`n=== $text ===" -ForegroundColor Cyan
    Add-Content -Path $script:logFile -Value ("[{0}] {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $text)
}
function Write-Need($what, $how) {
    Write-Host "  [缺失] $what" -ForegroundColor Yellow
    Write-Host "         安装：$how" -ForegroundColor DarkGray
}

# ── 1. what is installed ────────────────────────────────────────────────────
Write-Step '1/5 检查环境'

$tool = @{
    Go     = Find-Tool 'go' @('C:\Program Files\Go\bin\go.exe')
    Node   = Find-Tool 'node' @()
    Psql   = Find-Tool 'psql' @('C:\Program Files\PostgreSQL\*\bin\psql.exe')
    # pg_dump is a separate executable from psql and is what "备份数据库" uses. Checking
    # psql alone makes the backup button fail later with a message nobody expects, so it
    # gets probed (and reported) here.
    PgDump = Find-Tool 'pg_dump' @('C:\Program Files\PostgreSQL\*\bin\pg_dump.exe')
    Docker = Find-Tool 'docker' @()
}

# createdb and pg_dump live next to psql, so putting that directory on PATH makes all of
# them available.
if ($tool.Psql) { $env:PATH += ';' + (Split-Path $tool.Psql) }
elseif ($tool.PgDump) { $env:PATH += ';' + (Split-Path $tool.PgDump) }

$has = @{
    Go     = [bool]$tool.Go
    Node   = [bool]$tool.Node
    Psql   = [bool]$tool.Psql
    PgDump = [bool]$tool.PgDump
    Docker = [bool]$tool.Docker
}

Write-Host ("  go      : " + $(if ($has.Go) { 'ok' } else { '未安装' }))
Write-Host ("  node    : " + $(if ($has.Node) { 'ok' } else { '未安装' }))
Write-Host ("  psql    : " + $(if ($has.Psql) { 'ok' } else { '未安装' }))
Write-Host ("  pg_dump : " + $(if ($has.PgDump) { 'ok' } else { '未安装（备份功能不可用）' }))
Write-Host ("  docker  : " + $(if ($has.Docker) { 'ok' } else { '未安装' }))

if (-not $Mode) {
    # Probe docker with a timeout: "docker info" against a stopped Docker Desktop can
    # hang for a long time, which turns "slow" into "frozen".
    $dockerOk = $false
    if ($has.Docker) {
        $job = Start-Job -ScriptBlock { docker info | Out-Null }
        $null = Wait-Job $job -Timeout 15
        if ($job.State -eq 'Completed') { $dockerOk = $true }
        Remove-Job $job -Force -ErrorAction SilentlyContinue
    }

    $defaultPick = if ($dockerOk) { '1' } else { '2' }

    Write-Host ''
    Write-Host '  这台机器怎么跑？' -ForegroundColor White
    if ($dockerOk) {
        Write-Host '  1) Docker（推荐，约 1 分钟，不用本机装 PostgreSQL）' -ForegroundColor Green
        Write-Host '  2) 本机（需要已装 PostgreSQL，会帮你建库、迁移、构建前端）'
    }
    else {
        Write-Host '  1) Docker（未检测到运行中的 Docker）' -ForegroundColor DarkGray
        Write-Host '  2) 本机（需要已装 PostgreSQL，会帮你建库、迁移、构建前端）' -ForegroundColor Green
    }
    Write-Host '  3) 只检查环境，不部署'

    $pick = Read-Host "  选择 [1/2/3]（直接回车 = $defaultPick）"
    if ([string]::IsNullOrWhiteSpace($pick)) { $pick = $defaultPick }

    $Mode = switch ($pick) {
        '1' { if ($dockerOk) { 'Docker' } else { 'Local' } }
        '3' { 'Check' }
        default { 'Local' }
    }
}

if ($Mode -eq 'Check') {
    Write-Host ''
    Write-Host 'PostgreSQL（本机模式必需）：' -NoNewline
    if ($has.Psql) { Write-Host '已安装' -ForegroundColor Green } else {
        Write-Host '未安装' -ForegroundColor Yellow
        Write-Need 'PostgreSQL 16' 'winget install -e --id PostgreSQL.PostgreSQL.16'
    }
    Write-Host 'pg_dump（仅「备份数据库」按钮需要，随 PostgreSQL 一起装）：' -NoNewline
    if ($has.PgDump) { Write-Host '已安装' -ForegroundColor Green }
    else {
        Write-Host '未找到' -ForegroundColor Yellow
        Write-Host '  已自动查找 C:\Program Files\PostgreSQL\*\bin 也没有找到。' -ForegroundColor DarkGray
        Write-Need 'PostgreSQL 16' 'winget install -e --id PostgreSQL.PostgreSQL.16'
        Write-Host '  或者把已有 PostgreSQL 的 bin 目录加入 PATH（备份按钮会自动探测）。' -ForegroundColor DarkGray
    }

    Write-Host 'Go（仅构建后端需要，也可用预编译二进制）：' -NoNewline
    if ($has.Go) { Write-Host '已安装' -ForegroundColor Green } else { Write-Need 'Go' 'winget install -e --id GoLang.Go' }
    Write-Host 'Node（仅构建前端需要）：' -NoNewline
    if ($has.Node) { Write-Host '已安装' -ForegroundColor Green } else { Write-Need 'Node.js' 'winget install -e --id OpenJS.NodeJS.LTS' }

    exit 0
}

# ── 2. Docker path ──────────────────────────────────────────────────────────
if ($Mode -eq 'Docker') {
    Write-Step '2/5 Docker 部署'

    if (-not $has.Docker) {
        Write-Need 'Docker Desktop' 'winget install -e --id Docker.DockerDesktop'
        Write-Host '  装完需要重启一次（WSL 后端要初始化），然后重跑这个脚本。' -ForegroundColor Yellow
        exit 1
    }

    if (-not $Demo) {
        Write-Host '  注意：compose 里默认不放行本地重放，演示第 4 步会返回 403（这是防护在生效）。' -ForegroundColor DarkGray
        Write-Host '        要演示请编辑 docker-compose.yml 取消 REPLAY_ALLOW_* 两行注释。' -ForegroundColor DarkGray
    }

    if ($NoStart) { Write-Host '  -NoStart：跳过启动'; exit 0 }

    Write-Host '  启动中（首次会拉镜像，慢一点）…' -ForegroundColor DarkGray
    & docker compose up --build
    exit $LASTEXITCODE
}

# ── 3. local path: dependencies ─────────────────────────────────────────────
Write-Step '2/5 本机依赖'

if (-not $has.Psql) {
    Write-Need 'PostgreSQL 16' 'winget install -e --id PostgreSQL.PostgreSQL.16'
    Write-Host '  也可以改用 Docker 模式：pwsh ./scripts/setup.ps1 -Mode Docker' -ForegroundColor Yellow
    exit 1
}

if (-not $env:DATABASE_URL) { $env:DATABASE_URL = $defaultDsn }
if (-not $env:PGPASSWORD) { $env:PGPASSWORD = $DbPassword }

# ── 4. database ─────────────────────────────────────────────────────────────
Write-Step '3/5 数据库'

# The options come before "-d": psql treats a leading conninfo as the whole connection
# and silently ignores later arguments.
$exists = (& psql -U postgres -d postgres -tA -c "select 1 from pg_database where datname='$dbName'" 2>$null) -join ''

if ($exists.Trim() -eq '1') {
    Write-Host "  数据库 $dbName 已存在" -ForegroundColor Green
}
else {
    Write-Host "  创建数据库 $dbName …" -ForegroundColor DarkGray

    # Keep the error: swallowing it here is how "wrong password" used to look like
    # "the script just stopped doing anything".
    $err = & createdb -U postgres $dbName 2>&1

    $again = (& psql -U postgres -d postgres -tA -c "select 1 from pg_database where datname='$dbName'" 2>$null) -join ''

    if ($again.Trim() -ne '1') {
        Write-Host "  建库失败：$err" -ForegroundColor Red
        Write-Host "  通常是 postgres 用户的密码不是脚本里用的那个。请重跑并显式指定：" -ForegroundColor Yellow
        Write-Host "    pwsh ./scripts/setup.ps1 -Mode Local -DbPassword <你的密码>" -ForegroundColor Yellow
        Write-Host "  或手工执行：createdb -U postgres $dbName" -ForegroundColor DarkGray
        exit 1
    }
    Write-Host '  已创建' -ForegroundColor Green
}

# ── 5. build ────────────────────────────────────────────────────────────────
Write-Step '4/5 构建'

$bin = Join-Path $root 'bin'
New-Item -ItemType Directory -Force -Path $bin | Out-Null
$exe = Join-Path $bin 'webhook-zq.exe'

if (Test-Path $exe) {
    Write-Host "  已有二进制：$exe" -ForegroundColor Green
}
elseif ($has.Go) {
    Write-Host '  构建后端…' -ForegroundColor DarkGray
    $env:CGO_ENABLED = '0'

    # Stamp the version in so the running instance (and the tray / manager window) can
    # answer "which build is this?". git may be missing - fall back to a dated dev tag.
    $ver = 'dev'
    if ($has.Git) {
        $tag = (& $tool.Git describe --tags --always --dirty 2>$null)
        if ($LASTEXITCODE -eq 0 -and $tag) { $ver = $tag.Trim() }
    }

    $built = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    $pkg = 'github.com/yuandzhang/webhook-zq/internal/version'
    $ldflags = "-s -w -X '$pkg.version=$ver' -X '$pkg.buildTime=$built'"

    & $tool.Go build -trimpath -ldflags $ldflags -o $exe ./cmd/webhook-tester
    if ($LASTEXITCODE -ne 0) { Write-Host '  构建失败' -ForegroundColor Red; exit 1 }
    Write-Host '  后端构建完成' -ForegroundColor Green
}
else {
    Write-Need 'Go' 'winget install -e --id GoLang.Go'
    Write-Host '  或者从别的机器复制 webhook-zq.exe 到 bin\ 目录' -ForegroundColor Yellow
    exit 1
}

$distIndex = Join-Path $root 'web/dist/index.html'
$needFrontend = $true

if ((Test-Path $distIndex) -and ((Get-Content $distIndex -Raw) -notmatch '前端尚未构建')) {
    $needFrontend = $false
}

if ($needFrontend -and -not $SkipFrontend) {
    if (-not $has.Node) {
        Write-Need 'Node.js' 'winget install -e --id OpenJS.NodeJS.LTS'
        Write-Host '  没有 Node 就看不到界面（API 仍可用），或者改用 Docker 模式。' -ForegroundColor Yellow
    }
    else {
        Write-Host '  构建前端（约 20 秒）…' -ForegroundColor DarkGray
        if (-not (Test-Path (Join-Path $root 'web/node_modules'))) {
            & npm --prefix ./web ci --no-audit --no-fund
        }
        & npm --prefix ./web run build
        if ($LASTEXITCODE -ne 0) { Write-Host '  前端构建失败' -ForegroundColor Red; exit 1 }
        Write-Host '  前端构建完成' -ForegroundColor Green
    }
}

# ── 6. start ────────────────────────────────────────────────────────────────
Write-Step '5/5 启动'

$args = @('start', '--port', $Port)

if ($Demo) {
    Write-Host '  本机模式：放行 127.0.0.1，可以把事件重放到你本机上的服务' -ForegroundColor Yellow
    $args += @('--replay-allow-host', '127.0.0.1', '--replay-allow-private', '--replay-timeout', '3s')
}

Write-Host ''
Write-Host "  地址       http://127.0.0.1:$Port" -ForegroundColor Green
Write-Host '  建一个收件箱：' -ForegroundColor DarkGray
Write-Host ('    curl -XPOST http://127.0.0.1:' + $Port + '/api/v1/inboxes -H ''content-type: application/json'' -d ''{"name":"demo"}''') -ForegroundColor DarkGray
Write-Host '  完整演示：' -ForegroundColor DarkGray
Write-Host '    make accept' -ForegroundColor DarkGray
if ($Demo) {
    Write-Host '  本地接收端：' -ForegroundColor DarkGray
    Write-Host '    node scripts/test-receiver.mjs 9099' -ForegroundColor DarkGray
}
Write-Host ''

if ($NoStart) { Write-Host '  -NoStart：跳过启动' -ForegroundColor DarkGray; exit 0 }

& $exe @args
