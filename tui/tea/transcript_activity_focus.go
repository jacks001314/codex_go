package tea

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// transcriptActivityFocus mirrors the owned-transcript activity disclosure
// controls from Rust #46734. Tool-call identities are the stable disclosure
// keys; a message-index key remains only for history entries without IDs.
type transcriptActivityFocus struct {
	active         bool
	focusedMessage int
	focusedIDs     []string
	expanded       map[string]bool
	savedOffset    int
	savedFollow    bool
}

func (m *Model) transcriptActivityExpandable(index int) bool {
	if m == nil || m.State == nil || index < 0 || index >= len(m.State.Messages) {
		return false
	}
	message := m.State.Messages[index]
	if message.TranscriptOnly {
		return strings.TrimSpace(message.Text) != ""
	}
	if message.Role != codextui.RoleHistory {
		return false
	}
	if strings.TrimSpace(message.TranscriptText) != "" {
		return true
	}
	raw := strings.TrimSpace(message.RawText)
	return raw != "" && raw != strings.TrimSpace(message.Text)
}

func (m *Model) transcriptActivityIndexes() []int {
	if m == nil || m.State == nil {
		return nil
	}
	indexes := make([]int, 0)
	for index := range m.State.Messages {
		if m.transcriptActivityExpandable(index) {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func (m *Model) transcriptActivityKeys(index int) []string {
	if m == nil || m.State == nil || index < 0 || index >= len(m.State.Messages) {
		return nil
	}
	ids := normalizeActivityIDs(m.State.Messages[index].ActivityIDs)
	if len(ids) > 0 {
		return ids
	}
	return []string{fmt.Sprintf("message:%d", index)}
}

func (m *Model) transcriptActivityExpanded(index int) bool {
	if m == nil || len(m.activityFocus.expanded) == 0 {
		return false
	}
	for _, key := range m.transcriptActivityKeys(index) {
		if m.activityFocus.expanded[key] {
			return true
		}
	}
	return false
}

func (m *Model) transcriptActivityMessageForIDs(ids []string) int {
	if m == nil || m.State == nil || len(ids) == 0 {
		return -1
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			wanted[id] = true
		}
	}
	for index := range m.State.Messages {
		for _, key := range m.transcriptActivityKeys(index) {
			if wanted[key] {
				return index
			}
		}
	}
	return -1
}

func (m *Model) focusedTranscriptActivityIndex() int {
	if m == nil || !m.activityFocus.active {
		return -1
	}
	if index := m.transcriptActivityMessageForIDs(m.activityFocus.focusedIDs); index >= 0 {
		return index
	}
	if len(m.activityFocus.focusedIDs) == 0 && m.transcriptActivityExpandable(m.activityFocus.focusedMessage) {
		return m.activityFocus.focusedMessage
	}
	return -1
}

func (m *Model) setFocusedTranscriptActivity(index int) bool {
	if m == nil || !m.transcriptActivityExpandable(index) {
		return false
	}
	m.activityFocus.focusedMessage = index
	m.activityFocus.focusedIDs = m.transcriptActivityKeys(index)
	return true
}

func (m *Model) nearestTranscriptActivityIndex() int {
	if m == nil || m.State == nil {
		return -1
	}
	indexes := m.transcriptActivityIndexes()
	if len(indexes) == 0 {
		return -1
	}
	width := firstPositive(m.transcript.Width, m.width)
	_, ranges := renderTranscriptMessagesWithRanges(
		&m.transcriptMessages,
		m.State,
		m.rawOutput,
		width,
		m.activeTUITheme(),
		false,
		m.sessionCWD,
		m.showRawReasoning,
	)
	top := m.transcript.YOffset
	bottom := top + m.transcript.Height
	for index := len(indexes) - 1; index >= 0; index-- {
		message := indexes[index]
		if message >= len(ranges) {
			continue
		}
		start, end := ranges[message][0], ranges[message][1]
		if end > start && end > top && start < bottom {
			return message
		}
	}
	return indexes[len(indexes)-1]
}

func (m *Model) focusTranscriptActivity() {
	if m == nil {
		return
	}
	if m.activityFocus.active {
		m.clearTranscriptActivityFocus()
		return
	}
	index := m.nearestTranscriptActivityIndex()
	if index < 0 {
		return
	}
	m.clearMainTranscriptFind(false)
	if m.activityFocus.expanded == nil {
		m.activityFocus.expanded = map[string]bool{}
	}
	m.activityFocus.active = true
	m.setFocusedTranscriptActivity(index)
	m.activityFocus.savedOffset = m.transcript.YOffset
	m.activityFocus.savedFollow = m.activityFollow
	m.refreshTranscript()
}

func (m *Model) clearTranscriptActivityFocus() {
	if m == nil {
		return
	}
	m.activityFocus.active = false
	m.activityFocus.focusedMessage = -1
	m.activityFocus.focusedIDs = nil
	m.refreshTranscript()
}

func (m *Model) moveTranscriptActivityFocus(delta int) bool {
	if m == nil || !m.activityFocus.active || delta == 0 {
		return false
	}
	indexes := m.transcriptActivityIndexes()
	if len(indexes) == 0 {
		m.clearTranscriptActivityFocus()
		return false
	}
	current := -1
	focused := m.focusedTranscriptActivityIndex()
	for position, index := range indexes {
		if index == focused {
			current = position
			break
		}
	}
	if current < 0 {
		current = len(indexes) - 1
	}
	next := current + delta
	if next < 0 {
		next = 0
	}
	if next >= len(indexes) {
		next = len(indexes) - 1
	}
	if indexes[next] == focused {
		return false
	}
	m.setFocusedTranscriptActivity(indexes[next])
	m.refreshTranscript()
	return true
}

func (m *Model) setTranscriptActivityExpanded(expanded bool) bool {
	if m == nil || !m.activityFocus.active {
		return false
	}
	index := m.focusedTranscriptActivityIndex()
	if index < 0 || !m.transcriptActivityExpandable(index) {
		return false
	}
	if m.activityFocus.expanded == nil {
		m.activityFocus.expanded = map[string]bool{}
	}
	for _, key := range m.transcriptActivityKeys(index) {
		if expanded {
			m.activityFocus.expanded[key] = true
		} else {
			delete(m.activityFocus.expanded, key)
		}
	}
	m.refreshTranscript()
	return true
}

func (m *Model) toggleTranscriptActivityExpanded() bool {
	if m == nil || !m.activityFocus.active {
		return false
	}
	index := m.focusedTranscriptActivityIndex()
	if index < 0 {
		return false
	}
	return m.setTranscriptActivityExpanded(!m.transcriptActivityExpanded(index))
}

func (m *Model) expandedTranscriptActivityMessages() map[int]bool {
	if m == nil || m.State == nil || len(m.activityFocus.expanded) == 0 {
		return nil
	}
	expanded := map[int]bool{}
	for index := range m.State.Messages {
		if m.transcriptActivityExpanded(index) {
			expanded[index] = true
		}
	}
	return expanded
}

func (m *Model) renderTranscriptActivityContent() (string, int) {
	if m == nil || m.State == nil || !m.activityFocus.active {
		return "No messages yet.", -1
	}
	width := firstPositive(m.transcript.Width, m.width)
	content, ranges := renderTranscriptWithExpandedMessages(
		&m.transcriptMessages,
		m.State,
		m.rawOutput,
		width,
		m.activeTUITheme(),
		false,
		m.expandedTranscriptActivityMessages(),
		m.sessionCWD,
		m.showRawReasoning,
	)
	index := m.focusedTranscriptActivityIndex()
	if index < 0 || index >= len(ranges) {
		return content, -1
	}
	line := ranges[index][0]
	if line < 0 {
		return content, -1
	}
	return highlightTranscriptLine(content, line), line
}

func (m *Model) ensureTranscriptLineVisible(line int) {
	if m == nil || line < 0 {
		return
	}
	top := m.transcript.YOffset
	bottom := top + m.transcript.Height
	if line < top || line >= bottom {
		m.scrollTranscriptToLine(line)
	}
}

func (m *Model) activityFocusBarLine() string {
	if m == nil || !m.activityFocus.active {
		return ""
	}
	return "Activity · up/down select · enter details · esc back"
}

func (m *Model) updateTranscriptActivityFocusKey(msg bubbletea.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.activityFocus.active {
		return false, nil
	}
	if m.keyMatches("global", "focus_activity", keySpecFromKeyMsg(msg)) {
		m.clearTranscriptActivityFocus()
		return true, nil
	}
	switch msg.Type {
	case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
		m.clearTranscriptActivityFocus()
		return true, nil
	case bubbletea.KeyUp:
		m.moveTranscriptActivityFocus(-1)
		return true, nil
	case bubbletea.KeyDown:
		m.moveTranscriptActivityFocus(1)
		return true, nil
	case bubbletea.KeyPgUp:
		m.moveTranscriptActivityFocus(-5)
		return true, nil
	case bubbletea.KeyPgDown:
		m.moveTranscriptActivityFocus(5)
		return true, nil
	case bubbletea.KeyHome:
		indexes := m.transcriptActivityIndexes()
		if len(indexes) > 0 {
			m.setFocusedTranscriptActivity(indexes[0])
			m.refreshTranscript()
		}
		return true, nil
	case bubbletea.KeyEnd:
		indexes := m.transcriptActivityIndexes()
		if len(indexes) > 0 {
			m.setFocusedTranscriptActivity(indexes[len(indexes)-1])
			m.refreshTranscript()
		}
		return true, nil
	case bubbletea.KeyEnter:
		m.toggleTranscriptActivityExpanded()
		return true, nil
	case bubbletea.KeyRight:
		m.setTranscriptActivityExpanded(true)
		return true, nil
	case bubbletea.KeyLeft:
		m.setTranscriptActivityExpanded(false)
		return true, nil
	}
	if msg.Type == bubbletea.KeyRunes && len(msg.Runes) == 1 && !msg.Paste {
		switch msg.Runes[0] {
		case 'k':
			m.moveTranscriptActivityFocus(-1)
			return true, nil
		case 'j':
			m.moveTranscriptActivityFocus(1)
			return true, nil
		}
	}
	// Ordinary text resumes composing and drops the focus, matching Rust's
	// disclosure handler.
	m.activityFocus.active = false
	m.activityFocus.focusedMessage = -1
	m.activityFocus.focusedIDs = nil
	m.refreshTranscript()
	return false, nil
}
