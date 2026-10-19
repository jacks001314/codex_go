package tea

import (
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// PromptSuggestionRequest identifies one completed turn whose follow-up
// suggestion may be applied (Rust #47911 SuggestionRequest).
type PromptSuggestionRequest struct {
	ThreadID string
	TurnID   string
	Summary  string
	ID       uint64
}

// PromptSuggestionResultMsg carries a model response for one request.
type PromptSuggestionResultMsg struct {
	Request  PromptSuggestionRequest
	Response string
	Err      error
}

type promptSuggestionState struct {
	request   PromptSuggestionRequest
	text      string
	cancelled bool
}

const maxPromptSuggestionRunes = 240

// ParsePromptSuggestion strictly decodes Rust's `{"suggestion":"..."}`
// response. Unknown fields, non-string suggestions, control characters,
// paragraph separators, and responses over 240 runes are rejected.
func ParsePromptSuggestion(response string) (string, bool) {
	var decoded struct {
		Suggestion any `json:"suggestion"`
	}
	decoder := json.NewDecoder(strings.NewReader(response))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return "", false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", false
	}
	text, ok := decoded.Suggestion.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxPromptSuggestionRunes {
		return "", false
	}
	for _, r := range text {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return "", false
		}
	}
	return text, true
}

// setPromptSuggestion starts one suggestion request. Generation is not wired
// yet, matching Rust's #47911 foundation; hosts/tests can still install a
// request and deliver a result.
func (m *Model) setPromptSuggestion(request PromptSuggestionRequest) {
	if m == nil || !m.promptSuggestionsEnabled {
		return
	}
	request.ThreadID = strings.TrimSpace(request.ThreadID)
	request.TurnID = strings.TrimSpace(request.TurnID)
	if request.ThreadID == "" || request.TurnID == "" {
		m.clearPromptSuggestion()
		return
	}
	if request.ID == 0 {
		m.nextPromptSuggestionID++
		request.ID = m.nextPromptSuggestionID
	}
	m.promptSuggestion = &promptSuggestionState{request: request}
}

// applyPromptSuggestionResult accepts only the current request's result.
func (m *Model) applyPromptSuggestionResult(msg PromptSuggestionResultMsg) {
	if m == nil || m.promptSuggestion == nil {
		return
	}
	request := m.promptSuggestion.request
	if msg.Request.ID != request.ID ||
		strings.TrimSpace(msg.Request.ThreadID) != request.ThreadID ||
		strings.TrimSpace(msg.Request.TurnID) != request.TurnID {
		return
	}
	if msg.Err != nil {
		m.clearPromptSuggestion()
		return
	}
	text, ok := ParsePromptSuggestion(msg.Response)
	if !ok {
		m.clearPromptSuggestion()
		return
	}
	m.promptSuggestion.text = text
}

func (m *Model) clearPromptSuggestion() {
	if m == nil || m.promptSuggestion == nil {
		return
	}
	m.promptSuggestion.cancelled = true
	m.promptSuggestion = nil
}

// visiblePromptSuggestion returns the dim suggestion that may replace the
// empty-composer placeholder (Rust ChatComposer::visible_prompt_suggestion).
func (m *Model) visiblePromptSuggestion() string {
	if m == nil || !m.promptSuggestionsEnabled || m.promptSuggestion == nil ||
		m.promptSuggestion.cancelled || m.promptSuggestion.text == "" {
		return ""
	}
	if !m.terminalFocused || !m.composerIsEmpty() || len(m.attachments) > 0 ||
		len(m.composerElements) > 0 || m.modal != nil || m.overlay != nil ||
		m.mentionPopup != nil || m.slashPopup.Active || m.skillPopup.Active ||
		m.isUserTurnPendingOrRunning() || m.reviewState.IsReviewMode ||
		m.inPasteBurst() || m.composerHasInteractiveFooter() {
		return ""
	}
	if current := strings.TrimSpace(m.State.ThreadID); current != "" &&
		current != m.promptSuggestion.request.ThreadID {
		return ""
	}
	return m.promptSuggestion.text
}

// handlePromptSuggestionKey implements Escape cancellation and Tab acceptance.
// Tab yields to any explicit editor, Vim-normal, or composer binding.
func (m *Model) handlePromptSuggestionKey(msg bubbletea.KeyMsg, keySpec string) bool {
	if m == nil || m.promptSuggestion == nil {
		return false
	}
	if msg.Type == bubbletea.KeyEsc {
		m.clearPromptSuggestion()
		return true
	}
	if msg.Type != bubbletea.KeyTab || msg.Alt || msg.Paste {
		return false
	}
	for _, action := range codextui.KeymapActions(codextui.KeymapActionFilter{}) {
		if action.Context != "editor" && action.Context != "vim_normal" && action.Context != "composer" {
			continue
		}
		bindings, _, custom := codextui.ResolvedKeymapBindings(m.keymapConfig, action.Context, action.Action)
		if !custom {
			continue
		}
		for _, binding := range bindings {
			if binding == keySpec {
				return false
			}
		}
	}
	text := m.visiblePromptSuggestion()
	if text == "" {
		return false
	}
	m.clearPromptSuggestion()
	m.insertComposerText(text)
	m.promptSuggestionTabAccepted = true
	return true
}

// insertComposerText inserts suggestion text without submitting it.
func (m *Model) insertComposerText(text string) {
	if m == nil || text == "" {
		return
	}
	m.composer.InsertString(text)
	m.composerElements = nil
	m.refreshSlashPopup()
}

// inPasteBurst reports whether Tab is currently owned by paste-burst handling.
func (m *Model) inPasteBurst() bool {
	if m == nil {
		return false
	}
	now := m.currentTime()
	return m.composerPasteEnterUntil != nil && !now.After(*m.composerPasteEnterUntil)
}

// composerHasInteractiveFooter mirrors Rust's interactive-footer gate.
func (m *Model) composerHasInteractiveFooter() bool {
	if m == nil {
		return false
	}
	return m.quickDialMenu || m.slashPopup.Active || m.skillPopup.Active ||
		m.warningsView != nil || m.securitySetupBannerVisible()
}
