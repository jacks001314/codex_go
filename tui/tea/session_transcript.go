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

// SessionReadAcknowledgeFunc marks the transcript revision observed in the
// picker read and returns the resulting receipt so the unread dot follows the
// backend (Rust #58264 PickerLoadRequest::Acknowledge).
type SessionReadAcknowledgeFunc func(threadID string, expectedRevision string) (unread bool, revision string, err error)

// SessionTranscriptMsg reports a resume-picker transcript load.
type SessionTranscriptMsg struct {
	ThreadID string
	Messages []codextui.Message
	Err      error
}

// SessionTranscriptAcknowledgedMsg reports the receipt returned after the
// observed picker transcript revision was marked read (Rust #58264
// BackgroundEvent::Acknowledged).
type SessionTranscriptAcknowledgedMsg struct {
	ThreadID string
	Unread   bool
	Revision string
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
	m.seedOverlayMouseScrollSpeed()
	m.overlayTranscript = false
	m.overlayMouseCapture = mouseCaptureEnabled
	m.sessionTranscriptThreadID = threadID
	// Rust #58264: keep the receipt observed when the row was opened; the
	// acknowledgement uses it to reject a newer backend revision.
	m.sessionTranscriptRevision = strings.TrimSpace(item.UnreadRevision)
	m.sessionTranscriptReady = false
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
	m.overlay.SetContent(renderTranscriptWithCache(nil, state, m.rawOutput, m.width, m.activeTUITheme(), true, m.sessionCWD, m.showRawReasoning))
	// The overlay now holds the rendered transcript; the next focused frame may
	// acknowledge the observed revision (Rust #58264 handle_overlay_event).
	m.sessionTranscriptReady = true
}

// pickerTranscriptAckCmd acknowledges the observed picker transcript revision
// once it has rendered in a focused terminal (Rust #58264 acknowledges on the
// overlay's draw / focus event).
func (m *Model) pickerTranscriptAckCmd() bubbletea.Cmd {
	if m == nil || m.overlay == nil || m.overlayTranscript || !m.terminalFocused {
		return nil
	}
	if m.sessionTranscriptThreadID == "" || !m.sessionTranscriptReady {
		return nil
	}
	threadID := strings.TrimSpace(m.sessionTranscriptThreadID)
	revision := strings.TrimSpace(m.sessionTranscriptRevision)
	m.sessionTranscriptReady = false
	m.sessionTranscriptRevision = ""
	if threadID == "" || revision == "" {
		return nil
	}
	reader := m.onAcknowledgeSessionRead
	if reader == nil {
		return nil
	}
	return func() bubbletea.Msg {
		unread, next, err := reader(threadID, revision)
		return SessionTranscriptAcknowledgedMsg{ThreadID: threadID, Unread: unread, Revision: next, Err: err}
	}
}

// applySessionTranscriptAcknowledged updates the picker's unread marker from
// the acknowledgement response (Rust #58264 BackgroundEvent::Acknowledged).
func (m *Model) applySessionTranscriptAcknowledged(msg SessionTranscriptAcknowledgedMsg) {
	if m == nil || msg.Err != nil {
		return
	}
	revision := ""
	if msg.Unread {
		revision = strings.TrimSpace(msg.Revision)
	}
	m.setSessionUnreadRevision(msg.ThreadID, revision)
}

// setSessionUnreadRevision updates the receipt for one thread everywhere the
// picker may read it from (the stored snapshot and an already-open modal).
func (m *Model) setSessionUnreadRevision(threadID string, revision string) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	revision = strings.TrimSpace(revision)
	apply := func(items []codextui.SessionSummary) {
		for i := range items {
			if strings.TrimSpace(items[i].ThreadID) == threadID {
				items[i].UnreadRevision = revision
			}
		}
	}
	apply(m.sessionItems)
	if m.modal != nil && m.modal.sessionPicker != nil {
		apply(m.modal.sessionPicker.Items)
	}
}
