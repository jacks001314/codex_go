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

$package = Get-AppxPackage -Name OpenAI.Codex -ErrorAction SilentlyContinue
if ($null -eq $package) {
    Write-Error 'Desktop app package is not installed'
    exit 1
}

$manifest = Get-AppxPackageManifest -Package $package.PackageFullName
$application = $manifest.Package.Applications.Application |
    Where-Object {
        @($_.Extensions.Extension) | Where-Object {
            $_.Category -eq 'windows.protocol' -and $_.Protocol.Name -eq 'codex'
        }
    } |
    Select-Object -First 1
if ($null -eq $application -or [string]::IsNullOrWhiteSpace($application.Executable)) {
    Write-Error 'Desktop app package does not declare a codex protocol executable'
    exit 1
}

# Launch the package-declared protocol executable rather than an internal Electron shim.
# Windows can deny direct starts of internal executables under WindowsApps.
$exe = Join-Path $package.InstallLocation $application.Executable
$appDir = Split-Path -Parent $exe
$app = Join-Path $appDir 'resources\app.asar'
if (-not (Test-Path $exe)) {
    Write-Error "Desktop app executable not found at $exe"
    exit 1
}
if (-not (Test-Path $app)) {
    Write-Error "Desktop app bundle not found at $app"
    exit 1
}

Start-Process -FilePath $exe -WorkingDirectory $appDir -ArgumentList @('resources\app.asar', $url)
`
}
