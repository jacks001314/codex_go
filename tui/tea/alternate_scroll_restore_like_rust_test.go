package tea

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

type failOnceScrollWriter struct {
	buf      bytes.Buffer
	failNext bool
}

func (w *failOnceScrollWriter) Write(p []byte) (int, error) {
	if w.failNext {
		w.failNext = false
		return 0, errors.New("write failed")
	}
	return w.buf.Write(p)
}

func alternateScrollTestModel(t *testing.T, initial *bool) (*Model, *bytes.Buffer) {
	t.Helper()
	restore := codextui.SetInitialAlternateScrollForTest(initial)
	t.Cleanup(restore)
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	var output bytes.Buffer
	model.petRuntime = newPetRuntime(&output, nil)
	return model, &output
}

// Rust #61697: entering the owned screen disables 1007, and exit restores the
// mode recorded by the startup probe (disabled when it is unknown).
func TestAlternateScrollRestoresStartupModeOnExitLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		initial *bool
		want    string
	}{
		{name: "initially enabled", initial: boolPtrTea(true), want: "\x1b[?1007h"},
		{name: "initially disabled", initial: boolPtrTea(false), want: "\x1b[?1007l"},
		{name: "unknown defaults disabled", initial: nil, want: "\x1b[?1007l"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			model, output := alternateScrollTestModel(t, testCase.initial)
			model.Init()
			if !strings.Contains(output.String(), "\x1b[?1007l") {
				t.Fatalf("owned screen did not disable 1007: %q", output.String())
			}
			output.Reset()
			if !model.restoreAlternateScroll() {
				t.Fatal("restore failed")
			}
			if got := output.String(); got != testCase.want {
				t.Fatalf("restore = %q, want %q", got, testCase.want)
			}
			output.Reset()
			if !model.restoreAlternateScroll() || output.Len() != 0 {
				t.Fatalf("clean restore wrote again: %q", output.String())
			}
		})
	}
}

// Rust releases the alternate-scroll mode before handing the terminal to an
// editor and reapplies the owned-screen policy when the editor returns.
func TestExternalEditorHandoffRestoresAlternateScrollLikeRust(t *testing.T) {
	model, output := alternateScrollTestModel(t, boolPtrTea(true))
	model.onExternalEditor = func(seed string) bubbletea.Cmd {
		return func() bubbletea.Msg { return ExternalEditorFinishedMsg{Text: seed + "edited"} }
	}
	model.Init()
	output.Reset()

	cmd := model.openExternalEditor()
	if cmd == nil {
		t.Fatal("editor command was not created")
	}
	if !strings.Contains(output.String(), "\x1b[?1007h") {
		t.Fatalf("handoff did not restore the initial mode: %q", output.String())
	}
	model.applyExternalEditorFinished(ExternalEditorFinishedMsg{Text: "edited"})
	if !strings.Contains(output.String(), "\x1b[?1007l") {
		t.Fatalf("editor return did not reapply the owned-screen mode: %q", output.String())
	}
}

// Capture-policy changes follow Rust's AlternateScreen::configure_input: when a
// surface releases the mouse, alternate scroll is enabled again.
func TestMouseCapturePolicyReconfiguresAlternateScrollLikeRust(t *testing.T) {
	model, output := alternateScrollTestModel(t, boolPtrTea(true))
	model.Init()
	output.Reset()

	model.overlay = chatwidget.NewTranscriptOverlay(80, 24, "content")
	model.overlayMouseCapture = mouseCaptureDisabled
	model.mouseCaptureActive = true
	model.syncMouseCapture()
	if !strings.Contains(output.String(), "\x1b[?1007h") {
		t.Fatalf("releasing capture did not enable alternate scroll: %q", output.String())
	}
	output.Reset()
	model.overlay = nil
	model.overlayMouseCapture = mouseCaptureInherit
	model.syncMouseCapture()
	if !strings.Contains(output.String(), "\x1b[?1007l") {
		t.Fatalf("reacquiring capture did not disable alternate scroll: %q", output.String())
	}
}

// A failed cleanup write leaves the scroll state dirty so the exit restore
// retries it (Rust #61697 cleanup_write_failure test).
func TestAlternateScrollRestoreRetriesFailedCleanupLikeRust(t *testing.T) {
	restore := codextui.SetInitialAlternateScrollForTest(boolPtrTea(true))
	t.Cleanup(restore)
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	writer := &failOnceScrollWriter{failNext: true}
	model.petRuntime = newPetRuntime(writer, nil)

	model.configureAlternateScroll(false)
	if !model.alternateScroll.dirty {
		t.Fatal("failed configure cleared the dirty state")
	}
	if writer.buf.Len() != 0 {
		t.Fatalf("failed configure wrote bytes: %q", writer.buf.String())
	}
	if !model.restoreAlternateScroll() {
		t.Fatal("retry did not succeed")
	}
	if got := writer.buf.String(); got != "\x1b[?1007h" {
		t.Fatalf("retry wrote %q, want the initial enabled mode", got)
	}
	if model.alternateScroll.dirty {
		t.Fatal("successful retry left the dirty state set")
	}
}

// Run must restore the startup mode after Bubble Tea releases the alternate
// screen; an initially enabled terminal gets 1007 turned back on at exit.
func TestRunRestoresAlternateScrollAtExitLikeRust(t *testing.T) {
	restore := codextui.SetInitialAlternateScrollForTest(boolPtrTea(true))
	t.Cleanup(restore)
	input, inputWriter := io.Pipe()
	defer func() { _ = inputWriter.Close() }()
	output := &lockedProgramOutput{}
	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), codextui.NewState(nil), Options{Width: 80, Height: 24}, input, output)
		done <- err
	}()
	waitForProgramOutput(t, output, "\x1b[?1049h")
	waitForProgramOutput(t, output, "\x1b[?1007l")
	if _, err := inputWriter.Write([]byte{0x03}); err != nil {
		t.Fatalf("send Ctrl+C: %v", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit")
	}
	if text := output.String(); !strings.Contains(text, "\x1b[?1007h") {
		t.Fatalf("exit did not restore the initial alternate-scroll mode: %q", text)
	}
}
