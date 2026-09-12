package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// TranscriptPreviewFunc loads a session's newest transcript lines for the
// resume picker (Rust resume_picker_transcript_preview.rs). A nil hook leaves
// the picker's expanded rows without a conversation preview.
type TranscriptPreviewFunc func(threadID string, cwd string) ([]codextui.TranscriptPreviewLine, error)

// TranscriptPreviewMsg reports a resume-picker transcript preview load.
type TranscriptPreviewMsg struct {
	ThreadID string
	Lines    []codextui.TranscriptPreviewLine
	Err      error
}

// requestTranscriptPreview starts the lazy preview load for a newly expanded
// session row (Rust toggle_selected_expansion).
func (m *Model) requestTranscriptPreview(item codextui.SessionSummary) bubbletea.Cmd {
	if m == nil || m.onLoadTranscriptPreview == nil || m.modal == nil || m.modal.sessionPicker == nil {
		return nil
	}
	threadID := strings.TrimSpace(item.ThreadID)
	if threadID == "" || !m.modal.sessionPicker.BeginTranscriptPreview(threadID) {
		return nil
	}
	load := m.onLoadTranscriptPreview
	cwd := strings.TrimSpace(item.CWD)
	return func() bubbletea.Msg {
		lines, err := load(threadID, cwd)
		return TranscriptPreviewMsg{ThreadID: threadID, Lines: lines, Err: err}
	}
}

// applyTranscriptPreview records a loaded or failed preview for the open
// picker (Rust BackgroundEvent::Preview handling).
func (m *Model) applyTranscriptPreview(msg TranscriptPreviewMsg) {
	if m == nil || m.modal == nil || m.modal.sessionPicker == nil {
		return
	}
	state := codextui.TranscriptPreviewState{Kind: codextui.TranscriptPreviewLoadedState, Lines: msg.Lines}
	if msg.Err != nil {
		state = codextui.TranscriptPreviewState{Kind: codextui.TranscriptPreviewFailedState}
	}
	m.modal.sessionPicker.SetTranscriptPreview(msg.ThreadID, state)
}
