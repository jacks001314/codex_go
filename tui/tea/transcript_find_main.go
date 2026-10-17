package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

const mainFindQueryBytes = 4096

// transcriptFindMatch is one literal hit in the full transcript source. line is
// relative to the message's expanded rendering, so it remains stable while the
// owned transcript shows other messages collapsed.
type transcriptFindMatch struct {
	message int
	line    int
}

// mainTranscriptFind mirrors Rust transcript_view::Search for the owned
// transcript (#50045): search the complete message sources, keep the compact
// presentation, and temporarily reveal only the selected message.
type mainTranscriptFind struct {
	editing         bool
	accepted        bool
	query           string
	truncated       bool
	matches         []transcriptFindMatch
	index           int
	exhausted       bool
	matchesRevision uint64
	matchesWidth    int
	savedOffset     int
	savedFollow     bool
}

func (f *mainTranscriptFind) active() bool {
	return f != nil && (f.editing || f.accepted)
}

func (f *mainTranscriptFind) selected() (transcriptFindMatch, bool) {
	if f == nil || f.index < 0 || f.index >= len(f.matches) {
		return transcriptFindMatch{}, false
	}
	return f.matches[f.index], true
}

func (f *mainTranscriptFind) statusLine() string {
	switch {
	case f == nil || f.query == "":
		return "Type to find"
	case len(f.matches) == 0:
		return "No matches"
	case f.exhausted:
		return "No more matches · enter accept · ctrl+p older · ctrl+n newer"
	default:
		return "enter accept · ctrl+p older · ctrl+n newer"
	}
}

func (f *mainTranscriptFind) barLine() string {
	if f == nil || !f.active() {
		return ""
	}
	status := f.statusLine()
	if !f.editing {
		return "Find · " + status + " · esc latest"
	}
	line := "Find: " + f.query + " · " + status + " · esc cancel"
	if f.truncated {
		line += " · query limited to 4 KiB"
	}
	return line
}

func (m *Model) beginMainTranscriptFind() {
	if m == nil || m.mainFind.editing {
		return
	}
	if m.mainFind.accepted {
		// Rust begin_search while reading returns to the query editor without
		// discarding the active query or its current match.
		m.mainFind.editing = true
		m.mainFind.accepted = false
		m.mainFind.exhausted = false
		m.refreshMainTranscriptFindMatches(false)
		m.refreshTranscript()
		return
	}
	m.mainFind.savedOffset = m.transcript.YOffset
	m.mainFind.savedFollow = m.activityFollow
	m.mainFind.editing = true
	m.mainFind.accepted = false
	m.mainFind.query = ""
	m.mainFind.truncated = false
	m.mainFind.matches = nil
	m.mainFind.index = -1
	m.mainFind.exhausted = false
	m.mainFind.matchesRevision = 0
	m.mainFind.matchesWidth = 0
	m.refreshMainTranscriptFindMatches(true)
	m.refreshTranscript()
}

func (m *Model) cancelMainTranscriptFind() {
	if m == nil {
		return
	}
	offset := m.mainFind.savedOffset
	follow := m.mainFind.savedFollow
	m.mainFind = mainTranscriptFind{}
	m.activityFollow = follow
	m.refreshTranscript()
	if !follow {
		m.transcript.SetYOffset(offset)
	}
}

func (m *Model) clearMainTranscriptFind(jumpLatest bool) {
	if m == nil {
		return
	}
	m.mainFind = mainTranscriptFind{}
	if jumpLatest {
		m.activityFollow = true
		m.refreshTranscript()
		m.transcript.GotoBottom()
		return
	}
	m.refreshTranscript()
}

// refreshMainTranscriptFindMatches re-scans the full sources. resetSelection
// chooses the newest hit for a fresh query; content updates preserve the
// selected ordinal when possible.
func (m *Model) refreshMainTranscriptFindMatches(resetSelection bool) {
	if m == nil || m.State == nil {
		return
	}
	f := &m.mainFind
	width := firstPositive(m.transcript.Width, m.width)
	if resetSelection {
		f.index = -1
		f.exhausted = false
	}
	f.matches = nil
	f.matchesRevision = m.State.MessagesRevision
	f.matchesWidth = width
	if !f.active() || f.query == "" {
		return
	}
	folded := strings.ToLower(f.query)
	for messageIndex, message := range m.State.Messages {
		lines := flattenTranscriptDisplayLines(transcriptMessageDisplayLines(message, width, m.activeTUITheme(), true, m.sessionCWD, m.showRawReasoning))
		for lineIndex, line := range lines {
			if strings.Contains(strings.ToLower(line), folded) {
				f.matches = append(f.matches, transcriptFindMatch{message: messageIndex, line: lineIndex})
			}
		}
	}
	if len(f.matches) == 0 {
		f.index = -1
		return
	}
	if f.index < 0 || f.index >= len(f.matches) {
		f.index = len(f.matches) - 1
	}
}

func (m *Model) refreshMainTranscriptFindIfNeeded() {
	if m == nil || m.State == nil || !m.mainFind.active() {
		return
	}
	width := firstPositive(m.transcript.Width, m.width)
	if m.mainFind.matchesRevision != m.State.MessagesRevision || m.mainFind.matchesWidth != width {
		m.refreshMainTranscriptFindMatches(false)
	}
}

// renderMainTranscriptFindContent renders the compact transcript with only the
// selected match's message expanded. It returns the 0-based display line to
// reverse-video and scroll into view.
func (m *Model) renderMainTranscriptFindContent() (string, int) {
	if m == nil || m.State == nil {
		return "No messages yet.", -1
	}
	match, ok := m.mainFind.selected()
	if !ok {
		return m.transcriptRenderCached(m.State, m.transcript.Width), -1
	}
	width := firstPositive(m.transcript.Width, m.width)
	content, ranges := renderTranscriptWithExpandedMessage(
		&m.transcriptMessages,
		m.State,
		m.rawOutput,
		width,
		m.activeTUITheme(),
		match.message,
		m.sessionCWD,
		m.showRawReasoning,
	)
	if match.message >= len(ranges) {
		return content, -1
	}
	start, end := ranges[match.message][0], ranges[match.message][1]
	line := start + match.line
	if line < start || line >= end {
		line = start
	}
	if line < 0 {
		return content, -1
	}
	return highlightTranscriptLine(content, line), line
}

func (m *Model) scrollTranscriptToLine(line int) {
	if m == nil || line < 0 {
		return
	}
	offset := line - m.transcript.Height/3
	if offset < 0 {
		offset = 0
	}
	maxOffset := len(strings.Split(m.lastTranscriptContent, "\n")) - m.transcript.Height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	m.transcript.SetYOffset(offset)
	m.activityFollow = false
}

func highlightTranscriptLine(content string, target int) string {
	if target < 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if target >= len(lines) {
		return content
	}
	lines[target] = reverseVideoTranscriptLine(lines[target])
	return strings.Join(lines, "\n")
}

func reverseVideoTranscriptLine(line string) string {
	const (
		on  = "\x1b[7m"
		off = "\x1b[27m"
	)
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+on)
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+on)
	return on + line + off
}

func (m *Model) stepMainTranscriptFind(delta int) bool {
	if m == nil || !m.mainFind.active() || len(m.mainFind.matches) == 0 {
		return false
	}
	next := m.mainFind.index + delta
	if next < 0 || next >= len(m.mainFind.matches) {
		m.mainFind.exhausted = true
		return false
	}
	m.mainFind.index = next
	m.mainFind.exhausted = false
	m.refreshTranscript()
	return true
}

func (m *Model) editMainTranscriptFindQuery(msg bubbletea.KeyMsg) {
	f := &m.mainFind
	switch msg.Type {
	case bubbletea.KeyBackspace:
		if f.query == "" {
			return
		}
		runes := []rune(f.query)
		f.query = string(runes[:len(runes)-1])
		f.truncated = false
	case bubbletea.KeyRunes, bubbletea.KeySpace:
		text := string(msg.Runes)
		if msg.Type == bubbletea.KeySpace {
			text = " "
		}
		if text == "" {
			return
		}
		if len(f.query)+len(text) > mainFindQueryBytes {
			f.truncated = true
			return
		}
		f.query += text
	default:
		return
	}
	m.refreshMainTranscriptFindMatches(true)
	m.refreshTranscript()
}

// updateMainTranscriptFindKey returns handled=false for keys Rust deliberately
// lets the pager consume, and for every non-Find key while the result is being
// read.
func (m *Model) updateMainTranscriptFindKey(msg bubbletea.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.mainFind.active() {
		return false, nil
	}
	switch msg.Type {
	case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
		if m.mainFind.accepted {
			m.clearMainTranscriptFind(true)
		} else {
			m.cancelMainTranscriptFind()
		}
		return true, nil
	case bubbletea.KeyCtrlN:
		m.stepMainTranscriptFind(1)
		return true, nil
	case bubbletea.KeyCtrlP:
		m.stepMainTranscriptFind(-1)
		return true, nil
	}
	if !m.mainFind.editing {
		if m.keyMatches("global", "find_transcript", keySpecFromKeyMsg(msg)) {
			m.beginMainTranscriptFind()
			return true, nil
		}
		switch msg.Type {
		case bubbletea.KeyPgUp, bubbletea.KeyPgDown, bubbletea.KeyUp, bubbletea.KeyDown,
			bubbletea.KeyHome, bubbletea.KeyEnd:
			return false, nil
		default:
			return true, nil
		}
	}
	switch msg.Type {
	case bubbletea.KeyEnter:
		if m.mainFind.query != "" && len(m.mainFind.matches) > 0 {
			m.mainFind.editing = false
			m.mainFind.accepted = true
			m.refreshTranscript()
		}
		return true, nil
	case bubbletea.KeyPgUp, bubbletea.KeyPgDown:
		return false, nil
	case bubbletea.KeyBackspace:
		m.editMainTranscriptFindQuery(msg)
		return true, nil
	case bubbletea.KeyRunes, bubbletea.KeySpace:
		m.editMainTranscriptFindQuery(msg)
		return true, nil
	default:
		return true, nil
	}
}
