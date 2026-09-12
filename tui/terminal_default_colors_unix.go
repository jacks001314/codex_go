//go:build !windows

package tui

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Rust parity: codex-rs/tui/src/terminal_probe.rs (Unix) default_colors. OSC 10
// and OSC 11 are sent together under one bounded deadline and read from a
// duplicated, non-blocking stdin handle so the TUI's own reader is untouched.
// Bytes consumed in this exclusive window are not replayed, matching Rust's
// helper contract (callers run it before input polling begins).
func probePlatformTerminalDefaultColors(timeout time.Duration) (DefaultColors, bool) {
	if timeout <= 0 {
		timeout = terminalColorProbeTimeout
	}
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return DefaultColors{}, false
	}
	fd, err := syscall.Dup(int(os.Stdin.Fd()))
	if err != nil {
		return DefaultColors{}, false
	}
	file := os.NewFile(uintptr(fd), "stdin-probe")
	if file == nil {
		_ = syscall.Close(fd)
		return DefaultColors{}, false
	}
	defer file.Close()
	if err := syscall.SetNonblock(fd, true); err != nil {
		return DefaultColors{}, false
	}
	defer syscall.SetNonblock(fd, false)
	if _, err := os.Stdout.WriteString(terminalColorProbeQuery); err != nil {
		return DefaultColors{}, false
	}
	deadline := time.Now().Add(timeout)
	buffer := make([]byte, 0, 128)
	chunk := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := file.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
			if colors, ok := ParseDefaultColors(buffer); ok {
				return colors, true
			}
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return DefaultColors{}, false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return DefaultColors{}, false
}
