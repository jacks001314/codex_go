//go:build !windows

package tui

import (
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
)

// terminalStartupProbeQuery queries alternate-scroll mode before DA1 so the
// mode report acts as a barrier even when 1007 is unsupported (Rust #61697). It
// then asks for DA1 ahead of the OSC 10/11 colors: the DA1 reply finishes
// keyboard detection and records that the terminal can be asked to drain
// outstanding reports at exit (#57391). For an SSH session outside a
// multiplexer the secondary query (DA2) is appended so the reply pair can
// identify Apple Terminal (#47417).
func terminalStartupProbeQuery(queryIdentity bool, kittyGraphics string) string {
	query := "\x1b[?1007$p"
	// Rust sends the Kitty graphics query before DA1 so the DA1 reply acts as a
	// barrier even when the terminal ignores the query.
	query += kittyGraphics
	query += "\x1b[c" + terminalColorProbeQuery
	if queryIdentity {
		query += "\x1b[>c"
	}
	return query
}

// Rust parity: codex-rs/tui/src/terminal_probe.rs (Unix) startup probe. The
// queries are sent together under one bounded deadline and read from a
// duplicated, non-blocking stdin handle so the TUI's own reader is untouched.
// Bytes consumed in this exclusive window are not replayed, matching Rust's
// helper contract (callers run it before input polling begins). Rust enables raw
// mode before probing (`set_modes`), because a canonical terminal withholds the
// unterminated report bytes; the probe restores the previous mode before it
// returns.
func probePlatformTerminalDefaultColors(timeout time.Duration) (DefaultColors, []byte, bool) {
	timeout = defaultTerminalColorProbeTimeout(timeout)
	queryIdentity := StartupProbeQueryTerminalIdentity()
	SetTerminalAppOverSSHFromStartupProbe(nil)
	SetAlternateScrollFromStartupProbe(nil)
	if !queryIdentity {
		SetTerminalAppOverSSHFromStartupProbe(boolPtrTerminalProbe(false))
	}
	setTerminalDA1Supported(false)
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return DefaultColors{}, nil, false
	}
	fd, err := syscall.Dup(int(os.Stdin.Fd()))
	if err != nil {
		return DefaultColors{}, nil, false
	}
	file := os.NewFile(uintptr(fd), "stdin-probe")
	if file == nil {
		_ = syscall.Close(fd)
		return DefaultColors{}, nil, false
	}
	defer file.Close()
	if state, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
		defer func() { _ = term.Restore(os.Stdin.Fd(), state) }()
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return DefaultColors{}, nil, false
	}
	defer syscall.SetNonblock(fd, false)
	kittyGraphics := ProbedKittyGraphicsQuery()
	SetProbedKittyGraphics(nil)
	if _, err := os.Stdout.WriteString(terminalStartupProbeQuery(queryIdentity, kittyGraphics)); err != nil {
		return DefaultColors{}, nil, false
	}
	settleKittyGraphics := func(buffer []byte) {
		if kittyGraphics == "" || buffer == nil {
			return
		}
		if _, known := ProbedKittyGraphics(); known {
			return
		}
		if supported, found := FindKittyGraphicsReport(buffer); found {
			SetProbedKittyGraphics(&supported)
		}
	}
	deadline := time.Now().Add(timeout)
	buffer := make([]byte, 0, 128)
	chunk := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := file.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
			if _, known := InitialAlternateScrollKnown(); !known {
				if enabled, ok := FindAlternateScrollReport(buffer); ok {
					SetAlternateScrollFromStartupProbe(&enabled)
				}
			}
			if FindPrimaryDeviceAttributes(buffer) {
				setTerminalDA1Supported(true)
			}
			settleKittyGraphics(buffer)
			// Rust #63191 keeps a background-only reply and spends the rest of the
			// window waiting for the foreground.
			if colors, ok := ParseDefaultColors(buffer); ok && !colors.ForegroundMissing {
				// Pick up a DA1 reply that arrived right behind the colors so it
				// neither leaks into the input loop nor goes unrecorded.
				if n, _ := file.Read(chunk); n > 0 {
					buffer = append(buffer, chunk[:n]...)
					if FindPrimaryDeviceAttributes(buffer) {
						setTerminalDA1Supported(true)
					}
				}
				if overSSH, known := ParseTerminalAppOverSSH(buffer); known {
					SetTerminalAppOverSSHFromStartupProbe(&overSSH)
				}
				settleKittyGraphics(buffer)
				if !queryIdentity {
					return colors, StartupReplayInput(buffer), true
				}
				if _, known := TerminalAppOverSSHKnown(); known {
					return colors, StartupReplayInput(buffer), true
				}
			}
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return DefaultColors{}, StartupReplayInput(buffer), false
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A query that went unanswered is a rejection: the terminal never produced a
	// Kitty graphics reply inside the probe window.
	if kittyGraphics != "" {
		if _, known := ProbedKittyGraphics(); !known {
			rejected := false
			SetProbedKittyGraphics(&rejected)
		}
	}
	if colors, ok := ParseDefaultColors(buffer); ok {
		// Rust returns the probe with an unsettled identity; callers treat an
		// unknown terminal as not being Apple Terminal over SSH.
		return colors, StartupReplayInput(buffer), true
	}
	return DefaultColors{}, StartupReplayInput(buffer), false
}
