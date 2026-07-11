package app

import "strings"

// Rust parity subset: codex-rs/tui/src/app/history_ui.rs.

const DesktopThreadOpenedMessage = "Opened this session in Codex Desktop."

type HistoryUIState struct {
	Rows []string
}

func (s *HistoryUIState) InsertRows(rows []string) {
	if s == nil || len(rows) == 0 {
		return
	}
	s.Rows = append(s.Rows, rows...)
}

func (s *HistoryUIState) Clear() {
	if s == nil {
		return
	}
	s.Rows = nil
}

func DesktopThreadURL(threadID string) string {
	return "codex://threads/" + threadID
}

func DesktopThreadOpenErrorMessage(err string) string {
	return "Failed to open this session in Codex Desktop: " + err + ". Install or launch Codex Desktop and try again."
}

func PowershellSingleQuotedString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func WindowsDesktopAppLaunchScript(url string) string {
	quotedURL := PowershellSingleQuotedString(url)
	return `
$ErrorActionPreference = 'Stop'
$url = ` + quotedURL + `

$installLocation = (Get-AppxPackage -Name OpenAI.Codex -ErrorAction SilentlyContinue).InstallLocation
if ([string]::IsNullOrWhiteSpace($installLocation)) {
    Write-Error 'Codex Desktop package is not installed'
    exit 1
}

$appDir = Join-Path $installLocation 'app'
$exe = Join-Path $appDir 'Codex.exe'
$app = Join-Path $appDir 'resources\app.asar'
if (-not (Test-Path $exe)) {
    Write-Error "Codex Desktop executable not found at $exe"
    exit 1
}
if (-not (Test-Path $app)) {
    Write-Error "Codex Desktop app bundle not found at $app"
    exit 1
}

Start-Process -FilePath $exe -WorkingDirectory $appDir -ArgumentList @('resources\app.asar', $url)
`
}
