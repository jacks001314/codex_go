package tea

import (
	"strings"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"

	composerhistory "codex_go/tui/bottom_pane/chat_composer"
)

// maybeBeginHistorySearch opens Rust's composer-owned Ctrl+R search without
// changing the current draft until the query starts selecting a match.
func (m *Model) maybeBeginHistorySearch(keySpec string) bool {
	if m == nil || m.modal != nil || m.slashPopup.Active || m.skillPopup.Active ||
		m.asyncQuestions.Expanded() || !m.keyMatches("composer", "history_search_previous", keySpec) {
		return false
	}
	m.historySearch = composerhistory.BeginHistorySearch(m.currentComposerDraftState(), m.inputHistory)
	m.historySearchElements = cloneComposerTextElements(m.composerElements)
	m.historySearchPreviewText = m.composerText()
	m.historySearchAttachments = cloneComposerAttachments(m.attachments)
	m.historySearchMentionBindings = append([]string(nil), m.composerMentionBindings...)
	m.clearComposerPasteWindow()
	m.resetInputHistoryNavigation()
	return true
}

// applyActiveHistorySearchKey owns the query and preview while Ctrl+R search is
// active. Background draft recovery edits the saved draft, not this preview.
func (m *Model) applyActiveHistorySearchKey(message bubbletea.KeyMsg, keySpec string) bool {
	if m == nil || m.historySearch == nil || !m.historySearch.Active {
		return false
	}
	search := m.historySearch
	switch {
	case m.keyMatches("composer", "history_search_previous", keySpec):
		draft, _ := search.SearchInDirection(composerhistory.HistorySearchOlder)
		m.showHistorySearchDraft(draft)
	case m.keyMatches("composer", "history_search_next", keySpec):
		draft, _ := search.SearchInDirection(composerhistory.HistorySearchNewer)
		m.showHistorySearchDraft(draft)
	case message.Type == bubbletea.KeyEnter:
		if draft, accepted := search.Accept(); accepted {
			m.restoreHistorySearchDraft(draft, m.historyElementsForText(draft.Text))
			m.endHistorySearch()
		}
	case message.Type == bubbletea.KeyEsc || message.Type == bubbletea.KeyCtrlC:
		m.restoreHistorySearchDraft(search.Cancel(), m.historySearchElements)
		m.attachments = cloneComposerAttachments(m.historySearchAttachments)
		m.composerMentionBindings = append([]string(nil), m.historySearchMentionBindings...)
		m.endHistorySearch()
	case message.Type == bubbletea.KeyBackspace || message.Type == bubbletea.KeyCtrlH:
		draft, _ := search.BackspaceQuery()
		m.showHistorySearchDraft(draft)
	case message.Type == bubbletea.KeyCtrlU:
		draft, _ := search.ClearQuery()
		m.showHistorySearchDraft(draft)
	case message.Type == bubbletea.KeyRunes && !message.Alt:
		draft, _ := search.AppendQueryRune(firstRune(message.Runes))
		m.showHistorySearchDraft(draft)
	default:
		// Search owns the composer until the user accepts or cancels.
	}
	return true
}

func firstRune(values []rune) rune {
	if len(values) == 0 {
		return 0
	}
	return values[0]
}

func (m *Model) currentComposerDraftState() composerhistory.DraftState {
	draft := *composerhistory.NewDraftState()
	if m == nil {
		return draft
	}
	draft.Text = m.composerText()
	draft.IsBashMode = m.composerPrefix == composerPrefixShell
	draft.Cursor = m.composerCursorByteOffset()
	return draft
}

func (m *Model) composerCursorByteOffset() int {
	if m == nil {
		return 0
	}
	lines := strings.Split(m.composer.Value(), "\n")
	row := m.composer.Line()
	offset := m.composerPrefix.len()
	for index := 0; index < row && index < len(lines); index++ {
		offset += len(lines[index]) + 1
	}
	if row >= 0 && row < len(lines) {
		column := m.composer.LineInfo().CharOffset
		for _, r := range lines[row] {
			if column <= 0 {
				break
			}
			offset += utf8.RuneLen(r)
			column--
		}
	}
	return offset
}

func (m *Model) showHistorySearchDraft(draft composerhistory.DraftState) {
	if m == nil || m.historySearch == nil {
		return
	}
	elements := m.historyElementsForText(draft.Text)
	if draft.Text == m.historySearchPreviewText {
		elements = cloneComposerTextElements(m.historySearchElements)
	}
	m.restoreHistorySearchDraft(draft, elements)
}

func (m *Model) restoreHistorySearchDraft(draft composerhistory.DraftState, elements []ComposerTextElement) {
	if m == nil {
		return
	}
	m.setComposerPrompt(draft.Text)
	m.composerElements = cloneComposerTextElements(elements)
	m.setCanonicalComposerCursor(draft.Cursor)
}

func (m *Model) setCanonicalComposerCursor(cursor int) {
	if m == nil {
		return
	}
	cursor -= m.composerPrefix.len()
	if cursor < 0 {
		cursor = 0
	}
	value := m.composer.Value()
	if cursor > len(value) {
		cursor = len(value)
	}
	for cursor > 0 && cursor < len(value) && !utf8.RuneStart(value[cursor]) {
		cursor--
	}
	m.setComposerCursorOffset(utf8.RuneCountInString(value[:cursor]))
}

func (m *Model) historyElementsForText(text string) []ComposerTextElement {
	if m == nil {
		return nil
	}
	for index := len(m.inputHistory) - 1; index >= 0; index-- {
		if m.inputHistory[index] == text {
			return cloneComposerTextElements(m.inputHistoryElementsAt(index))
		}
	}
	return nil
}

func (m *Model) endHistorySearch() {
	if m == nil {
		return
	}
	m.historySearch = nil
	m.historySearchElements = nil
	m.historySearchPreviewText = ""
	m.historySearchAttachments = nil
	m.historySearchMentionBindings = nil
}

// editHistorySearchSavedDraft applies a background draft update behind the
// active query while preserving the visible match/no-match preview.
func (m *Model) editHistorySearchSavedDraft(edit func(string) string) bool {
	if m == nil || m.historySearch == nil || !m.historySearch.Active || edit == nil {
		return false
	}
	m.historySearch.EditSavedDraft(func(draft *composerhistory.DraftState) {
		draft.Text = edit(draft.Text)
		draft.Cursor = len(draft.Text)
	})
	return true
}

func (m *Model) historySearchFooterLine() (string, bool) {
	if m == nil || m.historySearch == nil {
		return "", false
	}
	return m.historySearch.FooterStyledLine()
}
