//go:build !windows

package tui

import (
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
)

// terminalStartupProbeQuery adds the primary device attributes (DA1) request
// ahead of the OSC 10/11 colors. Rust's startup probe asks for DA1 too: its
// reply finishes keyboard detection and records that the terminal can be asked
// to drain outstanding reports at exit (#57391).
const terminalStartupProbeQuery = "\x1b[c" + terminalColorProbeQuery

// Rust parity: codex-rs/tui/src/terminal_probe.rs (Unix) startup probe. The
// queries are sent together under one bounded deadline and read from a
// duplicated, non-blocking stdin handle so the TUI's own reader is untouched.
// Bytes consumed in this exclusive window are not replayed, matching Rust's
// helper contract (callers run it before input polling begins). Rust enables raw
// mode before probing (`set_modes`), because a canonical terminal withholds the
// unterminated report bytes; the probe restores the previous mode before it
// returns.
func probePlatformTerminalDefaultColors(timeout time.Duration) (DefaultColors, bool) {
	if timeout <= 0 {
		timeout = terminalColorProbeTimeout
	}
	setTerminalDA1Supported(false)
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
	if state, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
		defer func() { _ = term.Restore(os.Stdin.Fd(), state) }()
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return DefaultColors{}, false
	}
	defer syscall.SetNonblock(fd, false)
	if _, err := os.Stdout.WriteString(terminalStartupProbeQuery); err != nil {
		return DefaultColors{}, false
	}
	deadline := time.Now().Add(timeout)
	buffer := make([]byte, 0, 128)
	chunk := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := file.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
			if FindPrimaryDeviceAttributes(buffer) {
				setTerminalDA1Supported(true)
			}
			if colors, ok := ParseDefaultColors(buffer); ok {
				// Pick up a DA1 reply that arrived right behind the colors so it
				// neither leaks into the input loop nor goes unrecorded.
				if n, _ := file.Read(chunk); n > 0 {
					buffer = append(buffer, chunk[:n]...)
					if FindPrimaryDeviceAttributes(buffer) {
						setTerminalDA1Supported(true)
					}
				}
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
