//go:build windows

package mcp

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

var procMCPGetConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// reportMCPHelperConsoleState mirrors the Windows MCP test server helper from
// Rust #48238: when asked, the helper records whether it was created with a
// console window, so the launcher test can prove the suppression reached the
// child rather than only its command line.
func reportMCPHelperConsoleState() {
	path := strings.TrimSpace(os.Getenv("MCP_TEST_CONSOLE_STATE_FILE"))
	if path == "" {
		return
	}
	handle, _, _ := procMCPGetConsoleWindow.Call()
	_ = os.WriteFile(path, []byte(strconv.FormatBool(handle != 0)), 0o600)
}
