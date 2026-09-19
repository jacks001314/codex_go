//go:build windows

package mcp

import "golang.org/x/sys/windows"

// mcpTestProcessAlive reports whether a process still exists, accepting that a
// pid may have been reused (STILL_ACTIVE is 259).
func mcpTestProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == 259
}
