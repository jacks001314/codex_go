package app

import (
	"os"
	"strings"
	"testing"

	codextea "codex_go/tui/tea"
)

// Regression for a real-terminal launch bug: runInteractiveEntry probes the
// terminal first and hands the (possibly wrapped) reader to runInteractive, so
// the `--remote` branch rejected a real terminal with
// "interactive remote app-server TUI requires a real terminal" whenever the
// probe captured leftover bytes. Rust checks the raw handle instead.
func TestShouldRunInteractiveTUIUnwrapsStartupReplayLikeRust(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	replayed := codextea.StartupReplayInput([]byte("\x1b[1;1R"), file)
	if !shouldRunInteractiveTUI(replayed, file) {
		t.Fatal("a replayed terminal input was not recognised as a real terminal")
	}
	if shouldRunInteractiveTUI(file, &strings.Builder{}) {
		t.Fatal("a non-terminal output was accepted")
	}
	if shouldRunInteractiveTUI(codextea.StartupReplayInput([]byte("x"), strings.NewReader("")), file) {
		t.Fatal("a replay wrapper around a pipe was accepted as a terminal")
	}
}
