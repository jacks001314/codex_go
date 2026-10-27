//go:build windows

package tui

import (
	"io"
	"os"
	"syscall"
)

var windowsFlushConsoleInputBuffer = syscall.NewLazyDLL("kernel32.dll").NewProc("FlushConsoleInputBuffer")

// DrainPendingTerminalReports mirrors Rust's Windows arm of #57391: the
// platform has no DA1 probe, so flushing the console input queue before the
// shell starts is the whole drain.
func DrainPendingTerminalReports(input io.Reader, output io.Writer, waitForDA1 bool) bool {
	inputFile, ok := input.(*os.File)
	if !ok || inputFile == nil {
		return false
	}
	return flushWindowsConsoleInput(inputFile.Fd())
}

// flushWindowsConsoleInput discards every record queued on the stdin console.
// Handles that are not consoles (pipes, files, the test harness) fail the call
// and report false.
func flushWindowsConsoleInput(fd uintptr) bool {
	ret, _, _ := windowsFlushConsoleInputBuffer.Call(fd)
	return ret != 0
}
