# Runs the packages that have shown ambient-environment dependencies with a
# deliberately hostile environment. A green run here means the suite does not
# depend on the machine that runs it; a red one prints the offending tests.
#
# Usage:
#   pwsh -File scripts/test_hostile_env.ps1
#   pwsh -File scripts/test_hostile_env.ps1 -Packages ./tui/... -Count 1
[CmdletBinding()]
param(
    [string[]]$Packages = @('./tui/...', './app/...', './appserver/...'),
    [int]$Count = 1
)

$ErrorActionPreference = 'Stop'

# Every variable below has already bitten at least one test: colour detection
# (NO_COLOR/WT_SESSION/TERM/FORCE_COLOR/CLICOLOR_FORCE) and clipboard/SSH
# detection (SSH_CONNECTION/SSH_TTY).
$hostile = [ordered]@{
    NO_COLOR       = '1'
    WT_SESSION     = 'x'
    SSH_CONNECTION = '10.0.0.1 51234 10.0.0.2 22'
    SSH_TTY        = '/dev/pts/9'
    TERM           = 'dumb'
    FORCE_COLOR    = '1'
    CLICOLOR_FORCE = '1'
    CLICOLOR       = '1'
}

$saved = @{}
foreach ($key in $hostile.Keys) {
    $saved[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
    Set-Item -Path "Env:$key" -Value $hostile[$key]
}

$results = @()
try {
    $description = ($hostile.Keys | ForEach-Object { "$_=$($hostile[$_])" }) -join ' '
    Write-Host "Hostile environment: $description" -ForegroundColor Yellow
    foreach ($package in $Packages) {
        Write-Host "==> go test $package -count=$Count" -ForegroundColor Cyan
        $output = & go test $package "-count=$Count" 2>&1
        $code = $LASTEXITCODE
        $lines = @($output | ForEach-Object { [string]$_ })
        if ($code -eq 0) {
            $results += [pscustomobject]@{ Package = $package; Status = 'PASS'; Failures = @() }
            Write-Host "PASS $package" -ForegroundColor Green
        } else {
            $failures = @($lines | Where-Object { $_ -match '^\s*--- FAIL:' })
            $results += [pscustomobject]@{ Package = $package; Status = 'FAIL'; Failures = $failures }
            Write-Host "FAIL $package" -ForegroundColor Red
            foreach ($failure in $failures) { Write-Host "    $failure" -ForegroundColor Red }
        }
    }
} finally {
    foreach ($key in $hostile.Keys) {
        if ($null -eq $saved[$key]) {
            Remove-Item -Path "Env:$key" -ErrorAction SilentlyContinue
        } else {
            Set-Item -Path "Env:$key" -Value $saved[$key]
        }
    }
}

Write-Host ''
Write-Host 'Summary' -ForegroundColor Yellow
foreach ($result in $results) {
    $color = if ($result.Status -eq 'PASS') { 'Green' } else { 'Red' }
    Write-Host ("  {0,-7} {1}" -f $result.Status, $result.Package) -ForegroundColor $color
    foreach ($failure in $result.Failures) { Write-Host "          $failure" -ForegroundColor Red }
}

$failed = @($results | Where-Object { $_.Status -ne 'PASS' })
if ($failed.Count -gt 0) {
    Write-Host ''
    Write-Host "$($failed.Count) package(s) failed under the hostile environment." -ForegroundColor Red
    exit 1
}
Write-Host ''
Write-Host 'All packages passed under the hostile environment.' -ForegroundColor Green
exit 0
