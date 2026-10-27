package tui

import (
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// Rust parity: codex-rs/tui/src/terminal_probe.rs's `wait_for_exit_reports` and
// codex-rs/tui/src/tui.rs's `flush_terminal_input_buffer` (#57391).
//
// Reports still in flight when the TUI exits can leak into the parent shell's
// input. Disabling mouse/focus reporting stops new reports, but the terminal may
// already have generated some for gestures it was still processing. A primary
// device attributes (DA1) reply follows every earlier byte, so waiting for it
// proves the reports arrived; the input queue is then drained before stdin is
// handed back to the shell.

// ExitReportDrainTimeout mirrors Rust's two-second DA1 deadline.
const ExitReportDrainTimeout = 2 * time.Second

// primaryDeviceAttributesQuery is Rust's DA1 request (b"\x1b[c").
const primaryDeviceAttributesQuery = "\x1b[c"

// maxTerminalReportRetainedBytes keeps the split-reply window from Rust's probe:
// a DA1 prefix plus the longest terminal CSI it accepts.
const maxTerminalReportRetainedBytes = len("\x1b[?") + 64

var terminalDA1Supported atomic.Bool

// TerminalDA1Supported reports whether the startup probe saw a DA1 reply, like
// Rust's `DA1_SUPPORTED` static. Terminals that never answer DA1 are never made
// to wait at exit.
func TerminalDA1Supported() bool { return terminalDA1Supported.Load() }

func setTerminalDA1Supported(supported bool) { terminalDA1Supported.Store(supported) }

// readTerminalReportsUntilDA1 writes the DA1 query and discards input until the
// reply arrives or the deadline expires. The reader must be the non-blocking
// duplicate of the terminal the platform wrapper opens, so an empty queue
// reports a temporary error instead of blocking the shutdown.
func readTerminalReportsUntilDA1(reader io.Reader, writer io.Writer, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = ExitReportDrainTimeout
	}
	if _, err := io.WriteString(writer, primaryDeviceAttributesQuery); err != nil {
		return false
	}
	buffer := make([]byte, 0, 256)
	chunk := make([]byte, 256)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n, err := reader.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
			if FindPrimaryDeviceAttributes(buffer) {
				return true
			}
			if len(buffer) > maxTerminalReportRetainedBytes {
				buffer = append([]byte(nil), buffer[len(buffer)-maxTerminalReportRetainedBytes:]...)
			}
		}
		if errors.Is(err, io.EOF) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}
