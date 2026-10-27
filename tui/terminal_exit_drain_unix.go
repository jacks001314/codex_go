//go:build !windows

package tui

import (
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/charmbracelet/x/term"
)

// DrainPendingTerminalReports discards terminal reports that were generated
// while the TUI's mouse/focus reporting was still enabled, mirroring Rust's
// `restore_after_exit_with_events` (#57391). It runs while the process still
// owns stdin and never returns an error.
//
// waitForDA1 is false during panic unwinding, where Rust flushes queued input
// without waiting for a reply.
func DrainPendingTerminalReports(input io.Reader, output io.Writer, waitForDA1 bool) bool {
	inputFile, ok := input.(*os.File)
	if !ok || inputFile == nil {
		return false
	}
	info, err := inputFile.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fd, err := syscall.Dup(int(inputFile.Fd()))
	if err != nil {
		return false
	}
	probe := os.NewFile(uintptr(fd), "stdin-exit-drain")
	if probe == nil {
		_ = syscall.Close(fd)
		return false
	}
	defer func() { _ = probe.Close() }()
	// Rust drains before `disable_raw_mode`: a canonical terminal withholds
	// unterminated report bytes, so the queue is unreadable in cooked mode.
	if state, err := term.MakeRaw(inputFile.Fd()); err == nil {
		defer func() { _ = term.Restore(inputFile.Fd(), state) }()
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return false
	}
	defer func() { _ = syscall.SetNonblock(fd, false) }()

	drained := false
	if waitForDA1 && TerminalDA1Supported() && isTerminalWriter(output) {
		drained = readTerminalReportsUntilDA1(probe, output, ExitReportDrainTimeout)
	}
	flushTerminalInput(probe)
	return drained
}

// flushTerminalInput discards everything still queued, mirroring Rust's
// `tcflush(STDIN_FILENO, TCIFLUSH)`.
func flushTerminalInput(probe *os.File) {
	chunk := make([]byte, 512)
	for {
		n, err := probe.Read(chunk)
		if n > 0 {
			continue
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return
		}
		return
	}
}

// isTerminalWriter reports whether output is the terminal the DA1 query can be
// written to. A redirected stream keeps the queued reports but cannot answer.
func isTerminalWriter(output io.Writer) bool {
	outputFile, ok := output.(*os.File)
	if !ok || outputFile == nil {
		return false
	}
	info, err := outputFile.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
