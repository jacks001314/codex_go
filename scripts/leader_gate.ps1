<#
  leader_gate.ps1 - leader merge gate (round 86 protocol: lanes never commit; the leader verifies and commits)

  Usage:
    .\scripts\leader_gate.ps1 -Lane syncw1              # static gate only
    .\scripts\leader_gate.ps1 -Lane syncw1 -RunTests    # also run whole-package tests + parity
  Notes:
    - A lane delivers UNCOMMITTED worktree changes, so `git add -A -N` first.
    - Reverse control (RC) is manual and only reminded here.
#>
param(
  [Parameter(Mandatory=$true)][string]$Lane,
  [string]$Repo = "D:\qax\reagent\dev\codex_go",
  [string]$RustRoot = "D:\qax\reagent\dev\git\codex\codex-rs",
  [switch]$RunTests
)
$ErrorActionPreference = "Continue"
$wt = Join-Path (Split-Path $Repo -Parent) ("codex_go_wt\" + $Lane)
if (-not (Test-Path -LiteralPath $wt)) { Write-Host "FATAL: worktree not found: $wt"; exit 2 }
Write-Host ("=== lane=" + $Lane + " wt=" + $wt + " ===")
Push-Location $wt
git add -A -N 2>$null
Write-Host ""
Write-Host "### git status --porcelain"; git status --porcelain
Write-Host ""
Write-Host "### git diff --stat"; git diff --stat
$files = @(git diff --name-only)
$go = @($files | Where-Object { $_ -like '*.go' })
Write-Host ""
Write-Host ("### changed files: " + $files.Count + " (go: " + $go.Count + ")")
if ($files.Count -eq 0) { Write-Host "NO CHANGES -> nothing to gate"; Pop-Location; exit 0 }
Write-Host ""
Write-Host "### gofmt -l (expect empty)"
if ($go.Count) { gofmt -l $go } else { Write-Host "(no go files)" }
Write-Host ""
Write-Host "### go build ./..."
go build ./...
Write-Host ("build exit=" + $LASTEXITCODE)
$pkgs = @($go | ForEach-Object { "./" + ((Split-Path $_ -Parent) -replace '\\','/') + "/" } | Sort-Object -Unique)
Write-Host ""
Write-Host "### affected packages"
$pkgs | ForEach-Object { Write-Host ("  " + $_) }
Write-Host ""
Write-Host "### go vet (affected packages)"
if ($pkgs.Count) { go vet @pkgs; Write-Host ("vet exit=" + $LASTEXITCODE) }
if ($RunTests) {
  $env:CODEX_RUST_ROOT = $RustRoot
  Write-Host ""
  Write-Host "### go test (affected packages, count=1)"
  if ($pkgs.Count) { go test @pkgs -count=1 }
  Write-Host ""
  Write-Host "### parity"
  go test ./parity/ -count=1
}
Write-Host ""
Write-Host "[RC] manual step: back up file, python-assert-remove the fix (assert count==1), expect the target test to FAIL at value/behaviour level, restore, expect ok."
Write-Host "[COMMIT] leader only: git -C <wt> add -A ; git -C <wt> commit -m 'syncNNN: <summary> (#PR)' ; then cherry-pick into main."
Pop-Location