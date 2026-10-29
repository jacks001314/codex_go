#!/usr/bin/env pwsh
[CmdletBinding()]
param(
    [ValidateSet("smoke", "soak", "large", "faults", "ui", "memory", "disk", "all")]
    [string]$Suite = "smoke",
    [string]$Duration = "2h",
    [switch]$Race,
    [switch]$Report,
    # Write go test -json to a log file and print a per-test summary instead of
    # streaming raw output.
    [switch]$Json,
    # Run only one slice of the selected tests, in "index/count" form (1-based).
    # Shards are disjoint and cover every top-level test exactly once.
    [string]$Shard = "",
    # Run the trend analysis over the report directory after the suite passes.
    [switch]$Trend
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

Remove-Item Env:TERM -ErrorAction SilentlyContinue
$Root = Split-Path -Parent $PSScriptRoot
if (-not $env:GOCACHE) {
    $env:GOCACHE = Join-Path $Root ".gocache"
}
$env:CODEX_STABILITY_DURATION = $Duration
if (-not $env:CODEX_STABILITY_REPORT_DIR) {
    $env:CODEX_STABILITY_REPORT_DIR = Join-Path $Root "stability\reports"
}
if (-not $env:CODEX_STABILITY_COMMIT) {
    $commit = & git -C $Root rev-parse --short HEAD 2>$null
    if ($LASTEXITCODE -eq 0 -and $commit) {
        $env:CODEX_STABILITY_COMMIT = $commit.Trim()
    }
}

# Scenarios live in two places: the black-box package, plus the packages that
# own private helpers (memories reads rollouts, tui/tea renders frames,
# state owns the WAL). Only Test*Stability-shaped names run in those packages.
$packagesBySuite = @{
    smoke  = @("./stability/scenarios")
    soak   = @("./stability/scenarios")
    large  = @("./stability/scenarios", "./memories")
    faults = @("./stability/scenarios", "./turn", "./state")
    ui     = @("./stability/scenarios", "./tui/tea")
    memory = @("./stability/scenarios")
    disk   = @("./stability/scenarios", "./state")
    all    = @("./stability/scenarios", "./memories", "./tui/tea", "./state", "./turn")
}
$patternsBySuite = @{
    smoke  = "TestMockModel|TestMetricsDiskAndReport"
    soak   = "TestSoak"
    large  = "TestLarge"
    faults = "TestFault"
    ui     = "TestUI"
    memory = "TestMemory"
    disk   = "TestDisk"
    all    = "^(TestSoak|TestLarge|TestFault|TestUI|TestMemory|TestDisk|TestMockModel|TestMetricsDiskAndReport)"
}

$packages = $packagesBySuite[$Suite]
$run = $patternsBySuite[$Suite]
if (-not $packages -or -not $run) {
    throw "unknown stability suite: $Suite"
}

Push-Location $Root
try {
    if ($Shard) {
        if ($Shard -notmatch '^(\d+)/(\d+)$') {
            throw "-Shard must look like '1/3', got '$Shard'"
        }
        $index = [int]$Matches[1]
        $count = [int]$Matches[2]
        if ($count -lt 1 -or $index -lt 1 -or $index -gt $count) {
            throw "-Shard index must be between 1 and $count"
        }
        $names = @()
        foreach ($package in $packages) {
            $listed = & go test -tags stability -list $run $package 2>$null
            if ($LASTEXITCODE -ne 0) {
                throw "go test -list failed for $package"
            }
            $names += $listed | Where-Object { $_ -match '^Test' }
        }
        $names = $names | Sort-Object -Unique
        if ($names.Count -eq 0) {
            throw "no tests matched '$run' in $($packages -join ',')"
        }
        $selected = @()
        for ($i = 0; $i -lt $names.Count; $i++) {
            if (($i % $count) -eq ($index - 1)) {
                $selected += $names[$i]
            }
        }
        if ($selected.Count -eq 0) {
            throw "shard $index/$count selected no tests out of $($names.Count)"
        }
        $run = '^(' + (($selected | ForEach-Object { [regex]::Escape($_) }) -join '|') + ')$'
        Write-Host "shard ${index}/${count}: $($selected.Count) of $($names.Count) test(s)"
    }

    $timeout = if ($Suite -eq "soak" -or $Suite -eq "all") { "6h" } else { "2h" }
    # -p 1: scenarios assert process-level heap, RSS, handle counts and frame
    # throughput, so packages must not run next to each other.
    $arguments = @(
        "test", "-p", "1", "-tags", "stability"
    ) + $packages + @(
        "-count=1", "-v", "-timeout", $timeout, "-run", $run
    )
    if ($Race) {
        $arguments = @("-race") + $arguments
    }

    Write-Host "stability suite=$Suite duration=$Duration race=$Race shard=$Shard packages=$($packages -join ',')"
    $code = 0
    if ($Json) {
        New-Item -ItemType Directory -Force -Path $env:CODEX_STABILITY_REPORT_DIR | Out-Null
        $log = Join-Path $env:CODEX_STABILITY_REPORT_DIR ".tmp-stability-$(Get-Date -Format 'yyyyMMddTHHmmssfff').json.log"
        $arguments = $arguments + @("-json")
        & go @arguments 2>&1 | Tee-Object -FilePath $log | Out-Null
        $code = $LASTEXITCODE
        Write-Host "json log: $log"
        $results = @{}
        Get-Content $log | ForEach-Object {
            $line = $_.Trim()
            if (-not $line.StartsWith("{")) { return }
            try { $event = $line | ConvertFrom-Json } catch { return }
            $testProperty = $event.PSObject.Properties["Test"]
            $actionProperty = $event.PSObject.Properties["Action"]
            $packageProperty = $event.PSObject.Properties["Package"]
            if (-not $testProperty -or -not $actionProperty -or -not $packageProperty) { return }
            if ($testProperty.Value -and $actionProperty.Value -in @("pass", "fail", "skip")) {
                $results["$($packageProperty.Value)/$($testProperty.Value)"] = $actionProperty.Value
            }
        }
        $passed = @($results.Keys | Where-Object { $results[$_] -eq "pass" })
        $failed = @($results.Keys | Where-Object { $results[$_] -eq "fail" })
        $skipped = @($results.Keys | Where-Object { $results[$_] -eq "skip" })
        Write-Host "summary: pass=$($passed.Count) fail=$($failed.Count) skip=$($skipped.Count)"
        foreach ($name in ($failed | Sort-Object)) { Write-Host "FAIL $name" }
        foreach ($name in ($skipped | Sort-Object)) { Write-Host "SKIP $name" }
    } else {
        & go @arguments
        $code = $LASTEXITCODE
    }

    if ($code -eq 0 -and $Report) {
        $latest = Get-ChildItem $env:CODEX_STABILITY_REPORT_DIR -Filter "*.json" -ErrorAction SilentlyContinue |
            Sort-Object LastWriteTime -Descending |
            Select-Object -First 1
        if ($latest) {
            Write-Host "latest report: $($latest.FullName)"
        }
    }
    if ($code -eq 0 -and $Trend) {
        & go run ./stability/cmd/stability-trend -dir $env:CODEX_STABILITY_REPORT_DIR
        if ($LASTEXITCODE -ne 0) { $code = $LASTEXITCODE }
    }
    exit $code
} finally {
    Pop-Location
}
