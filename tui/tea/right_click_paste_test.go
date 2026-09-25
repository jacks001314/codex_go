package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	tuiinternal "codex_go/tui/tui"
)

func rightClickPasteTestModel(t *testing.T, mode string, read func() (string, error)) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{
		Width:           80,
		Height:          24,
		RightClickPaste: mode,
		OnClipboardRead: read,
	})
	model.rightClickPasteEnv = tuiapp.PasteEnvironment{PlatformDefault: true, Vscode: tuiinternal.VscodeDetectionOther}
	model.State.SetThreadID("thread-1")
	return model
}

func rightButtonPress() bubbletea.MouseMsg {
	return bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonRight}
}

// Mirrors Rust #48118: the fullscreen right click reads the clipboard off the
// update loop and the text is delivered through the normal composer paste path
// at the next render.
func TestRightClickPasteDeliversClipboardTextAtRender(t *testing.T) {
	model := rightClickPasteTestModel(t, "auto", func() (string, error) { return "hello world", nil })

	_, cmd := model.Update(rightButtonPress())
	if cmd == nil {
		t.Fatal("an eligible right click should start a clipboard read")
	}
	if model.rightClickPasteTarget == nil || !model.clipboardReadPending {
		t.Fatalf("pending state = %#v busy=%v", model.rightClickPasteTarget, model.clipboardReadPending)
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer changed before the read delivered: %q", got)
	}

	if _, _ = model.Update(cmd()); model.rightClickPasteResult == nil {
		t.Fatal("the worker result should be stored until the next render")
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("the result must not be delivered before a render: %q", got)
	}
	model.View()
	if got := model.composer.Value(); got != "hello world" {
		t.Fatalf("composer after render = %q, want %q", got, "hello world")
	}
	if model.rightClickPasteTarget != nil || model.rightClickPasteResult != nil || model.clipboardReadPending {
		t.Fatal("delivery should clear the pending read")
	}
}

func TestRightClickPasteInvalidatedByInterveningInputAndDraftChanges(t *testing.T) {
	// Intervening input before the read completes discards it.
	model := rightClickPasteTestModel(t, "auto", func() (string, error) { return "hello", nil })
	_, cmd := model.Update(rightButtonPress())
	if cmd == nil {
		t.Fatal("expected a read to start")
	}
	result := cmd()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("x")})
	model.Update(result)
	model.View()
	if got := model.composer.Value(); got != "x" {
		t.Fatalf("composer after invalidated paste = %q, want %q", got, "x")
	}

	// A changed draft/cursor between the press and the render drops the read.
	model = rightClickPasteTestModel(t, "auto", func() (string, error) { return "hello", nil })
	_, cmd = model.Update(rightButtonPress())
	if cmd == nil {
		t.Fatal("expected a read to start")
	}
	model.Update(cmd())
	model.composer.SetValue("typed")
	model.View()
	if got := model.composer.Value(); got != "typed" {
		t.Fatalf("stale-target paste = %q, want %q", got, "typed")
	}
}

func TestRightClickPasteHonorsModeAndTerminalPolicy(t *testing.T) {
	blocked := []struct {
		name   string
		mode   string
		mutate func(*Model)
	}{
		{"mode off", "off", func(*Model) {}},
		{"ssh session", "auto", func(m *Model) {
			m.rightClickPasteEnv = tuiapp.PasteEnvironment{PlatformDefault: true, SSH: true, Vscode: tuiinternal.VscodeDetectionOther}
		}},
		{"vs code terminal", "auto", func(m *Model) {
			m.rightClickPasteEnv = tuiapp.PasteEnvironment{PlatformDefault: true, Vscode: tuiinternal.VscodeDetectionVsCode}
		}},
		{"inconclusive wsl", "auto", func(m *Model) {
			m.rightClickPasteEnv = tuiapp.PasteEnvironment{PlatformDefault: true, WSL: true, Vscode: tuiinternal.VscodeDetectionUnknown}
		}},
		{"vim search", "auto", func(m *Model) { m.vimSearchMode = true }},
		{"inline screen", "auto", func(m *Model) { m.noAltScreen = true }},
	}
	for _, tc := range blocked {
		model := rightClickPasteTestModel(t, tc.mode, func() (string, error) { return "hello", nil })
		tc.mutate(model)
		if _, cmd := model.Update(rightButtonPress()); cmd != nil {
			t.Fatalf("%s should not start a clipboard read", tc.name)
		}
		if model.clipboardReadPending {
			t.Fatalf("%s left the worker busy", tc.name)
		}
	}

	// `on` also enables macOS, which `auto` leaves to the terminal.
	model := rightClickPasteTestModel(t, "on", func() (string, error) { return "hello", nil })
	model.rightClickPasteEnv = tuiapp.PasteEnvironment{PlatformDefault: false, Vscode: tuiinternal.VscodeDetectionOther}
	if _, cmd := model.Update(rightButtonPress()); cmd == nil {
		t.Fatal("`on` should enable the fallback off the platform default")
	}
}

func TestRightClickPasteReportsReadFailuresAndIgnoresEmptyText(t *testing.T) {
	model := rightClickPasteTestModel(t, "auto", func() (string, error) {
		return "", errors.New("clipboard unavailable")
	})
	_, cmd := model.Update(rightButtonPress())
	model.Update(cmd())
	model.View()
	if model.notice != "clipboard text is unavailable" {
		t.Fatalf("read failure notice = %q", model.notice)
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("failed read pasted %q", got)
	}

	model = rightClickPasteTestModel(t, "auto", func() (string, error) { return "", nil })
	_, cmd = model.Update(rightButtonPress())
	model.Update(cmd())
	model.View()
	if model.notice != "" || model.composer.Value() != "" {
		t.Fatalf("empty clipboard notice=%q composer=%q", model.notice, model.composer.Value())
	}

	oversized := strings.Repeat("x", tuiapp.RightClickPasteMaxTextChars+1)
	model = rightClickPasteTestModel(t, "auto", func() (string, error) { return oversized, nil })
	_, cmd = model.Update(rightButtonPress())
	model.Update(cmd())
	model.View()
	if model.notice != "clipboard text exceeds the message size limit" {
		t.Fatalf("oversized notice = %q", model.notice)
	}
}

func TestRightClickPasteRejectsOverlappingReads(t *testing.T) {
	model := rightClickPasteTestModel(t, "auto", func() (string, error) { return "hello", nil })
	_, cmd := model.Update(rightButtonPress())
	if cmd == nil {
		t.Fatal("expected the first read to start")
	}
	if _, second := model.Update(rightButtonPress()); second != nil {
		t.Fatal("a press while the worker is busy must not start another read")
	}
}

func TestRightClickPasteReadTimeoutIsReported(t *testing.T) {
	model := rightClickPasteTestModel(t, "auto", func() (string, error) { return "", nil })
	model.rightClickPasteTarget = &tuiapp.RightClickPasteTarget{}
	model.applyRightClickPasteRead(rightClickPasteReadMsg{err: tuiapp.ErrRightClickPasteReadTimeout})
	if model.rightClickPasteResult == nil || model.rightClickPasteResult.err != "clipboard read timed out" {
		t.Fatalf("timeout result = %#v", model.rightClickPasteResult)
	}
}

func TestRightClickPasteReadDeadlineMatchesRustWorker(t *testing.T) {
	if _, err := tuiapp.ReadClipboardTextWithDeadline(func() (string, error) { return "ok", nil }, tuiapp.RightClickPasteReadTimeout); err != nil {
		t.Fatalf("fast read error = %v", err)
	}
	blocked := make(chan struct{})
	defer close(blocked)
	if _, err := tuiapp.ReadClipboardTextWithDeadline(func() (string, error) {
		<-blocked
		return "late", nil
	}, 5*time.Millisecond); !errors.Is(err, tuiapp.ErrRightClickPasteReadTimeout) {
		t.Fatalf("blocked read error = %v, want the worker timeout", err)
	}
}
