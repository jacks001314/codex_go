[CmdletBinding()]
param(
    [string]$Version,
    [string]$InstallDir,
    [ValidateSet("auto", "on", "off")]
    [string]$CGO = "auto",
    [switch]$Force
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$RunningOnWindows = $env:OS -eq "Windows_NT"
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    if ($RunningOnWindows) {
        $InstallDir = Join-Path $env:LOCALAPPDATA "Programs/CodexGo/bin"
    } else {
        $InstallDir = Join-Path $HOME ".local/bin"
    }
}
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$Extension = if ($RunningOnWindows) { ".exe" } else { "" }
$Destination = Join-Path $InstallDir "codex$Extension"
$Staging = Join-Path ([System.IO.Path]::GetTempPath()) ("codex-go-install-" + [guid]::NewGuid().ToString("N") + $Extension)

if ((Test-Path -LiteralPath $Destination) -and -not $Force) {
    Write-Host "==> Updating existing installation at $Destination"
}

try {
    & (Join-Path $PSScriptRoot "build.ps1") -Version $Version -Output $Staging -CGO $CGO
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Move-Item -LiteralPath $Staging -Destination $Destination -Force
} finally {
    if (Test-Path -LiteralPath $Staging) {
        Remove-Item -LiteralPath $Staging -Force
    }
}

Write-Host "==> Installed $Destination"
& $Destination --version
if (($env:PATH -split [System.IO.Path]::PathSeparator) -notcontains $InstallDir) {
    Write-Warning "$InstallDir is not on PATH. Add it to PATH to run 'codex' directly."
}
