package app

import "strings"

// Rust parity subset: codex-rs/tui/src/app/history_ui.rs.

const DesktopThreadOpenedMessage = "Opened this session in the Desktop app."

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
	return "Failed to open this session in the Desktop app: " + err + ". Install or launch the Desktop app and try again."
}

func PowershellSingleQuotedString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func WindowsDesktopAppLaunchScript(url string) string {
	quotedURL := PowershellSingleQuotedString(url)
	return `
try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}
$ErrorActionPreference = 'Stop'
$url = ` + quotedURL + `

$package = Get-AppxPackage -Name OpenAI.Codex -ErrorAction SilentlyContinue |
    Where-Object { $_.PublisherId -in @('2p2nqsd0c76g0', '3k8sg7r9htsxt') } |
    Select-Object -First 1
if ($null -eq $package) {
    Write-Error 'Desktop app package is not installed'
    exit 1
}

Start-Process -FilePath $url
`
}
