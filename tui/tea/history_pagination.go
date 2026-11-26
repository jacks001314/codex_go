package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
)

// Rust parity: codex-rs/tui/src/app/history_pagination.rs (#46739).
//
// A resumed transcript can start with the newest page of a long thread. When
// the reader reaches the top row the next page is fetched, spliced in above the
// loaded window, and the view keeps the same prompts under the cursor. The
// backtrack ordinal shifts with the inserted prompts so rewinding still edits
// the prompt the reader selected.

// OlderHistoryLoadFunc fetches one page of transcript messages older than
// cursor for threadID.
type OlderHistoryLoadFunc func(threadID string, cursor string) (OlderHistoryPage, error)

// OlderHistoryPage is one page of messages older than the loaded window.
type OlderHistoryPage struct {
	// Messages are the page in transcript order (oldest first).
	Messages []codextui.Message
	// Cursor addresses the page before Messages; empty means none.
	Cursor string
	// HasOlder reports that Cursor still has pages behind it.
	HasOlder bool
	// SplitTurnID names a turn the page only begins: its boundary row (a final
	// message separator, completion footer or interruption notice carrying this
	// TurnID) already sits in the loaded window (Rust
	// history_pagination::split_image_insertion).
	SplitTurnID string
	// SplitImages are that turn's generated-image rows. They belong directly
	// before its boundary row rather than at the top of the prepended page.
	SplitImages []codextui.Message
}

// olderHistoryLoadedMsg carries the async result of one page fetch.
type olderHistoryLoadedMsg struct {
	threadID string
	page     OlderHistoryPage
	err      error
}

// maybeLoadOlderHistory starts the next older page when the reader sits on the
// top row of a partial transcript (Rust App::maybe_load_older_history). A
// transcript shorter than the view keeps asking until the screen is filled or
// the thread begins.
func (m *Model) maybeLoadOlderHistory() bubbletea.Cmd {
	if m == nil || m.loadOlderHistory == nil || m.olderHistoryLoading || !m.transcriptHistoryPartial {
		return nil
	}
	cursor := strings.TrimSpace(m.olderHistoryCursor)
	threadID := m.currentThreadID()
	if cursor == "" || threadID == "" {
		return nil
	}
	if m.overlay != nil {
		if !m.overlayTranscript || m.overlay.YOffset() > 0 {
			return nil
		}
	} else if m.transcript.YOffset > 0 {
		return nil
	}
	load := m.loadOlderHistory
	m.olderHistoryLoading = true
	return func() bubbletea.Msg {
		page, err := load(threadID, cursor)
		return olderHistoryLoadedMsg{threadID: threadID, page: page, err: err}
	}
}

// applyOlderHistoryPage splices a loaded page above the window and keeps the
// reading position and backtrack selection on the prompts that were visible.
func (m *Model) applyOlderHistoryPage(threadID string, page OlderHistoryPage, err error) {
	if m == nil || m.State == nil {
		return
	}
	m.olderHistoryLoading = false
	if strings.TrimSpace(threadID) != m.currentThreadID() {
		// The reader switched threads while the page was in flight.
		return
	}
	if err != nil {
		return
	}
	m.olderHistoryCursor = page.Cursor
	m.transcriptHistoryPartial = page.HasOlder
	if len(page.Messages) == 0 {
		return
	}
	insertAt := m.sessionHeaderPrefixLen()
	inlineFollowing := m.transcript.AtBottom()
	inlineOffset := m.transcript.YOffset
	inlineRows := len(m.transcript.lines)
	overlayFollowing := true
	overlayOffset := 0
	overlayRows := 0
	if m.overlay != nil && m.overlayTranscript {
		overlayFollowing = m.overlay.AtBottom()
		overlayOffset = m.overlay.YOffset()
		overlayRows = contentLineCount(m.overlay.Content())
	}
	inserted := page.Messages
	if m.isSideConversationThread(threadID) {
		// Rust #60554 leaves interruption notices out of side threads: the
		// restored row describes the main conversation's turn boundary.
		inserted = dropInterruptionNotices(page.Messages)
	}
	if m.backtrack.HasSelection() && strings.TrimSpace(m.backtrack.BaseThreadID) == m.currentThreadID() {
		m.backtrack.NthUserMessage += tuiapp.UserCount(inserted)
	}
	if anchor := m.splitTurnAnchorIndex(page.SplitTurnID, insertAt); anchor >= 0 && len(page.SplitImages) > 0 {
		// Rust places the split turn's images directly before its boundary row,
		// after any image rows already sitting there. Everything else on the page
		// stays a normal older page.
		m.State.InsertMessages(insertAt, inserted)
		m.Transcript.NoteOlderAssistantMessages(inserted)
		m.registerCollabTranscriptRows(inserted)
		m.refreshTranscript()
		m.adjustAfterOlderPage(inlineFollowing, inlineOffset, inlineRows, overlayFollowing, overlayOffset, overlayRows)
		m.State.InsertMessages(anchor+len(inserted), page.SplitImages)
		m.refreshTranscript()
		return
	}
	inserted = append(inserted, page.SplitImages...)
	m.State.InsertMessages(insertAt, inserted)
	// Rust #62403: replies from an older page sort before everything the
	// view observed live.
	m.Transcript.NoteOlderAssistantMessages(inserted)
	m.registerCollabTranscriptRows(inserted)
	m.refreshTranscript()
	m.adjustAfterOlderPage(inlineFollowing, inlineOffset, inlineRows, overlayFollowing, overlayOffset, overlayRows)
}

// splitTurnAnchorIndex finds where a split turn's boundary row will sit after the
// page is prepended at insertAt. It returns -1 when the loaded window has no row
// for that turn, which keeps the images on the page itself.
func (m *Model) splitTurnAnchorIndex(turnID string, insertAt int) int {
	turnID = strings.TrimSpace(turnID)
	if m == nil || m.State == nil || turnID == "" {
		return -1
	}
	for index := len(m.State.Messages) - 1; index >= 0; index-- {
		message := m.State.Messages[index]
		if strings.TrimSpace(message.TurnID) != turnID {
			continue
		}
		start := index
		for start > 0 && m.State.Messages[start-1].ImageGenerated {
			start--
		}
		return start
	}
	return -1
}

// adjustAfterOlderPage keeps the reader on the rows that were visible when the
// page landed. Insertions below the reading position leave the offsets alone.
func (m *Model) adjustAfterOlderPage(inlineFollowing bool, inlineOffset, inlineRows int, overlayFollowing bool, overlayOffset, overlayRows int) {
	if !inlineFollowing {
		added := len(m.transcript.lines) - inlineRows
		m.transcript.YOffset = min(inlineOffset+added, m.transcript.maxYOffset())
	}
	if m.overlay != nil && m.overlayTranscript {
		m.syncTranscriptOverlay()
		if !overlayFollowing {
			added := contentLineCount(m.overlay.Content()) - overlayRows
			m.overlay.SetYOffset(overlayOffset + added)
		}
	}
}

// sessionHeaderPrefixLen is the number of leading transcript entries that are
// session chrome rather than conversation history (Rust history_prefix_len).
func (m *Model) sessionHeaderPrefixLen() int {
	if m == nil || m.State == nil || !m.sessionHeaderShown || len(m.State.Messages) == 0 {
		return 0
	}
	return 1
}

// isSideConversationThread reports whether threadID is one of the retained
// side conversations, which never restore interruption notices.
func (m *Model) isSideConversationThread(threadID string) bool {
	if m == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	for _, side := range m.sideConversations {
		if side != nil && strings.TrimSpace(side.SideThreadID) == threadID {
			return true
		}
	}
	return false
}

// dropInterruptionNotices removes restored interrupted-turn boundary rows.
func dropInterruptionNotices(messages []codextui.Message) []codextui.Message {
	kept := make([]codextui.Message, 0, len(messages))
	for _, message := range messages {
		if message.InterruptionNotice {
			continue
		}
		kept = append(kept, message)
	}
	return kept
}

func contentLineCount(content string) int {
	if strings.TrimRight(content, "\r\n") == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}
