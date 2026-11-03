package tea

import (
	"io"

	"github.com/charmbracelet/x/term"
)

// replayInputReader serves the terminal bytes that the startup probe consumed
// before reading the TUI's real input, mirroring Rust's
// `crossterm::event::buffer_input(startup_replay_input(&buffer))`
// (codex-rs/tui/src/terminal_probe.rs). It keeps Bubble Tea's terminal setup
// intact by delegating Fd to the wrapped file, so raw mode and resize handling
// still target the real stdin.
type replayInputReader struct {
	pending []byte
	input   io.Reader
}

var _ term.File = (*replayInputReader)(nil)

// newReplayInputReader returns input unchanged when the probe captured nothing.
func newReplayInputReader(pending []byte, input io.Reader) io.Reader {
	if len(pending) == 0 {
		return input
	}
	return &replayInputReader{pending: pending, input: input}
}

// StartupReplayInput wraps input so bytes consumed by the startup probe are
// replayed first. Exported so launchers and tests can build the same reader the
// probe produces.
func StartupReplayInput(pending []byte, input io.Reader) io.Reader {
	return newReplayInputReader(pending, input)
}

// UnderlyingInput returns the terminal handle a startup-probe replay wrapper
// delegates to. Launch-time terminal checks must keep seeing the real handle:
// Rust decides whether to start the TUI from the raw `stdin.is_terminal()`
// before it installs the replayed input buffer, so a wrapper must not change
// that verdict.
func UnderlyingInput(input io.Reader) io.Reader {
	for {
		replayed, ok := input.(*replayInputReader)
		if !ok {
			return input
		}
		input = replayed.input
	}
}

func (r *replayInputReader) Read(p []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.input == nil {
		return 0, io.EOF
	}
	return r.input.Read(p)
}

// Write forwards to the wrapped file; the TUI never writes to its input handle,
// so a non-writable input discards the bytes.
func (r *replayInputReader) Write(p []byte) (int, error) {
	if writer, ok := r.input.(io.Writer); ok {
		return writer.Write(p)
	}
	return len(p), nil
}

// Close intentionally leaves the caller-owned terminal open. Bubble Tea only
// needs this method so the wrapper preserves the terminal File interface.
func (r *replayInputReader) Close() error {
	return nil
}

func (r *replayInputReader) Fd() uintptr {
	if file, ok := r.input.(interface{ Fd() uintptr }); ok {
		return file.Fd()
	}
	return ^uintptr(0)
}
