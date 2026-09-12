package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// SessionTranscriptFunc loads a session's full transcript for the resume
// picker's transcript overlay (Rust PickerLoadRequest::Transcript). A nil hook
// leaves ctrl+t unavailable in the picker.
type SessionTranscriptFunc func(threadID string) ([]codextui.Message, error)

// SessionTranscriptMsg reports a resume-picker transcript load.
type SessionTranscriptMsg struct {
	ThreadID string
	Messages []codextui.Message
	Err      error
}

// openSelectedSessionTranscript opens the transcript overlay with a loading
// frame and starts the load (Rust open_selected_transcript +
// begin_transcript_loading).
func (m *Model) openSelectedSessionTranscript(item codextui.SessionSummary) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := strings.TrimSpace(item.ThreadID)
	if threadID == "" {
		m.notice = "No transcript available for this session"
		return nil
	}
	m.ensureSize()
	m.overlay = chatwidget.NewTranscriptOverlayWithTitle(m.width, m.height, "Loading transcript...", "T R A N S C R I P T")
	m.overlayTranscript = false
	m.sessionTranscriptThreadID = threadID
	openCmd := m.openPagerTerminalMode()
	reader := m.onReadSessionTranscript
	if reader == nil {
		m.overlay.SetContent("Could not load transcript preview")
		return openCmd
	}
	return bubbletea.Batch(openCmd, func() bubbletea.Msg {
		messages, err := reader(threadID)
		return SessionTranscriptMsg{ThreadID: threadID, Messages: messages, Err: err}
	})
}

// applySessionTranscript renders the loaded transcript into the open overlay
// (Rust BackgroundEvent::Transcript handling).
func (m *Model) applySessionTranscript(msg SessionTranscriptMsg) {
	if m == nil || m.overlay == nil || m.overlayTranscript {
		return
	}
	if strings.TrimSpace(msg.ThreadID) == "" || strings.TrimSpace(msg.ThreadID) != strings.TrimSpace(m.sessionTranscriptThreadID) {
		return
	}
	if msg.Err != nil {
		m.overlay.SetContent("Could not load transcript: " + msg.Err.Error())
		return
	}
	state := &codextui.State{Messages: append([]codextui.Message(nil), msg.Messages...)}
	m.overlay.SetContent(renderTranscriptWithCache(nil, state, m.rawOutput, m.width, m.activeTUITheme(), true, m.sessionCWD))
}
