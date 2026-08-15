package bottompane

import (
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/bottom_pane/custom_prompt_view.rs.

// NormalizePastedText mirrors Rust #38704: pasted text may contain CRLF pairs
// or bare CRs (e.g., iTerm2), but the composer expects LF. Normalize CRLF
// pairs before bare CRs so each pasted line break becomes one LF and existing
// LFs stay unchanged.
func NormalizePastedText(pasted string) string {
	return strings.ReplaceAll(strings.ReplaceAll(pasted, "\r\n", "\n"), "\r", "\n")
}

type CustomPromptCompletion string

const (
	CustomPromptPending   CustomPromptCompletion = ""
	CustomPromptAccepted  CustomPromptCompletion = "accepted"
	CustomPromptCancelled CustomPromptCompletion = "cancelled"
)

type CustomPromptView struct {
	Title        string
	Placeholder  string
	Text         string
	ContextLabel string

	Submitted  []string
	Completion CustomPromptCompletion

	pasteBurst PasteBurst
	cursor     int
}

func NewCustomPromptView(title string, placeholder string, initialText string, contextLabel string) *CustomPromptView {
	view := &CustomPromptView{
		Title:        title,
		Placeholder:  placeholder,
		Text:         initialText,
		ContextLabel: contextLabel,
		cursor:       len([]rune(initialText)),
	}
	return view
}

func (v *CustomPromptView) HandleKey(key string) {
	v.HandleKeyAt(key, time.Now())
}

func (v *CustomPromptView) HandleKeyAt(key string, now time.Time) {
	if v == nil || v.IsComplete() {
		return
	}
	key = normalizeCustomPromptKey(key)
	switch key {
	case "esc", "escape", "ctrl-c":
		v.Completion = CustomPromptCancelled
	case "enter":
		if v.pasteBurst.DirectInsertNewlineShouldInsert(now) {
			v.pasteBurst.ExtendWindow(now)
			v.InsertString("\n")
			return
		}
		v.Submit()
	case "backspace":
		v.Backspace()
	case "tab":
		return
	default:
		if strings.HasPrefix(key, "paste:") {
			v.HandlePaste(strings.TrimPrefix(key, "paste:"))
		}
	}
}

func (v *CustomPromptView) HandleRuneAt(ch rune, now time.Time) {
	if v == nil || v.IsComplete() {
		return
	}
	_, pasteLikeBurst := v.pasteBurst.OnPlainCharNoHold(now)
	v.InsertString(string(ch))
	if pasteLikeBurst {
		v.pasteBurst.ExtendWindow(now)
	}
}

func (v *CustomPromptView) HandleTextAt(text string, start time.Time, step time.Duration) {
	now := start
	for _, ch := range text {
		v.HandleRuneAt(ch, now)
		now = now.Add(step)
	}
}

func (v *CustomPromptView) HandlePaste(pasted string) bool {
	if v == nil || pasted == "" || v.IsComplete() {
		return false
	}
	// Rust #38704: CRLF pairs and bare CRs normalize to LF so each pasted
	// line break becomes a single newline.
	pasted = NormalizePastedText(pasted)
	v.InsertString(pasted)
	v.pasteBurst.ClearAfterExplicitPaste()
	return true
}

func (v *CustomPromptView) Submit() bool {
	if v == nil || v.IsComplete() {
		return false
	}
	text := strings.TrimSpace(v.Text)
	if text == "" {
		return false
	}
	v.Submitted = append(v.Submitted, text)
	v.Completion = CustomPromptAccepted
	return true
}

func (v *CustomPromptView) Cancel() {
	if v == nil || v.IsComplete() {
		return
	}
	v.Completion = CustomPromptCancelled
}

func (v *CustomPromptView) IsComplete() bool {
	return v != nil && v.Completion != CustomPromptPending
}

func (v *CustomPromptView) LastSubmitted() (string, bool) {
	if v == nil || len(v.Submitted) == 0 {
		return "", false
	}
	return v.Submitted[len(v.Submitted)-1], true
}

func (v *CustomPromptView) InsertString(text string) {
	if v == nil || text == "" {
		return
	}
	runes := []rune(v.Text)
	if v.cursor < 0 || v.cursor > len(runes) {
		v.cursor = len(runes)
	}
	insert := []rune(text)
	next := make([]rune, 0, len(runes)+len(insert))
	next = append(next, runes[:v.cursor]...)
	next = append(next, insert...)
	next = append(next, runes[v.cursor:]...)
	v.Text = string(next)
	v.cursor += len(insert)
}

func (v *CustomPromptView) Backspace() {
	if v == nil || v.cursor <= 0 {
		return
	}
	runes := []rune(v.Text)
	if v.cursor > len(runes) {
		v.cursor = len(runes)
	}
	next := make([]rune, 0, len(runes)-1)
	next = append(next, runes[:v.cursor-1]...)
	next = append(next, runes[v.cursor:]...)
	v.Text = string(next)
	v.cursor--
}

func (v *CustomPromptView) Rows() []string {
	if v == nil {
		return nil
	}
	rows := []string{v.Title}
	if strings.TrimSpace(v.ContextLabel) != "" {
		rows = append(rows, v.ContextLabel)
	}
	if v.Text == "" {
		rows = append(rows, v.Placeholder)
	} else {
		rows = append(rows, strings.Split(v.Text, "\n")...)
	}
	rows = append(rows, "", "Press enter to submit or esc to cancel")
	return rows
}

func normalizeCustomPromptKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "+", "-")
	return key
}
