[CmdletBinding()]
param(
    [string]$Version,
    [string]$Output,
    [string]$GOOS = $env:GOOS,
    [string]$GOARCH = $env:GOARCH,
    [ValidateSet("auto", "on", "off")]
    [string]$CGO = "auto",
    [switch]$Race,
    [switch]$Rebuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot

function Invoke-GitText {
    param([string[]]$Arguments)
    try {
        return (& git -C $Root @Arguments 2>$null | Select-Object -First 1).Trim()
    } catch {
        return ""
    }
}

function Resolve-Version {
    if (-not [string]::IsNullOrWhiteSpace($Version)) {
        return $Version.TrimStart("v")
    }
    if (-not [string]::IsNullOrWhiteSpace($env:CODEX_GO_VERSION)) {
        return $env:CODEX_GO_VERSION.TrimStart("v")
    }
    $tag = Invoke-GitText -Arguments @("describe", "--tags", "--exact-match", "HEAD")
    if ($tag) {
        return $tag -replace "^(?:go-)?v", ""
    }
    $commit = Invoke-GitText -Arguments @("rev-parse", "--short=12", "HEAD")
    if ($commit) {
        return "0.0.0-dev+$commit"
    }
    return "0.0.0-dev"
}

$HostGOOS = (& go env GOOS).Trim()
$HostGOARCH = (& go env GOARCH).Trim()
if (-not $GOOS) { $GOOS = $HostGOOS }
if (-not $GOARCH) { $GOARCH = $HostGOARCH }
$ResolvedVersion = Resolve-Version
$Extension = if ($GOOS -eq "windows") { ".exe" } else { "" }
if ([string]::IsNullOrWhiteSpace($Output)) {
    $Output = Join-Path $Root "bin/codex$Extension"
} elseif (-not [System.IO.Path]::IsPathRooted($Output)) {
    $Output = Join-Path $Root $Output
}

$Output = [System.IO.Path]::GetFullPath($Output)
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null

$env:GOOS = $GOOS
$env:GOARCH = $GOARCH
switch ($CGO) {
    "on" { $env:CGO_ENABLED = "1" }
    "off" { $env:CGO_ENABLED = "0" }
    default {
        if ($GOOS -ne $HostGOOS -or $GOARCH -ne $HostGOARCH) {
            $env:CGO_ENABLED = "0"
        }
    }
}

$ldflags = "-s -w -X codex_go/doctor.buildVersion=$ResolvedVersion -X codex_go/appserver.buildVersion=$ResolvedVersion -X codex_go/mcp.buildVersion=$ResolvedVersion"
$arguments = @("build", "-trimpath", "-buildvcs=false", "-ldflags", $ldflags, "-o", $Output)
if ($Race) { $arguments += "-race" }
if ($Rebuild) { $arguments += "-a" }
$arguments += "./cmd/codex"

Write-Host "==> Building Codex Go $ResolvedVersion for $GOOS/$GOARCH"
& go @arguments
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> Built $Output"
if ($GOOS -eq "windows") {
    $ResourcesDir = Join-Path (Split-Path -Parent $Output) "codex-resources"
    New-Item -ItemType Directory -Force -Path $ResourcesDir | Out-Null
    $WindowsHelpers = @(
        @{ Name = "codex-command-runner.exe"; Package = "./cmd/codex-command-runner" },
        @{ Name = "codex-windows-sandbox-setup.exe"; Package = "./cmd/codex-windows-sandbox-setup" }
    )
    foreach ($Helper in $WindowsHelpers) {
        $HelperOutput = Join-Path $ResourcesDir $Helper.Name
        $HelperArguments = @("build", "-trimpath", "-buildvcs=false", "-ldflags", $ldflags, "-o", $HelperOutput)
        if ($Race) { $HelperArguments += "-race" }
        if ($Rebuild) { $HelperArguments += "-a" }
        $HelperArguments += $Helper.Package
        & go @HelperArguments
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        Write-Host "==> Built $HelperOutput"
    }
}
$HostOutput = Join-Path (Split-Path -Parent $Output) "codex-code-mode-host$Extension"
$hostArguments = @("build", "-trimpath", "-buildvcs=false", "-ldflags", $ldflags, "-o", $HostOutput)
if ($Race) { $hostArguments += "-race" }
if ($Rebuild) { $hostArguments += "-a" }
$hostArguments += "./cmd/codex-code-mode-host"
& go @hostArguments
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "==> Built $HostOutput"
if ($GOOS -eq $HostGOOS -and $GOARCH -eq $HostGOARCH) {
    & $Output --version
}
