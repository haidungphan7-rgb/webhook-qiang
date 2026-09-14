# b4-install-uninstall.ps1 - acceptance for `webhook-zq install` / `webhook-zq uninstall`
#
# Sandboxing strategy (protects real machine state):
#   - LOCALAPPDATA is redirected to %TEMP%\wzq-accept-sbox for every child process,
#     so AppDir = sbox\webhook-zq. The real Roaming config and the D:\webhook-zq
#     main instance (port 8080) are never touched.
#   - The sbox deliberately sits OUTSIDE the uninstaller's own directory: the Go
#     uninstaller also kills webhook-zq.exe processes living next to itself
#     (ourDir fallback), so a trap dir inside it would be caught by that rule.
#   - The HKCU registry entry, scheduled task and service use the real names; they
#     are created by install and must be gone at the end (final sweep asserts it).
#   - Database tests use a throwaway DB (wzq_accept_test) on the local PostgreSQL.
#   - All stdin cases go through `cmd /c ... < file` (deterministic EOF via nul);
#     PowerShell piping has edge cases (empty array may inherit the console).
#
# The uninstaller is additionally copied to wzq-uninstaller.exe: a differently-named
# binary mirrors the intended download flow (webhook-zq-windows-amd64.exe), where
# the process-name filter does not match the uninstaller itself.

try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }
$ErrorActionPreference = 'Stop'

$Wk      = "$env:TEMP\wzq-accept"
$Build   = "$Wk\webhook-zq-accept.exe"
$Uninst  = "$Wk\wzq-uninstaller.exe"
$SBox    = "$env:TEMP\wzq-accept-sbox"
$AppDir  = "$SBox\webhook-zq"
$InstExe = "$AppDir\bin\webhook-zq.exe"
$RunDir  = "$Wk\rundir"
$DbName  = 'wzq_accept_test'
$Dsn     = "postgres://postgres:postgres@127.0.0.1:5432/$DbName`?sslmode=disable"
$RegKey  = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\webhook-zq'
# The schtasks logon task inherits the REAL user environment (not this script's
# sandbox redirect), so its `start` reads the real config file below, which the
# I7 case creates temporarily and removes afterwards.
$RealCfg = "$env:LOCALAPPDATA\webhook-zq\config.json"

$script:Pass = 0
$script:Fail = 0
$script:Findings = [System.Collections.Generic.List[string]]::new()

function Check([string]$Name, [bool]$Cond) {
    if ($Cond) { $script:Pass++; Write-Host "PASS $Name" }
    else { $script:Fail++; Write-Host "FAIL $Name" -ForegroundColor Red }
}

function Info([string]$Text) {
    Write-Host "INFO $Text" -ForegroundColor DarkCyan
    $script:Findings.Add($Text)
}

function Idx([string]$Text, [string]$Needle) {
    return $Text.IndexOf($Needle, [System.StringComparison]::OrdinalIgnoreCase)
}

# ── env sandbox helpers ───────────────────────────────────────────────────────
$script:SavedLocal = $null
$script:SavedDb    = $null

function Use-SandboxEnv([string]$DbUrl) {
    $script:SavedLocal = $env:LOCALAPPDATA
    $script:SavedDb    = $env:DATABASE_URL
    $env:LOCALAPPDATA  = $SBox
    if ($DbUrl) { $env:DATABASE_URL = $DbUrl }
    else { Remove-Item Env:DATABASE_URL -ErrorAction SilentlyContinue }
}

function Restore-Env {
    $env:LOCALAPPDATA = $script:SavedLocal
    if ($script:SavedDb) { $env:DATABASE_URL = $script:SavedDb }
    else { Remove-Item Env:DATABASE_URL -ErrorAction SilentlyContinue }
}

function Invoke-Zq {
    # Runs the exe under the sandbox env. $StdinFile feeds stdin via cmd
    # redirection; the literal 'nul' gives a deterministic EOF.
    param([string]$Exe, [string[]]$Arguments, [string]$StdinFile, [string]$DbUrl)
    Use-SandboxEnv $DbUrl
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'   # exe stderr is captured, not fatal
    try {
        New-Item -ItemType Directory -Force -Path $RunDir | Out-Null
        Push-Location $RunDir
        try {
            if ($StdinFile) {
                $out = & cmd.exe /c "$Exe $($Arguments -join ' ') < $StdinFile" 2>&1
            }
            else {
                $out = & $Exe @Arguments 2>&1
            }
            $code = $LASTEXITCODE
        }
        finally { Pop-Location }
    }
    finally {
        $ErrorActionPreference = $prev
        Restore-Env
    }
    return [pscustomobject]@{ Code = $code; Out = ($out | Out-String) }
}

function Start-ZqProc {
    param([string]$Exe, [int]$Port, [string]$DbUrl = $Dsn)
    Use-SandboxEnv $DbUrl
    try {
        $p = Start-Process -FilePath $Exe -ArgumentList @('start', '--port', "$Port") `
            -WindowStyle Hidden -WorkingDirectory $RunDir -PassThru `
            -RedirectStandardOutput "$RunDir\p$Port.out.log" `
            -RedirectStandardError  "$RunDir\p$Port.err.log"
    }
    finally { Restore-Env }
    $ok = $false
    for ($i = 0; $i -lt 40; $i++) {
        Start-Sleep -Milliseconds 500
        try {
            $null = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec 2
            $ok = $true; break
        } catch { }
    }
    return [pscustomobject]@{ Pid = $p.Id; Healthy = $ok }
}

# ── psql helper ──────────────────────────────────────────────────────────────
$PsqlExe = (Get-ChildItem 'C:\Program Files\PostgreSQL\*\bin\psql.exe' -ErrorAction SilentlyContinue |
    Sort-Object FullName -Descending | Select-Object -First 1).FullName

function Invoke-Psql([string]$Sql) {
    # psql prints benign notices (e.g. DROP IF EXISTS) to stderr; with
    # EAP=Stop the 2>&1 merge would abort the script. Collect, never throw.
    $env:PGPASSWORD = 'postgres'
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & $PsqlExe -w -h 127.0.0.1 -p 5432 -U postgres -d postgres -v ON_ERROR_STOP=1 -c $Sql 2>&1
        $code = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $prev
        Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
    }
    return [pscustomobject]@{ Code = $code; Out = ($out | Out-String) }
}

# ── state probes ─────────────────────────────────────────────────────────────
function Test-RegEntry { Test-Path $RegKey }

function Get-RegValue([string]$Name) {
    (Get-ItemProperty $RegKey -ErrorAction SilentlyContinue).$Name
}

function Test-Task {
    $null = & cmd.exe /c "schtasks.exe /Query /TN webhook-zq 2>nul"
    return ($LASTEXITCODE -eq 0)
}

function Test-Service {
    $null = & cmd.exe /c "sc.exe query webhook-zq 2>nul"
    return ($LASTEXITCODE -eq 0)
}

function Test-DbExists {
    $r = Invoke-Psql "SELECT 1 FROM pg_database WHERE datname = '$DbName'"
    return ($r.Code -eq 0 -and $r.Out -match '1 row')
}

function Get-PathSnapshot {
    (& reg.exe query HKCU\Environment /v Path 2>&1 | Out-String).Trim()
}

function Ensure-CleanState {
    # kill only sandboxed webhook-zq processes (never the D:\webhook-zq main instance)
    Get-Process -Name 'webhook-zq' -ErrorAction SilentlyContinue | Where-Object {
        $_.Path -and ($_.Path -like "$Wk*" -or $_.Path -like "$SBox*" -or $_.Path -like 'D:\webhook-zq-evil*')
    } | Stop-Process -Force -ErrorAction SilentlyContinue

    # cmd-level 2>nul: PS5.1 EAP=Stop turns native stderr into a terminating
    # error even with *> $null, so the not-found cases must die inside cmd.
    $null = & cmd.exe /c "reg.exe delete HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\webhook-zq /f 2>nul"
    $null = & cmd.exe /c "schtasks.exe /Delete /TN webhook-zq /F 2>nul"
    $null = & cmd.exe /c "sc.exe stop webhook-zq 2>nul"
    $null = & cmd.exe /c "sc.exe delete webhook-zq 2>nul"
    Start-Sleep -Milliseconds 300
    Remove-Item $AppDir -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item "$env:TEMP\webhook-zq-uninstalled.exe" -Force -ErrorAction SilentlyContinue
}

# ═════════════════════════════════════════════════════════════════════════════
# Preflight
# ═════════════════════════════════════════════════════════════════════════════
Write-Host '== preflight =='
# Always refresh from the canonical fresh build: the acceptance must exercise
# the CURRENT working-tree source (the three fixes are uncommitted; older exes
# like bin\webhook-zq.exe 20:01 or %TEMP% copies from earlier runs are stale).
$Fresh = 'D:\webhook-zq\bin\webhook-zq-accept.exe'
if (-not (Test-Path $Fresh)) {
    throw "fresh build not found: $Fresh (run: go build -trimpath -o $Fresh .\cmd\webhook-tester)"
}
Copy-Item $Fresh $Build -Force
Remove-Item "$Wk\webhook-zq.exe" -Force -ErrorAction SilentlyContinue   # stale earlier build
Write-Host ("acceptance exe: {0} (built {1})" -f $Build, (Get-Item $Build).LastWriteTime)
Copy-Item $Build $Uninst -Force
New-Item -ItemType Directory -Force -Path $RunDir | Out-Null
# stdin feed files - created here so the earliest interactive case (B1) has them
Set-Content -Path "$Wk\in-n.txt"      -Value 'n'        -Encoding ascii
Set-Content -Path "$Wk\in-y.txt"      -Value 'y'        -Encoding ascii
Set-Content -Path "$Wk\in-yyes.txt"   -Value "y`nyes`nyes" -Encoding ascii   # 3 gates: continue / drop DB / type yes
Set-Content -Path "$Wk\in-ywrong.txt" -Value "y`nnope"  -Encoding ascii
if (-not $PsqlExe) { throw 'psql.exe not found under C:\Program Files\PostgreSQL\*' }

$r = Invoke-Psql 'select 1'
if ($r.Code -ne 0) { throw "local PostgreSQL is not reachable: $($r.Out)" }

Ensure-CleanState
$PathBefore = Get-PathSnapshot

# throwaway DB
$null = Invoke-Psql "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$DbName'"
$null = Invoke-Psql "DROP DATABASE IF EXISTS $DbName"
$r = Invoke-Psql "CREATE DATABASE $DbName"
if ($r.Code -ne 0) { throw "cannot create test database: $($r.Out)" }

$mig = Invoke-Zq -Exe $Build -Arguments @('migrate') -DbUrl $Dsn
Check 'pre-migrate schema applied' ($mig.Code -eq 0)
if ($mig.Code -ne 0) { Write-Host $mig.Out; throw 'migrate failed, aborting' }

# ═════════════════════════════════════════════════════════════════════════════
# Phase A - install
# ═════════════════════════════════════════════════════════════════════════════
Write-Host ''
Write-Host '== A: install =='

$r = Invoke-Zq -Exe $Build -Arguments @('install')
Check 'A1 install exit 0'              ($r.Code -eq 0)
Check 'A1 install prints destination'  ($r.Out -match '已安装到')
Check 'A1 installed exe exists'        (Test-Path $InstExe)
Check 'A1 installed exe identical'     ((Get-FileHash $Build).Hash -eq (Get-FileHash $InstExe).Hash)

Check 'A2 registry entry exists'       (Test-RegEntry)
Check 'A2 DisplayName'                 ((Get-RegValue 'DisplayName') -eq 'webhook-zq')
Check 'A2 DisplayVersion non-empty'    ([string](Get-RegValue 'DisplayVersion') -ne '')
Check 'A2 InstallLocation'             ((Get-RegValue 'InstallLocation') -eq "$AppDir\bin")
Check 'A2 UninstallString'             ((Get-RegValue 'UninstallString') -eq ('"' + $InstExe + '" uninstall'))
Check 'A2 DisplayIcon'                 ((Get-RegValue 'DisplayIcon') -eq "$InstExe,0")
Check 'A2 NoModify=1'                  ((Get-RegValue 'NoModify') -eq 1)
Check 'A2 NoRepair=1'                  ((Get-RegValue 'NoRepair') -eq 1)
Check 'A2 EstimatedSize>0'             ((Get-RegValue 'EstimatedSize') -gt 0)

$PathAfterInstall = Get-PathSnapshot
if ($PathAfterInstall -eq $PathBefore) {
    Check 'A3 HKCU Path untouched by install' $true
    Info 'F1 install does not add bin to PATH (documented behavior; README wording was fixed to "该目录不在 PATH 上"; an install-time PATH write is a v1.0.2 candidate)'
}
else {
    Check 'A3 HKCU Path untouched by install' $false
}

$r2 = Invoke-Zq -Exe $Build -Arguments @('install')
Check 'A4 second install exit 0 (idempotent)' ($r2.Code -eq 0)
Check 'A4 prints install destination'         ($r2.Out -match '已安装到')
Check 'A4 registry still single entry'        (Test-RegEntry)
Check 'A4 installed exe still present'        (Test-Path $InstExe)
if ($r2.Out -notmatch '文件已更新') {
    Info 'F5 (minor, I8 wording): the second install re-prints the plain 已安装到 line and re-copies silently - no "文件已更新" distinction exists in the output (pm I8 expected that wording).'
}

$r3 = Invoke-Zq -Exe $Build -Arguments @('install', '--autostart')
Check 'A5 install --autostart exit 0'  ($r3.Code -eq 0)
Check 'A5 logon task created'          (Test-Task)
$xml = (& schtasks.exe /Query /TN webhook-zq /XML 2>&1 | Out-String)
Check 'A5 task runs installed exe'     ($xml.ToLower().Contains($InstExe.ToLower()))
Check 'A5 task argument is tray'      ($xml -match '<Arguments>tray</Arguments>')

# I9: install captures the shell's DATABASE_URL into the config file, so the
# autostart task (bare `start`, empty scheduled env) finds a DSN. An existing
# different value in the file is never overwritten.
$r = Invoke-Zq -Exe $Build -Arguments @('install') -DbUrl $Dsn
Check 'I9 install-with-DSN exit 0'        ($r.Code -eq 0)
Check 'I9 config.json created by install' (Test-Path "$AppDir\config.json")
$cfgText = if (Test-Path "$AppDir\config.json") { [System.IO.File]::ReadAllText("$AppDir\config.json") } else { '' }
Check 'I9 config contains the DSN'        ($cfgText.Contains($Dsn))
Check 'I9 prints capture note'            ($r.Out -match '已把当前 DATABASE_URL 记入')
$Dsn2 = "postgres://postgres:postgres@127.0.0.1:5432/${DbName}b`?sslmode=disable"
$r = Invoke-Zq -Exe $Build -Arguments @('install') -DbUrl $Dsn2
$cfgText = [System.IO.File]::ReadAllText("$AppDir\config.json")
Check 'I9 different env DSN not overwritten' ($cfgText.Contains($Dsn) -and -not $cfgText.Contains($Dsn2))
Check 'I9 prints no-overwrite note'          ($r.Out -match '未用当前环境变量覆盖')

# I7: end-to-end logon-task run. The task inherits the REAL user environment,
# so its `start` reads the real %LOCALAPPDATA%\webhook-zq\config.json - not
# the sandbox one. That file does not exist on this machine; it is created
# here with the test DSN + a free port, and removed afterwards.
$hadRealCfg = Test-Path $RealCfg
if ($hadRealCfg) { Copy-Item $RealCfg "$Wk\real-config-backup.json" -Force }
New-Item -ItemType Directory -Force -Path (Split-Path $RealCfg) | Out-Null
[System.IO.File]::WriteAllText($RealCfg,
    ('{"schema":1,"database_url":"' + $Dsn + '","port":18098}'))
try {
    $null = & cmd.exe /c "schtasks.exe /Run /TN webhook-zq 2>nul"
    $i7accepted = ($LASTEXITCODE -eq 0)
    $i7healthy = $false
    for ($i = 0; $i -lt 40; $i++) {
        Start-Sleep -Milliseconds 500
        try {
            $null = Invoke-WebRequest -Uri 'http://127.0.0.1:18098/healthz' -UseBasicParsing -TimeoutSec 2
            $i7healthy = $true; break
        } catch { }
    }
    $i7proc = @(Get-Process -Name 'webhook-zq' -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -like "$AppDir*" })
    $sti = Get-ScheduledTaskInfo -TaskName webhook-zq -ErrorAction SilentlyContinue
    $i7res = if ($sti) { $sti.LastTaskResult } else { -1 }
    Check 'I7 task run accepted'                     $i7accepted
    Check 'I7 server healthy (DSN+port via config)'  $i7healthy
    Check 'I7 task process is the installed exe'     ($i7proc.Count -ge 1)
    Check 'I7 LastTaskResult 0/running'              ($i7res -eq 0 -or $i7res -eq 267009)
    $i7proc | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
}
finally {
    if ($hadRealCfg) { Move-Item "$Wk\real-config-backup.json" $RealCfg -Force }
    else { Remove-Item $RealCfg -Force -ErrorAction SilentlyContinue }
}
Check 'I7 real config state restored' ((Test-Path $RealCfg) -eq $hadRealCfg)


$r4 = Invoke-Zq -Exe $InstExe -Arguments @('install')
Check 'A6 self-install exit 0'                ($r4.Code -eq 0)
Check 'A6 self-install skips copy'            ($r4.Out -match '跳过复制')

$h1 = Invoke-Zq -Exe $Build -Arguments @('install', '--help')
$h2 = Invoke-Zq -Exe $Build -Arguments @('uninstall', '--help')
Check 'A7 install help works'   ($h1.Code -eq 0)
Check 'A7 uninstall help works' ($h2.Code -eq 0)
if ($h1.Out -notmatch '--json' -and $h2.Out -notmatch '--json' -and
    $h1.Out -notmatch '--dry-run' -and $h2.Out -notmatch '--dry-run') {
    Info 'F2 install/uninstall expose no --json / --dry-run flags (status has --json; pm criteria expect contract parity)'
}

# ═════════════════════════════════════════════════════════════════════════════
# Phase B - interactive safety
# ═════════════════════════════════════════════════════════════════════════════
Write-Host ''
Write-Host '== B: interactive safety =='

# B cases use the differently-named binary (download flow): a webhook-zq.exe
# runner lists ITSELF via the ourDir rule, which would pollute the interactive
# safety assertions with a noise item.
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall') -StdinFile "$Wk\in-n.txt"
Check 'B1 decline exits 0'             ($r.Code -eq 0)
Check 'B1 decline prints cancel'       ($r.Out -match '已取消')
Check 'B1 registry intact'             (Test-RegEntry)
Check 'B1 task intact'                 (Test-Task)
Check 'B1 files intact'                (Test-Path $InstExe)

$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall') -StdinFile 'nul'
Check 'B2 EOF refused (non-zero exit)' ($r.Code -ne 0)
Check 'B2 EOF explains --yes'          ($r.Out -match '无法读取确认输入')
Check 'B2 registry intact'             (Test-RegEntry)
Check 'B2 files intact'                (Test-Path $InstExe)

# ═════════════════════════════════════════════════════════════════════════════
# Phase C - uninstall with live processes, prefix traps, service
# ═════════════════════════════════════════════════════════════════════════════
Write-Host ''
Write-Host '== C: processes, traps, service =='

# three evil twins: same exe name, directories that are PREFIX siblings of the
# dirs the uninstaller owns:
#   D:\webhook-zq-evil                       (team-lead's literal trap)
#   sbox\webhook-zq-evil  vs appDir sbox\webhook-zq   (appDir prefix trap)
#   TEMP\wzq-accept-evil  vs uninstaller dir TEMP\wzq-accept (ourDir prefix trap)
New-Item -ItemType Directory -Force -Path 'D:\webhook-zq-evil'        | Out-Null
New-Item -ItemType Directory -Force -Path "$SBox\webhook-zq-evil"     | Out-Null
New-Item -ItemType Directory -Force -Path "$env:TEMP\wzq-accept-evil" | Out-Null
Copy-Item $Build 'D:\webhook-zq-evil\webhook-zq.exe' -Force
Copy-Item $Build "$SBox\webhook-zq-evil\webhook-zq.exe" -Force
Copy-Item $Build "$env:TEMP\wzq-accept-evil\webhook-zq.exe" -Force

$evilD = Start-ZqProc -Exe 'D:\webhook-zq-evil\webhook-zq.exe' -Port 18097
$evilS = Start-ZqProc -Exe "$SBox\webhook-zq-evil\webhook-zq.exe" -Port 18096
$evilT = Start-ZqProc -Exe "$env:TEMP\wzq-accept-evil\webhook-zq.exe" -Port 18095
$legit = Start-ZqProc -Exe $InstExe -Port 18098
Check 'C1 legit installed server healthy'    $legit.Healthy
Check 'C1 evil D:\webhook-zq-evil healthy'   $evilD.Healthy
Check 'C1 evil sbox\webhook-zq-evil healthy' $evilS.Healthy
Check 'C1 evil wzq-accept-evil healthy'      $evilT.Healthy

# uninstall via the differently-named binary (download flow)
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--yes')
Check 'C2 uninstall --yes exit 0'        ($r.Code -eq 0)
Check 'C2 prints completion'             ($r.Out -match '卸载完成')
$iProc = Idx $r.Out '正在运行的进程'
$iTask = Idx $r.Out '开机自启任务'
$iReg  = Idx $r.Out '应用和功能'
$iFile = Idx $r.Out '程序文件'
Check 'C2 order: processes < task'       ($iProc -ge 0 -and $iTask -gt $iProc)
Check 'C2 order: task < registry'        ($iTask -ge 0 -and $iReg -gt $iTask)
Check 'C2 order: registry < files'       ($iReg -ge 0 -and $iFile -gt $iReg)
Check 'C2 legit process killed'          (-not (Get-Process -Id $legit.Pid -ErrorAction SilentlyContinue))
$evilDAlive = [bool](Get-Process -Id $evilD.Pid -ErrorAction SilentlyContinue)
$evilSAlive = [bool](Get-Process -Id $evilS.Pid -ErrorAction SilentlyContinue)
$evilTAlive = [bool](Get-Process -Id $evilT.Pid -ErrorAction SilentlyContinue)
Check 'C2 D:\webhook-zq-evil NOT killed'    $evilDAlive
Check 'C2 sbox\webhook-zq-evil NOT killed'  $evilSAlive
Check 'C2 wzq-accept-evil NOT killed'       $evilTAlive
Check 'C2 registry removed'                 (-not (Test-RegEntry))
Check 'C2 task removed'                     (-not (Test-Task))
Check 'C2 appDir removed'                   (-not (Test-Path $AppDir))
Check 'C2 database preserved by default'    (Test-DbExists)
Check 'C2 no temp self-rename residue'      (-not (Test-Path "$env:TEMP\webhook-zq-uninstalled.exe"))

# cleanup evils
foreach ($p in @($evilD, $evilS, $evilT)) {
    Stop-Process -Id $p.Pid -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Milliseconds 500

# service path: reinstall, create service (elevated shell), run server, uninstall
$r = Invoke-Zq -Exe $Build -Arguments @('install')
Check 'C4 reinstall exit 0' ($r.Code -eq 0)
$null = & sc.exe create webhook-zq binPath= "$InstExe start --port 18094" start= demand
Check 'C4 service created' (Test-Service)
$legit2 = Start-ZqProc -Exe $InstExe -Port 18093
Check 'C4 second server healthy' $legit2.Healthy

$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--yes')
Check 'C4 uninstall with service exit 0' ($r.Code -eq 0)
$iProc = Idx $r.Out '正在运行的进程'
$iSvc  = Idx $r.Out 'Windows 服务'
$iReg  = Idx $r.Out '应用和功能'
Check 'C4 order: processes < service' ($iProc -ge 0 -and $iSvc -gt $iProc)
Check 'C4 order: service < registry'  ($iSvc -ge 0 -and $iReg -gt $iSvc)
Check 'C4 service removed'            (-not (Test-Service))
Check 'C4 appDir removed'             (-not (Test-Path $AppDir))

# ═════════════════════════════════════════════════════════════════════════════
# Phase D - database gates (U5)
# ═════════════════════════════════════════════════════════════════════════════
Write-Host ''
Write-Host '== D: database gates =='

function Write-TestConfig([string]$Url) {
    # [IO.File]::WriteAllText = UTF-8 without BOM (PS5.1 has no utf8NoBOM and a
    # BOM would break Go's json decoder).
    New-Item -ItemType Directory -Force -Path $AppDir | Out-Null
    [System.IO.File]::WriteAllText("$AppDir\config.json",
        ('{"schema":1,"database_url":"' + $Url + '"}'))
}

# D1: --yes without --drop-database -> everything removed, DB kept + hint (U4)
$r = Invoke-Zq -Exe $Build -Arguments @('install')
Write-TestConfig $Dsn
$srv = Start-ZqProc -Exe $InstExe -Port 18092
Check 'D1 server healthy' $srv.Healthy
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--yes')
Check 'D1 exit 0'                          ($r.Code -eq 0)
Check 'D1 wizard lists database by name'   ($r.Out -match $DbName)
Check 'D1 wizard shows config.json owner'  ($r.Out -match 'config\.json')
Check 'D1 database still exists'           (Test-DbExists)
Check 'D1 prints keep-hint with flag'      ($r.Out -match '库已保留' -and $r.Out -match '--drop-database')
Check 'D1 config.json removed same run'    (-not (Test-Path "$AppDir\config.json"))
Check 'D1 appDir removed'                  (-not (Test-Path $AppDir))
Check 'D1 server killed'                   (-not (Get-Process -Id $srv.Pid -ErrorAction SilentlyContinue))

# D3 (before D2 - needs the DB alive): interactive --drop-database via
# redirected stdin. Interactive drop has THREE gates: 1) continue (y/N=y),
# 2) drop the database too (y/N=N), 3) type the literal word yes. The shared
# bufio.Reader (prompts.in) keeps buffered lines across all three, so
# 'y\nyes\nyes' passes them in one piped run - regression check for the old F4
# (a fresh reader per line used to eat the remaining lines and abort on EOF).
$r = Invoke-Zq -Exe $Build -Arguments @('install')
Write-TestConfig $Dsn
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--drop-database') -StdinFile "$Wk\in-yyes.txt"
Check 'D3 redirected stdin passes all gates' ($r.Code -eq 0)
Check 'D3 database dropped via piped answers' (-not (Test-DbExists))
Check 'D3 appDir removed'                      (-not (Test-Path $AppDir))

# D3b: a wrong answer at gate 2 declines the DROP only - the uninstall itself
# proceeds and removes everything else, keeping the database (exit 0).
$null = Invoke-Psql "CREATE DATABASE $DbName"
$r = Invoke-Zq -Exe $Build -Arguments @('install')
Write-TestConfig $Dsn
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--drop-database') -StdinFile "$Wk\in-ywrong.txt"
Check 'D3b wrong answer keeps database'     ((Test-DbExists) -and ($r.Out -match '数据库已保留'))
Check 'D3b wrong answer still removes files' (-not (Test-Path $AppDir))
Check 'D3b exit 0'                           ($r.Code -eq 0)
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--yes')
$null = $r

# D2: --yes --drop-database -> dropped, no typed confirmation needed (U6/U10)
$r = Invoke-Zq -Exe $Build -Arguments @('install')
Write-TestConfig $Dsn
$srv = Start-ZqProc -Exe $InstExe -Port 18091
Check 'D2 server healthy' $srv.Healthy
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--yes', '--drop-database')
Check 'D2 exit 0'                    ($r.Code -eq 0)
Check 'D2 database dropped'          (-not (Test-DbExists))
Check 'D2 appDir removed'            (-not (Test-Path $AppDir))

# D4: unreachable database -> warning, uninstall still proceeds
$r = Invoke-Zq -Exe $Build -Arguments @('install')
Write-TestConfig 'postgres://postgres:postgres@127.0.0.1:5999/webhook_rd?sslmode=disable'
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall') -StdinFile "$Wk\in-y.txt"
Check 'D4 unreachable noted'   ($r.Out -match '不可达')
Check 'D4 exit 0'              ($r.Code -eq 0)
Check 'D4 appDir removed'      (-not (Test-Path $AppDir))

# U11: tray identification. The three desktop scripts (tray.ps1,
# server-manager.ps1, start-gui.ps1) restart the server, so they must be
# killed FIRST; identification = command line contains "webhook-zq" AND one
# of the script names. A same-named script from another project must NOT
# match (killing a foreign window would be far worse than missing ours).
$r = Invoke-Zq -Exe $Build -Arguments @('install')
Write-TestConfig $Dsn
# D2 dropped the test database, so recreate + re-migrate it (same as preflight)
# before starting the server, or healthz never turns healthy.
$null = Invoke-Psql "CREATE DATABASE $DbName"
$m = Invoke-Zq -Exe $Build -Arguments @('migrate') -DbUrl $Dsn
if ($m.Code -ne 0) { Info "U11 re-migrate failed: $($m.Out)" }
$srv = Start-ZqProc -Exe $InstExe -Port 18099
Check 'U11 server healthy' $srv.Healthy
$realTray = Start-Process powershell.exe -WindowStyle Hidden -PassThru `
    -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 300 # webhook-zq scripts tray.ps1'
$decoyTray = Start-Process powershell.exe -WindowStyle Hidden -PassThru `
    -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 300 # other-project scripts tray.ps1'
Start-Sleep -Seconds 2
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--yes')
Check 'U11 uninstall exit 0'            ($r.Code -eq 0)
Check 'U11 tray item listed'            ($r.Out -match '托盘')
Check 'U11 tray item listed first'      ((Idx $r.Out '托盘') -ge 0 -and (Idx $r.Out '托盘') -lt (Idx $r.Out '正在运行的进程'))
Check 'U11 our tray process killed'     (-not (Get-Process -Id $realTray.Id -ErrorAction SilentlyContinue))
Check 'U11 foreign tray decoy survives' ($null -ne (Get-Process -Id $decoyTray.Id -ErrorAction SilentlyContinue))
Stop-Process -Id $decoyTray.Id -Force -ErrorAction SilentlyContinue

# ═════════════════════════════════════════════════════════════════════════════
# Phase E - idempotency and the Apps&features path (UninstallString)
# ═════════════════════════════════════════════════════════════════════════════
Write-Host ''
Write-Host '== E: idempotency + Apps&features path =='

# E1 uses the differently-named binary (download flow): the runner's name does
# not match the webhook-zq.exe process filter, so "nothing installed" is clean.
$r = Invoke-Zq -Exe $Uninst -Arguments @('uninstall', '--yes')
Check 'E1 clean-slate uninstall exit 0'      ($r.Code -eq 0)
Check 'E1 prints nothing to remove'         ($r.Out -match '未发现任何已安装内容')

# E2: run the INSTALLED exe (webhook-zq.exe, the UninstallString target).
# Regression check for the old F3 P0: the process scan used to kill the
# uninstaller itself (no own-pid exclusion) and nothing after that step ran;
# now the full sweep must complete with zero residue.
$r = Invoke-Zq -Exe $Build -Arguments @('install')
Check 'E2 setup install exit 0' ($r.Code -eq 0)
$r = Invoke-Zq -Exe $InstExe -Arguments @('uninstall', '--yes')
Check 'E2 Apps&features path completes' ($r.Code -eq 0 -and $r.Out -match '卸载完成')
Check 'E2 registry removed'             (-not (Test-RegEntry))
Check 'E2 files removed'                (-not (Test-Path $InstExe))
Check 'E2 task removed'                 (-not (Test-Task))

# ═════════════════════════════════════════════════════════════════════════════
# Final sweep
# ═════════════════════════════════════════════════════════════════════════════
Write-Host ''
Write-Host '== final sweep =='

Ensure-CleanState
# leftover DB (only if D-phase kept it alive)
if (Test-DbExists) {
    $null = Invoke-Psql "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$DbName'"
    $null = Invoke-Psql "DROP DATABASE IF EXISTS $DbName"
}
Remove-Item 'D:\webhook-zq-evil' -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item "$env:TEMP\wzq-accept-evil" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item $SBox -Recurse -Force -ErrorAction SilentlyContinue

Check 'S1 registry gone'      (-not (Test-RegEntry))
Check 'S1 task gone'          (-not (Test-Task))
Check 'S1 service gone'       (-not (Test-Service))
Check 'S1 appDir gone'        (-not (Test-Path $AppDir))
Check 'S1 test DB dropped'    (-not (Test-DbExists))
Check 'S1 no sandbox procs left' (-not (Get-Process -Name 'webhook-zq' -ErrorAction SilentlyContinue | Where-Object {
    $_.Path -and ($_.Path -like "$Wk*" -or $_.Path -like "$SBox*" -or $_.Path -like 'D:\webhook-zq-evil*') }))
$PathEnd = Get-PathSnapshot
Check 'S1 HKCU Path untouched overall' ($PathEnd -eq $PathBefore)

Write-Host ''
Write-Host "RESULT pass=$($script:Pass) fail=$($script:Fail)"
if ($script:Findings.Count -gt 0) {
    Write-Host 'FINDINGS:'
    foreach ($f in $script:Findings) { Write-Host "  - $f" }
}
exit $(if ($script:Fail -eq 0) { 0 } else { 1 })
