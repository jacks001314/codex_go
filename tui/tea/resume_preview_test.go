package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func TestSessionPickerCtrlELoadsTranscriptPreview(t *testing.T) {
	var requested []string
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-1", Title: "One", Preview: "hi", CWD: "/repo"}},
		OnLoadTranscriptPreview: func(threadID string, cwd string) ([]codextui.TranscriptPreviewLine, error) {
			requested = append(requested, threadID+"|"+cwd)
			return []codextui.TranscriptPreviewLine{{Speaker: codextui.TranscriptPreviewUser, Text: "hello"}}, nil
		},
	})

	typeText(t, model, "/resume")
	model.Update(key(keymsg.KeyEnter))
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("resume picker not open: %#v", model.modal)
	}

	_, cmd := model.Update(key(keymsg.KeyCtrlE))
	if cmd == nil {
		t.Fatal("expanding a row should request the transcript preview")
	}
	state, ok := model.modal.sessionPicker.TranscriptPreview("thread-1")
	if !ok || state.Kind != codextui.TranscriptPreviewLoadingState {
		t.Fatalf("preview state = %#v ok=%v", state, ok)
	}

	runTeaCmd(t, model, cmd)
	if len(requested) != 1 || requested[0] != "thread-1|/repo" {
		t.Fatalf("requested = %#v", requested)
	}
	state, _ = model.modal.sessionPicker.TranscriptPreview("thread-1")
	if state.Kind != codextui.TranscriptPreviewLoadedState || len(state.Lines) != 1 || state.Lines[0].Text != "hello" {
		t.Fatalf("preview state = %#v", state)
	}

	// Collapsing and re-expanding reuses the cached preview.
	if _, cmd := model.Update(key(keymsg.KeyCtrlE)); cmd != nil {
		t.Fatal("collapsing should not request a preview")
	}
	if _, cmd := model.Update(key(keymsg.KeyCtrlE)); cmd != nil {
		t.Fatal("a cached preview should not be re-requested")
	}
	if len(requested) != 1 {
		t.Fatalf("requested = %#v, want one load", requested)
	}
}

func TestSessionPickerCtrlEPreviewFailureIsRecorded(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-1", Preview: "hi", CWD: "/repo"}},
		OnLoadTranscriptPreview: func(string, string) ([]codextui.TranscriptPreviewLine, error) {
			return nil, errTranscriptPreview
		},
	})
	typeText(t, model, "/resume")
	model.Update(key(keymsg.KeyEnter))
	_, cmd := model.Update(key(keymsg.KeyCtrlE))
	runTeaCmd(t, model, cmd)
	state, ok := model.modal.sessionPicker.TranscriptPreview("thread-1")
	if !ok || state.Kind != codextui.TranscriptPreviewFailedState {
		t.Fatalf("preview state = %#v ok=%v", state, ok)
	}
}

type transcriptPreviewError struct{}

func (transcriptPreviewError) Error() string { return "preview unavailable" }

var errTranscriptPreview = transcriptPreviewError{}
