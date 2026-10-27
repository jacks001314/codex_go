package tea

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
)

// TestReplayInputReaderServesProbeBytesFirst covers the startup replay wiring:
// bytes the terminal probe swallowed are read before the live terminal, and the
// wrapper still reports the wrapped descriptor so Bubble Tea keeps raw mode.
func TestReplayInputReaderServesProbeBytesFirst(t *testing.T) {
	input := newReplayInputReader([]byte("typed"), strings.NewReader("later"))
	if _, ok := input.(term.File); !ok {
		t.Fatal("replayed input must keep Bubble Tea's terminal File interface")
	}
	data, err := io.ReadAll(input)
	if err != nil {
		t.Fatalf("read replayed input: %v", err)
	}
	if string(data) != "typedlater" {
		t.Fatalf("replayed input = %q, want %q", data, "typedlater")
	}
}

func TestNewReplayInputReaderWithoutPendingBytesKeepsInput(t *testing.T) {
	input := strings.NewReader("later")
	if got := newReplayInputReader(nil, input); got != io.Reader(input) {
		t.Fatalf("empty replay wrapped the input as %T", got)
	}
}

func TestReplayInputReaderForwardedFd(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer file.Close()
	input := newReplayInputReader([]byte("typed"), file)
	fd := input.(interface{ Fd() uintptr }).Fd()
	if fd != file.Fd() {
		t.Fatalf("replayed input fd = %d, want the wrapped %d", fd, file.Fd())
	}
}
