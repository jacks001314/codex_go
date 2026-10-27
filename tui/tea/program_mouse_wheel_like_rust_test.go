package tea

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// lockedProgramOutput collects the bytes the real Bubble Tea program writes to
// its terminal while the test reads them from another goroutine.
type lockedProgramOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *lockedProgramOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *lockedProgramOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// waitForProgramOutput blocks until the program has written needle. Startup
// sequences are written before the first frame, so the wheel test can inject
// input as soon as the alternate screen and mouse reporting are live.
func waitForProgramOutput(t *testing.T, output *lockedProgramOutput, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), needle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; output=%q", needle, output.String())
}

// realProgram drives NewProgram with a pipe for input and a buffer for output so
// tests can observe the terminal sequences and feed terminal input.
type realProgram struct {
	input  *io.PipeWriter
	output *lockedProgramOutput
	quit   func()
	final  func(t *testing.T) *Model
}

func startRealProgram(t *testing.T, options Options) *realProgram {
	t.Helper()
	return startRealProgramWithState(t, tuiStateWithScrollback(), options)
}

// startRealProgramWithState drives NewProgram over a pipe so a test can feed
// real terminal escapes against a prepared transcript.
func startRealProgramWithState(t *testing.T, state *codextui.State, options Options) *realProgram {
	t.Helper()
	input, inputWriter := io.Pipe()
	output := &lockedProgramOutput{}
	program := NewProgram(context.Background(), state, options, input, output)
	models := make(chan bubbletea.Model, 1)
	errs := make(chan error, 1)
	go func() {
		model, err := program.StartReturningModel()
		models <- model
		errs <- err
	}()
	running := &realProgram{
		input:  inputWriter,
		output: output,
		quit: func() {
			program.Quit()
		},
	}
	running.final = func(t *testing.T) *Model {
		t.Helper()
		_ = inputWriter.Close()
		var model bubbletea.Model
		select {
		case model = <-models:
		case <-time.After(5 * time.Second):
			t.Fatal("program did not stop after Quit")
		}
		if err := <-errs; err != nil {
			t.Fatalf("program returned error: %v", err)
		}
		final, ok := model.(*Model)
		if !ok {
			t.Fatalf("program returned %T, want *Model", model)
		}
		return final
	}
	t.Cleanup(func() { _ = inputWriter.Close() })
	return running
}

// tuiStateWithScrollback builds a transcript taller than the viewport so the
// owned transcript starts scrolled to its bottom row.
func tuiStateWithScrollback() *codextui.State {
	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	return state
}

// Rust's owned transcript enters the alternate screen, enables cell-motion mouse
// reporting, and disables DEC 1007 before it accepts input. The wheel escape the
// terminal then sends must reach the transcript viewport instead of scrolling the
// terminal scrollback.
func TestOwnedProgramWheelReachesTranscriptLikeRust(t *testing.T) {
	running := startRealProgram(t, Options{Width: 60, Height: 10})
	waitForProgramOutput(t, running.output, "\x1b[?1049h")
	waitForProgramOutput(t, running.output, "\x1b[?1002h")
	waitForProgramOutput(t, running.output, "\x1b[?1007l")
	// Let the first frame lay out the transcript rows before hit-testing them.
	time.Sleep(100 * time.Millisecond)
	// SGR mouse press, button 64 (wheel up), at column 5 row 3.
	if _, err := running.input.Write([]byte("\x1b[<64;5;3M")); err != nil {
		t.Fatalf("write wheel escape: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	running.quit()
	model := running.final(t)
	if model.transcript.AtBottom() {
		t.Fatalf("wheel-up left the transcript at the bottom (offset %d); the terminal scrolled instead", model.TranscriptYOffset())
	}
}

// An inline launch keeps the terminal-owned transcript: no alternate screen, no
// mouse reporting, and no change to the terminal's alternate-scroll policy.
func TestInlineProgramLeavesWheelToTerminalLikeRust(t *testing.T) {
	running := startRealProgram(t, Options{Width: 60, Height: 10, NoAltScreen: true})
	waitForProgramOutput(t, running.output, "\x1b[?25l")
	running.quit()
	_ = running.final(t)
	text := running.output.String()
	for _, sequence := range []string{"\x1b[?1049h", "\x1b[?1002h", "\x1b[?1003h", "\x1b[?1007"} {
		if strings.Contains(text, sequence) {
			t.Fatalf("inline launch wrote %q; the wheel must stay with the terminal; output=%q", sequence, text)
		}
	}
}
