package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestModelCopyUsesStatusOutputLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "gpt-5.4"
	state.CWD = "/work/example"
	state.ThreadName = "Clipboard example"
	state.SetThreadID("00000000-0000-0000-0000-000000000123")
	var copied string
	model := NewModel(state, Options{
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})

	typeText(t, model, "/status")
	model.Update(key(bubbletea.KeyEnter))
	typeText(t, model, "/copy")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Copy to clipboard", "Whole status", "Model", "Directory", "Thread name", "Session ID"} {
		if !strings.Contains(view, want) {
			t.Fatalf("status copy picker missing %q:\n%s", want, view)
		}
	}

	// Selecting "Model" (second item) copies just the model.
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if copied != "gpt-5.4" {
		t.Fatalf("copied = %q, want gpt-5.4", copied)
	}

	// Whole status copies the rendered /status text as plain output.
	typeText(t, model, "/copy")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyEnter))
	if !strings.HasPrefix(copied, "/status\n") {
		t.Fatalf("copied = %q, want the /status text", copied)
	}
	for _, want := range []string{"gpt-5.4", "/work/example", "Clipboard example"} {
		if !strings.Contains(copied, want) {
			t.Fatalf("whole status copy missing %q:\n%s", want, copied)
		}
	}
}

func TestModelCopyReturnsToResponseAfterOtherCommandLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "gpt-5.4"
	state.AddMessage(codextui.RoleAssistant, "final answer")
	var copied string
	model := NewModel(state, Options{
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})

	typeText(t, model, "/status")
	model.Update(key(bubbletea.KeyEnter))
	if model.statusCopyTargets == nil {
		t.Fatal("/status did not retain copy targets")
	}

	// Any command other than /copy returns /copy to the assistant response.
	typeText(t, model, "/help")
	model.Update(key(bubbletea.KeyEnter))
	if model.statusCopyTargets != nil {
		t.Fatal("/help did not clear status copy targets")
	}
	typeText(t, model, "/copy")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); strings.Contains(view, "Whole status") || !strings.Contains(view, "Whole response") {
		t.Fatalf("expected response copy targets:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEnter))
	if copied != "final answer" {
		t.Fatalf("copied = %q, want final answer", copied)
	}
}
