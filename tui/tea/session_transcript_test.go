package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func TestSessionPickerCtrlTOpensSelectedTranscript(t *testing.T) {
	var requested []string
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-1", Title: "One", Preview: "hi", CWD: "/repo"}},
		OnReadSessionTranscript: func(threadID string) ([]codextui.Message, error) {
			requested = append(requested, threadID)
			return []codextui.Message{
				{Role: codextui.RoleUser, Text: "hello"},
				{Role: codextui.RoleAssistant, Text: "world"},
			}, nil
		},
	})
	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("resume picker not open: %#v", model.modal)
	}

	_, cmd := model.Update(key(bubbletea.KeyCtrlT))
	if model.overlay == nil {
		t.Fatal("ctrl+t should open the transcript overlay")
	}
	if !strings.Contains(model.overlay.Content(), "Loading transcript") {
		t.Fatalf("loading content = %q", model.overlay.Content())
	}
	if cmd == nil {
		t.Fatal("ctrl+t should load the transcript")
	}
	runTeaCmd(t, model, cmd)
	if len(requested) != 1 || requested[0] != "thread-1" {
		t.Fatalf("requested = %#v", requested)
	}
	content := model.overlay.Content()
	if !strings.Contains(content, "hello") || !strings.Contains(content, "world") || strings.Contains(content, "Loading transcript") {
		t.Fatalf("overlay content = %q", content)
	}
}

func TestSessionPickerCtrlTRequiresAThread(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "", Title: "Path only", Path: "rollout.jsonl", CWD: "/repo"}},
	})
	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("picker not open: modal=%#v overlay=%v", model.modal, model.overlay != nil)
	}
	model.Update(key(bubbletea.KeyCtrlT))
	if model.overlay != nil {
		t.Fatal("a session without a thread id should not open the overlay")
	}
	if model.notice != "No transcript available for this session" {
		t.Fatalf("notice = %q", model.notice)
	}
}

// TestNonTranscriptOverlayEscKeepsPagerKeymap guards the backtrack Esc handling
// from hijacking pagers that are not the transcript overlay (/diff and the
// resume picker's session transcript).
func TestNonTranscriptOverlayEscKeepsPagerKeymap(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24, DisablePasteBurst: true})
	model.overlay = chatwidget.NewTranscriptOverlayWithTitle(80, 24, "diff content", "D I F F")
	model.overlayTranscript = false

	model.Update(key(bubbletea.KeyEsc))

	if model.overlay == nil {
		t.Fatal("Esc must not close a non-transcript pager")
	}
	if strings.Contains(modelMessageText(model), "No previous message to edit.") {
		t.Fatalf("Esc started backtracking in a non-transcript pager:\n%s", modelMessageText(model))
	}
	if model.backtrack.Primed {
		t.Fatal("Esc must not prime backtracking in a non-transcript pager")
	}
}
