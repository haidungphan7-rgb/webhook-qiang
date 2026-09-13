# One answer to "is the server running, is it healthy, and am I allowed to stop it?".
#
# Why this file exists: the tray, the manager window, the service script, the graphical
# launcher and both uninstallers each had their own copy. The dangerous part was not the
# duplication but the MISSING CHECK - three of them killed whatever listened on the port
# without proving the process belonged to this project. On a shared port that kills
# someone else's process.
#
# Usage (dot-source from a script in scripts/):
#   . (Join-Path $PSScriptRoot 'lib\process.ps1')
#   $id = Get-AppPid -Port 8080
#   if ($id) { $r = Stop-AppProcess -Id $id -Root $root }
#   Get-AppWindow | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }

# The windows that belong to this app. Matched against the full command line, because
# "powershell.exe" on its own says nothing about which script it is running.
$script:AppWindowPattern = 'tray\.ps1|server-manager\.ps1|start-gui\.ps1'

function Get-AppPid {
    param([Parameter(Mandatory)][uint16]$Port)

    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue |
        Select-Object -First 1

    if (-not $conn) { return $null }

    return $conn.OwningProcess
}

function Test-AppHealthy {
    param(
        [Parameter(Mandatory)][uint16]$Port,
        [int]$TimeoutSec = 2
    )

    try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec $TimeoutSec

        return ($r.StatusCode -eq 200)
    }
    catch {
        return $false
    }
}

function Get-AppProcessPath {
    # Returns $null when the path cannot be read (access denied, process gone, elevated
    # process). Callers must treat $null as "cannot prove it is ours".
    param([Parameter(Mandatory)][int]$Id)

    try { return (Get-Process -Id $Id -ErrorAction Stop).Path }
    catch { return $null }
}

function Stop-AppProcess {
    <#
    The ONLY implementation of "kill the server" in the project.

    A process is stopped only when its executable lives under $Root. When the path cannot
    be read we DO NOT KILL - that is exactly the case where we cannot prove it is ours.
    (An earlier version fell through to Stop-Process when the path was empty; the
    protection was inverted.)

    Returns @{ Stopped; Reason = 'stopped'|'no-path'|'not-ours'; Path; Id } so the caller
    can say WHY nothing was stopped instead of staying silent.
    #>
    param(
        [Parameter(Mandatory)][int]$Id,
        [Parameter(Mandatory)][string]$Root
    )

    $path = Get-AppProcessPath -Id $Id

    if (-not $path) {
        return [pscustomobject]@{ Stopped = $false; Reason = 'no-path'; Path = $null; Id = $Id }
    }

    # The trailing separator is not cosmetic. "D:\webhook-zq-evil\wh.exe" also starts with
    # "D:\webhook-zq", so a bare StartsWith would let a neighbouring project's process be
    # killed here. This check has to mean "inside this directory".
    $rootKey = $Root.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) +
    [IO.Path]::DirectorySeparatorChar

    if (-not $path.StartsWith($rootKey, [StringComparison]::OrdinalIgnoreCase)) {
        return [pscustomobject]@{ Stopped = $false; Reason = 'not-ours'; Path = $path; Id = $Id }
    }

    Stop-Process -Id $Id -Force -ErrorAction SilentlyContinue

    return [pscustomobject]@{ Stopped = $true; Reason = 'stopped'; Path = $path; Id = $Id }
}

function Stop-AppProcessByPort {
    param(
        [Parameter(Mandatory)][uint16]$Port,
        [Parameter(Mandatory)][string]$Root
    )

    $id = Get-AppPid -Port $Port

    if (-not $id) {
        return [pscustomobject]@{ Stopped = $false; Reason = 'not-running'; Path = $null; Id = $null }
    }

    return (Stop-AppProcess -Id $id -Root $Root)
}

function Get-AppWindow {
    # PowerShell processes running one of our scripts. Killing the server exe alone leaves
    # these alive - and the tray will simply start the server again.
    param([string]$Pattern = $script:AppWindowPattern)

    $procs = Get-CimInstance Win32_Process `
        -Filter "Name = 'powershell.exe' OR Name = 'pwsh.exe'" -ErrorAction SilentlyContinue

    # $PID is excluded: a script must never close the window it is running in.
    return @($procs | Where-Object {
            $_.ProcessId -ne $PID -and $_.CommandLine -and $_.CommandLine -match $Pattern
        })
}
