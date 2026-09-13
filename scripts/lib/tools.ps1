# One answer to "is this tool installed, and where is it?".
#
# Why this file exists: psql / pg_dump / go are frequently installed but not on PATH - the
# PostgreSQL installer does not add its bin directory by default. Five scripts each probed
# for them with slightly different rules, so "missing" meant different things in different
# windows, and "psql is not installed" was almost always wrong.
#
# Usage (dot-source from a script in scripts/):
#   . (Join-Path $PSScriptRoot 'lib\tools.ps1')
#   $psql = Find-Tool 'psql' @('C:\Program Files\PostgreSQL\*\bin\psql.exe')
#   if ($psql) { Add-ToolPath $psql }

function Find-Tool {
    param(
        [Parameter(Mandatory)][string]$Name,
        [string[]]$Candidates = @()
    )

    # -CommandType Application matters: without it a shell function or alias with the same
    # name would be reported as "installed" and then fail when invoked as a path.
    $cmd = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1

    if ($cmd) { return $cmd.Source }

    foreach ($pattern in $Candidates) {
        if (-not $pattern) { continue }

        $hit = Get-ChildItem -Path $pattern -ErrorAction SilentlyContinue |
            Sort-Object -Property @{ Expression = { Get-ToolVersionOrder $_ }; Descending = $true },
                                  @{ Expression = { $_.FullName }; Descending = $true } |
            Select-Object -First 1

        if ($hit) { return $hit.FullName }
    }

    return $null
}

function Get-ToolVersionOrder {
    # Sort key for the candidate probe. PostgreSQL installs into
    # C:\Program Files\PostgreSQL\<major>\bin, so the newest install is the highest NUMBER,
    # not the highest STRING - as text "9" sorts after "16", which made the old probe pick
    # the OLDEST server on machines that had more than one.
    param([System.IO.FileInfo]$File)

    $dir = ''
    try { $dir = $File.Directory.Parent.Name } catch { }

    $n = 0
    if ([int]::TryParse($dir, [ref]$n)) { return $n }

    return 0
}

function Add-ToolPath {
    # createdb / pg_dump / dropdb sit next to psql, so putting that one directory on PATH
    # makes all of them callable by name.
    param([string]$ToolPath)

    if (-not $ToolPath) { return }

    $dir = Split-Path -Parent $ToolPath
    if (-not $dir) { return }

    if (";$env:PATH;" -notlike "*;$dir;*") { $env:PATH += ';' + $dir }
}
