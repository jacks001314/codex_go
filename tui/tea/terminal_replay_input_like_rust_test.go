package tea

import (
	"os"
	"strings"
	"testing"
)

// Rust decides whether to run the TUI from the raw `stdin.is_terminal()` value
// before it installs the replayed startup buffer
// (codex-rs/tui/src/terminal_probe.rs). A wrapper must therefore keep reporting
// the underlying terminal handle.
func TestUnderlyingInputUnwrapsStartupReplayLikeRust(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if got := UnderlyingInput(file); got != file {
		t.Fatalf("plain input changed: %T", got)
	}
	wrapped := StartupReplayInput([]byte("typed-before-first-frame"), file)
	if _, ok := wrapped.(*replayInputReader); !ok {
		t.Fatalf("StartupReplayInput returned %T, want the replay wrapper", wrapped)
	}
	if got := UnderlyingInput(wrapped); got != file {
		t.Fatalf("UnderlyingInput = %T, want the wrapped *os.File", got)
	}
	// Nested wrappers (a launcher probing twice) still resolve to the terminal.
	if got := UnderlyingInput(StartupReplayInput([]byte("x"), wrapped)); got != file {
		t.Fatalf("nested UnderlyingInput = %T, want the wrapped *os.File", got)
	}
	// A replay wrapper around a non-terminal stays a non-terminal.
	pipe := StartupReplayInput([]byte("x"), strings.NewReader(""))
	if got := UnderlyingInput(pipe); got == file {
		t.Fatalf("UnderlyingInput invented a terminal handle")
	}
}
