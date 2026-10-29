package tea

import (
	"strings"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/draft_state.rs
// is_bash_mode. The leading `!` of a shell escape is composer state rather
// than editable text: the textarea holds only the command body and the marker
// is drawn as the prompt. Slash text stays in the textarea (#56021), so `/model`
// renders after the normal composer prompt. Every canonical read of the draft
// (submission, history, queued restore, external editor, slash popup) goes
// through composerText so the shell marker is put back in front of the body.
type composerPrefix int

const (
	composerPrefixNone composerPrefix = iota
	composerPrefixShell
)

func (p composerPrefix) text() string {
	switch p {
	case composerPrefixShell:
		return "!"
	default:
		return ""
	}
}

func (p composerPrefix) len() int {
	return len(p.text())
}

// prompt is what the textarea prints in front of the first line. Rust's
// composer draws a bold `\u203a` (`›`) and one space before the draft.
func (p composerPrefix) prompt() string {
	if p == composerPrefixNone {
		return "\u203a "
	}
	return p.text() + " "
}

// composerText returns the canonical draft text: prompt prefix plus the
// editable body (Rust ChatComposer::current_text).
func (m *Model) composerText() string {
	if m == nil {
		return ""
	}
	return m.composerPrefix.text() + m.composer.Value()
}

// composerBody returns the editable text without the prompt marker.
func (m *Model) composerBody() string {
	if m == nil {
		return ""
	}
	return m.composer.Value()
}

// composerIsEmpty mirrors Rust's is_empty: a bare prompt marker is not empty.
func (m *Model) composerIsEmpty() bool {
	if m == nil {
		return true
	}
	return m.composerPrefix == composerPrefixNone && m.composer.Value() == ""
}

// composerCursor is the cursor position in canonical text coordinates.
func (m *Model) composerCursor() int {
	if m == nil {
		return 0
	}
	return len([]rune(m.composerPrefix.text())) + m.composerCursorOffset()
}

// setComposerPrompt replaces the draft with canonical text, moving a leading
// `!` into prompt state (Rust imported_text_for_textarea). A leading `/` stays
// editable.
func (m *Model) setComposerPrompt(text string) {
	if m == nil {
		return
	}
	prefix, body := splitComposerPrompt(text)
	m.composerPrefix = prefix
	m.composerElements = nil
	m.composer.SetValue(body)
	m.refreshComposerPrompt()
}

// resetComposer clears the draft and its prompt marker.
func (m *Model) resetComposer() {
	if m == nil {
		return
	}
	m.endHistorySearch()
	m.composerPrefix = composerPrefixNone
	m.composer.Reset()
	m.refreshComposerPrompt()
}

// clearComposerPrefix drops only the prompt marker, leaving the body intact
// (Rust's backward-deletion-at-start behavior).
func (m *Model) clearComposerPrefix() {
	if m == nil || m.composerPrefix == composerPrefixNone {
		return
	}
	m.composerPrefix = composerPrefixNone
	m.refreshComposerPrompt()
}

func (m *Model) refreshComposerPrompt() {
	if m == nil {
		return
	}
	m.composer.Prompt = m.composerPrefix.prompt()
}

// splitComposerPrompt reports the shell prompt prefix carried by canonical
// text. Rust matches the first *extended grapheme cluster*, so `!` followed by
// a combining mark stays literal text.
func splitComposerPrompt(text string) (composerPrefix, string) {
	if text == "" {
		return composerPrefixNone, text
	}
	cluster, rest, _, _ := uniseg.FirstGraphemeClusterInString(text, -1)
	switch cluster {
	case "!":
		return composerPrefixShell, rest
	default:
		return composerPrefixNone, text
	}
}

// syncComposerPrefixFromText absorbs a leading `!` that just landed in
// the body, preserving the cursor offset (Rust sync_prompt_prefix_from_text).
func (m *Model) syncComposerPrefixFromText() {
	if m == nil || m.composerPrefix != composerPrefixNone {
		return
	}
	body := m.composer.Value()
	prefix, rest := splitComposerPrompt(body)
	if prefix == composerPrefixNone {
		return
	}
	cursor := m.composerCursorOffset()
	m.composerPrefix = prefix
	m.composer.SetValue(rest)
	if cursor > 0 {
		cursor--
	}
	m.setComposerCursorOffset(cursor)
	m.refreshComposerPrompt()
}

// handleComposerPrefixKey handles the two key events that act on the marker
// itself. It runs before the textarea sees the key.
func (m *Model) handleComposerPrefixKey(msg bubbletea.Msg) (bool, bubbletea.Cmd) {
	if m == nil || m.composerPrefix == composerPrefixNone {
		return false, nil
	}
	key, ok := msg.(bubbletea.KeyMsg)
	if !ok {
		return false, nil
	}
	if m.composerCursorOffset() != 0 || m.vimSearchMode {
		return false, nil
	}
	switch key.Type {
	case bubbletea.KeyBackspace, bubbletea.KeyCtrlH:
		m.clearComposerPrefix()
		m.refreshSlashPopup()
		return true, nil
	case bubbletea.KeyEsc:
		// Rust only lets Esc out of shell mode, and only once the body is empty.
		if m.composerPrefix == composerPrefixShell && m.composer.Value() == "" {
			m.clearComposerPrefix()
			m.refreshSlashPopup()
			return true, nil
		}
	}
	return false, nil
}

// composerCursorOffset returns the cursor's rune offset inside the body.
func (m *Model) composerCursorOffset() int {
	if m == nil {
		return 0
	}
	row := m.composer.Line()
	lines := strings.Split(m.composer.Value(), "\n")
	offset := 0
	for i := 0; i < row && i < len(lines); i++ {
		offset += utf8.RuneCountInString(lines[i]) + 1
	}
	info := m.composer.LineInfo()
	if row >= 0 && row < len(lines) {
		offset += info.CharOffset
	}
	return offset
}

// setComposerCursorOffset moves the textarea cursor to a rune offset inside
// the body. bubbles exposes only per-row column moves, so walk the rows.
func (m *Model) setComposerCursorOffset(offset int) {
	if m == nil || offset < 0 {
		return
	}
	lines := strings.Split(m.composer.Value(), "\n")
	if len(lines) == 0 {
		return
	}
	row, col := len(lines)-1, utf8.RuneCountInString(lines[len(lines)-1])
	for i, line := range lines {
		width := utf8.RuneCountInString(line)
		if offset <= width {
			row, col = i, offset
			break
		}
		offset -= width + 1
	}
	for current := m.composer.Line(); current > row; current-- {
		m.composer.CursorUp()
	}
	for current := m.composer.Line(); current < row; current++ {
		m.composer.CursorDown()
	}
	m.composer.SetCursor(col)
}
