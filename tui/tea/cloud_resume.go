package tea

// Rust parity: codex-rs/tui/src/app/resume_preparation.rs CloudResume (#63211).
// A dropped Cloud reply stream can miss replies the server saved while the
// terminal was away, so the TUI reattaches the displayed conversation in the
// background instead of only releasing held input.

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// cloudHistoryUnavailableNotice mirrors Rust's CloudHistoryCheck::Unavailable:
// sending stays paused and the draft stays in the composer.
const cloudHistoryUnavailableNotice = "Cloud conversation history is unavailable. Sending is paused and your draft is saved here."

// CloudConversationResyncMsg carries the background reattach of a Cloud
// conversation whose reply stream came back.
type CloudConversationResyncMsg struct {
	ThreadID string
	Response SessionResumeResponse
	Err      error
}

// beginCloudConversationResync refetches the displayed conversation through
// the resume callback. It reports nil when the host has no resume surface, in
// which case the stream-reconnected path only releases held input.
func (m *Model) beginCloudConversationResync(threadID string) bubbletea.Cmd {
	if m == nil || m.onResumeSession == nil || m.State == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	resume := m.onResumeSession
	backendID := m.backendID
	cwd := strings.TrimSpace(m.State.CWD)
	return func() bubbletea.Msg {
		response, err := resume(codextui.SessionSelection{
			Kind:      codextui.SessionSelectionResume,
			Target:    codextui.SessionTarget{BackendID: backendID, ThreadID: threadID, CWD: cwd},
			ResumeCWD: cwd,
		})
		return CloudConversationResyncMsg{ThreadID: threadID, Response: response, Err: err}
	}
}

// applyCloudConversationResync installs complete history and resumes sending,
// or keeps the conversation paused when the history cannot be verified
// (Rust #63211: "Check that paginated history is complete ... If history
// cannot be verified, keep sending paused and show an error.").
// cloudHistoryIncludesTranscript reports whether the refetched history still
// contains every reply the view already showed, the Rust #63211 check that the
// paginated history covers the conversation on screen before it is installed.
func cloudHistoryIncludesTranscript(shown []codextui.Message, fetched []codextui.Message) bool {
	if len(shown) == 0 {
		return true
	}
	byItemID := make(map[string]bool, len(fetched))
	byText := make(map[string]bool, len(fetched))
	for _, message := range fetched {
		if itemID := strings.TrimSpace(message.ItemID); itemID != "" {
			byItemID[itemID] = true
		}
		if text := strings.TrimSpace(message.Text); text != "" {
			byText[text] = true
		}
	}
	for _, message := range shown {
		text := strings.TrimSpace(message.Text)
		itemID := strings.TrimSpace(message.ItemID)
		if text == "" && itemID == "" {
			continue
		}
		if itemID != "" && byItemID[itemID] {
			continue
		}
		if text != "" && byText[text] {
			continue
		}
		return false
	}
	return true
}

func (m *Model) applyCloudConversationResync(msg CloudConversationResyncMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(msg.ThreadID)
	if threadID == "" || threadID != m.cloudStreamDisconnectedThreadID {
		return nil
	}
	if msg.Err != nil || msg.Response.ReadOnly || msg.Response.HasOlderHistory ||
		!cloudHistoryIncludesTranscript(m.State.Messages, msg.Response.Messages) {
		// The conversation stays held: queued prompts and the draft are kept
		// for a later successful reattach.
		m.notice = cloudHistoryUnavailableNotice
		m.refreshTranscript()
		return nil
	}
	cwd := strings.TrimSpace(m.State.CWD)
	m.applyResumeResponseWithResolvedCWD(threadID, msg.Response, cwd)
	m.cloudStreamDisconnectedThreadID = ""
	if m.notice == cloudHistoryUnavailableNotice || m.notice == "Reconnecting to the Cloud conversation\u2026" {
		m.notice = ""
	}
	m.refreshTranscript()
	return m.submitNextQueued()
}
