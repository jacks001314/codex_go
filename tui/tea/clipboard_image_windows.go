//go:build windows

package tea

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codex_go/envutil"
)

// pasteImageFromClipboardPlatform mirrors Rust's Windows path: read the system
// clipboard image and save it as PNG (codex-rs/tui/src/clipboard_paste.rs,
// arboard via the Windows clipboard).
func pasteImageFromClipboardPlatform() (string, error) {
	return pasteImageFromClipboardWindows()
}

func pasteImageFromClipboardWindows() (string, error) {
	dir, err := os.MkdirTemp("", "codex-paste-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "clipboard.png")
	script := "$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.Windows.Forms; $img=[Windows.Forms.Clipboard]::GetImage(); if ($null -eq $img) { exit 2 }; $img.Save('" + strings.ReplaceAll(path, "'", "''") + "', [Drawing.Imaging.ImageFormat]::Png)"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Sta", "-Command", script)
	// Rust #48483: the clipboard helper is piped, so it must not allocate a
	// console window when the TUI itself has no console.
	envutil.SuppressConsoleWindow(cmd)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("%v: %s", runErr, strings.TrimSpace(string(output)))
	}
	return path, nil
}
