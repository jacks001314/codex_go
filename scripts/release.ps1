[CmdletBinding()]
param(
    [string]$Version,
    [string]$OutputDir,
    [string[]]$Targets = @(
        "windows/amd64", "windows/arm64",
        "linux/amd64", "linux/arm64",
        "darwin/amd64", "darwin/arm64"
    ),
    [switch]$SkipTests
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($Version)) {
    $VersionFile = Join-Path $Root "VERSION"
    if (Test-Path -LiteralPath $VersionFile) {
        $Version = ([System.IO.File]::ReadAllText($VersionFile)).Trim()
    } else {
        throw "Version required (-Version or a VERSION file at $VersionFile)."
    }
}
$Version = $Version.TrimStart("v")
if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$') {
    throw "Version must be semantic version text such as 1.2.3 or 1.2.3-beta.1."
}
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $Root "dist/v$Version"
} elseif (-not [System.IO.Path]::IsPathRooted($OutputDir)) {
    $OutputDir = Join-Path $Root $OutputDir
}
$OutputDir = [System.IO.Path]::GetFullPath($OutputDir)
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

if (-not $SkipTests) {
    Write-Host "==> Running release compile checks"
    & go test -run '^$' ./...
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

$Artifacts = @()
foreach ($Target in $Targets) {
    $parts = $Target.Split("/", 2)
    if ($parts.Count -ne 2) { throw "Invalid target '$Target'; expected GOOS/GOARCH." }
    $goos, $goarch = $parts
    $Name = "codex-go-v$Version-$goos-$goarch"
    $StageDir = Join-Path $OutputDir $Name
    New-Item -ItemType Directory -Force -Path $StageDir | Out-Null
    $Extension = if ($goos -eq "windows") { ".exe" } else { "" }
    $Binary = Join-Path $StageDir "codex$Extension"

    & (Join-Path $PSScriptRoot "build.ps1") -Version $Version -GOOS $goos -GOARCH $goarch -CGO off -Output $Binary

    $Notice = Join-Path $Root "NOTICE"
    $License = Join-Path $Root "LICENSE"
    if (Test-Path -LiteralPath $Notice) { Copy-Item -LiteralPath $Notice -Destination $StageDir }
    if (Test-Path -LiteralPath $License) { Copy-Item -LiteralPath $License -Destination $StageDir }
    Copy-Item -LiteralPath (Join-Path $Root "README.md") -Destination $StageDir

    if ($goos -eq "windows") {
        $Archive = Join-Path $OutputDir "$Name.zip"
        Compress-Archive -Path (Join-Path $StageDir "*") -DestinationPath $Archive -Force
    } else {
        $Archive = Join-Path $OutputDir "$Name.tar.gz"
        & tar -czf $Archive -C $OutputDir $Name
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    }
    Remove-Item -LiteralPath $StageDir -Recurse -Force
    $Artifacts += $Archive
}

$ChecksumPath = Join-Path $OutputDir "SHA256SUMS"
$Lines = foreach ($Artifact in $Artifacts) {
    $Hash = (Get-FileHash -LiteralPath $Artifact -Algorithm SHA256).Hash.ToLowerInvariant()
    "$Hash  $(Split-Path -Leaf $Artifact)"
}
[System.IO.File]::WriteAllLines($ChecksumPath, $Lines, [System.Text.UTF8Encoding]::new($false))

$Manifest = [ordered]@{
    version = $Version
    generatedAt = [DateTime]::UtcNow.ToString("o")
    artifacts = @($Artifacts | ForEach-Object { Split-Path -Leaf $_ })
}
$Manifest | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $OutputDir "release.json") -Encoding utf8

Write-Host "==> Release artifacts written to $OutputDir"
Get-ChildItem -LiteralPath $OutputDir | Select-Object Name,Length
