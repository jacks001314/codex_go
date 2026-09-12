package tui

import "strings"

// Rust parity: codex-rs/tui/src/resume_picker.rs transcript previews. The
// resume picker lazily loads the newest few transcript lines for an expanded
// session row.

// TranscriptPreviewSpeaker identifies who authored a preview line.
type TranscriptPreviewSpeaker string

const (
	// TranscriptPreviewUser is a user message line.
	TranscriptPreviewUser TranscriptPreviewSpeaker = "user"
	// TranscriptPreviewAssistant is an assistant message line.
	TranscriptPreviewAssistant TranscriptPreviewSpeaker = "assistant"
)

// TranscriptPreviewLine is one speaker-tagged line of the conversation preview
// (Rust TranscriptPreviewLine).
type TranscriptPreviewLine struct {
	Speaker TranscriptPreviewSpeaker
	Text    string
}

// TranscriptPreviewKind is the load state of a session's transcript preview
// (Rust TranscriptPreviewState).
type TranscriptPreviewKind string

const (
	TranscriptPreviewLoadingState TranscriptPreviewKind = "loading"
	TranscriptPreviewFailedState  TranscriptPreviewKind = "failed"
	TranscriptPreviewLoadedState  TranscriptPreviewKind = "loaded"
)

// TranscriptPreviewState is one session's lazy preview.
type TranscriptPreviewState struct {
	Kind  TranscriptPreviewKind
	Lines []TranscriptPreviewLine
}

// TranscriptPreview returns the cached preview state for a session.
func (s *SessionPickerState) TranscriptPreview(threadID string) (TranscriptPreviewState, bool) {
	if s == nil || strings.TrimSpace(threadID) == "" {
		return TranscriptPreviewState{}, false
	}
	state, ok := s.TranscriptPreviews[strings.TrimSpace(threadID)]
	return state, ok
}

// SetTranscriptPreview records a loaded or failed preview (Rust
// BackgroundEvent::Preview handling).
func (s *SessionPickerState) SetTranscriptPreview(threadID string, state TranscriptPreviewState) {
	if s == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	if s.TranscriptPreviews == nil {
		s.TranscriptPreviews = map[string]TranscriptPreviewState{}
	}
	s.TranscriptPreviews[strings.TrimSpace(threadID)] = state
}

// BeginTranscriptPreview marks a session's preview as loading and reports
// whether a load request should be issued (Rust toggle_selected_expansion only
// requests a preview for a session with no cached state).
func (s *SessionPickerState) BeginTranscriptPreview(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return false
	}
	if _, ok := s.TranscriptPreviews[threadID]; ok {
		return false
	}
	s.SetTranscriptPreview(threadID, TranscriptPreviewState{Kind: TranscriptPreviewLoadingState})
	return true
}

// renderTranscriptPreviewLines renders the preview section appended to an
// expanded session row (Rust render_transcript_preview_lines +
// render_conversation_preview_lines).
func (s *SessionPickerState) renderTranscriptPreviewLines(threadID string, width int) []string {
	state, ok := s.TranscriptPreview(threadID)
	if !ok {
		return nil
	}
	switch state.Kind {
	case TranscriptPreviewLoadingState:
		return []string{"  \u2502 Loading recent transcript..."}
	case TranscriptPreviewFailedState:
		return []string{"  \u2502 Could not load transcript preview"}
	case TranscriptPreviewLoadedState:
		if len(state.Lines) == 0 {
			return []string{"  \u2514 No transcript preview available"}
		}
	}
	var rendered []string
	for _, line := range state.Lines {
		rendered = append(rendered, renderTranscriptPreviewContent(line, width)...)
	}
	if len(rendered) == 0 {
		return nil
	}
	out := make([]string, 0, len(rendered))
	for index, line := range rendered {
		prefix := "  \u2502 "
		if index == len(rendered)-1 {
			prefix = "  \u2514 "
		}
		out = append(out, prefix+line)
	}
	return out
}

// renderTranscriptPreviewContent wraps one preview line to the preview content
// width (the row width minus the four-column frame), preserving explicit line
// breaks.
func renderTranscriptPreviewContent(line TranscriptPreviewLine, width int) []string {
	contentWidth := width - 4
	if contentWidth < 1 {
		contentWidth = 1
	}
	text := strings.TrimRight(line.Text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return WrapLines([]string{text}, WrapOptions{Width: contentWidth, BreakWords: true})
}
