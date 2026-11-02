package agentsoverview

import (
	"strings"
	"unicode/utf8"

	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/app/agents_overview_composer.rs and
// agents_overview_input.rs (#42428). The command center composes new tasks in
// the shared bottom-pane composer instead of a private single-line input, so
// multiline editing, paste handling, Vim mode, configured keybindings and
// drafts behave exactly like the chat composer.

// AgentsOverviewFocus mirrors Rust AgentsOverviewFocus (#42428). Go keeps the
// list as the zero value because the dashboard historically opened on the task
// list; a plain character still focuses the composer, matching Rust's
// non-Vim path.
type AgentsOverviewFocus int

const (
	AgentsOverviewFocusList AgentsOverviewFocus = iota
	AgentsOverviewFocusComposer
)

// AgentsOverviewComposerPlaceholder is the shared composer's empty prompt
// (Rust "Describe a new task").
const AgentsOverviewComposerPlaceholder = "Describe a new task"

// ComposerState returns the retained shared composer, creating it on first use
// so refreshes and reconnects never replace a draft (Rust
// sync_agents_overview_composer keeps `state.composer` alive).
func (v *View) ComposerState() *bottompane.ChatComposerState {
	if v == nil {
		return nil
	}
	if v.composer == nil {
		v.composer = bottompane.NewChatComposerState()
	}
	return v.composer
}

// FocusComposer gives the shared composer input focus (Rust focus_composer).
func (v *View) FocusComposer() {
	if v == nil {
		return
	}
	v.focus = AgentsOverviewFocusComposer
	v.ComposerState()
}

// FocusList returns input to the task list.
func (v *View) FocusList() {
	if v == nil {
		return
	}
	v.focus = AgentsOverviewFocusList
}

// Focus reports the current input owner.
func (v *View) Focus() AgentsOverviewFocus {
	if v == nil {
		return AgentsOverviewFocusList
	}
	return v.focus
}

// Composing reports whether the shared composer owns input (Rust composing:
// composer focus while neither metadata editor is open).
func (v *View) Composing() bool {
	return v != nil && v.focus == AgentsOverviewFocusComposer && !v.State.Searching && !v.State.Renaming
}

// ComposerOwnsEscape reports whether Escape edits the composer instead of
// returning to the list: Vim Insert must first leave insert mode (Rust
// composer_owns_escape).
func (v *View) ComposerOwnsEscape() bool {
	if v == nil {
		return false
	}
	return v.composerVimEnabled && v.composerVimInsert
}

// SetComposerVimEnabled mirrors the /vim setting onto the retained composer and
// reports whether it changed (Rust sync_agents_overview_composer).
func (v *View) SetComposerVimEnabled(enabled bool) bool {
	if v == nil {
		return false
	}
	changed := v.composerVimEnabled != enabled
	v.composerVimEnabled = enabled
	if !enabled {
		v.composerVimInsert = false
	} else if changed {
		v.composerVimInsert = true
	}
	return changed
}

// ComposerVimEnabled reports the composer's Vim preference.
func (v *View) ComposerVimEnabled() bool {
	return v != nil && v.composerVimEnabled
}

// SetComposerVimInsert switches the composer between Vim insert and normal
// mode so Escape is routed correctly.
func (v *View) SetComposerVimInsert(insert bool) {
	if v == nil {
		return
	}
	v.composerVimInsert = insert
}

// ComposerVimInsert reports whether the composer is in Vim insert mode.
func (v *View) ComposerVimInsert() bool {
	return v != nil && v.composerVimInsert
}

// ComposerText returns the retained draft exactly as typed; dispatch keeps its
// whitespace (Rust ChatComposerConfig::trim_submission = false).
func (v *View) ComposerText() string {
	if v == nil || v.composer == nil || v.composer.Draft == nil {
		return ""
	}
	return v.composer.Draft.Text
}

// ComposerEmpty mirrors the composer's is_empty, which ignores whitespace-only
// drafts and attachments.
func (v *View) ComposerEmpty() bool {
	if v == nil || v.composer == nil {
		return true
	}
	return v.composer.Draft.IsEmpty()
}

// ComposerCursor returns the draft's rune-safe cursor byte offset.
func (v *View) ComposerCursor() int {
	if v == nil || v.composer == nil || v.composer.Draft == nil {
		return 0
	}
	return v.composer.Draft.Cursor
}

// MoveComposerCursor moves the caret by delta bytes (negative moves left).
func (v *View) MoveComposerCursor(delta int) {
	if v == nil {
		return
	}
	v.ComposerState().Draft.MoveCursor(delta)
}

// SetComposerCursor places the caret at a byte offset.
func (v *View) SetComposerCursor(cursor int) {
	if v == nil || v.ComposerState().Draft == nil {
		return
	}
	draft := v.ComposerState().Draft
	draft.Cursor = cursor
	if draft.Cursor < 0 {
		draft.Cursor = 0
	}
	if draft.Cursor > len(draft.Text) {
		draft.Cursor = len(draft.Text)
	}
}

// InsertComposerText inserts text at the caret. Pasted text keeps its newlines
// (Rust handle_paste), so a multi-line paste stays one draft.
func (v *View) InsertComposerText(text string) bool {
	if v == nil || text == "" {
		return false
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	v.ComposerState().Draft.Insert(text)
	return true
}

// InsertComposerRune inserts one typed character.
func (v *View) InsertComposerRune(r rune) bool {
	if r == 0 {
		return false
	}
	return v.InsertComposerText(string(r))
}

// ComposerBackspace deletes the rune before the caret.
func (v *View) ComposerBackspace() bool {
	if v == nil {
		return false
	}
	return v.ComposerState().Draft.Backspace()
}

// ComposerDelete deletes the rune after the caret.
func (v *View) ComposerDelete() bool {
	if v == nil {
		return false
	}
	draft := v.ComposerState().Draft
	if draft == nil || draft.Cursor < 0 || draft.Cursor >= len(draft.Text) {
		return false
	}
	_, size := utf8.DecodeRuneInString(draft.Text[draft.Cursor:])
	draft.Text = draft.Text[:draft.Cursor] + draft.Text[draft.Cursor+size:]
	return true
}

// TakeComposerSubmission snapshots and clears the draft, returning the prompt
// verbatim. It is the shared composer's submit path with trim_submission off.
func (v *View) TakeComposerSubmission() (bottompane.ComposerSubmission, bool) {
	if v == nil || v.composer == nil {
		return bottompane.ComposerSubmission{}, false
	}
	return v.composer.Submit(false)
}

// ClearComposer empties the retained draft without recording history.
func (v *View) ClearComposer() {
	if v == nil {
		return
	}
	v.ComposerState().Draft.SnapshotAndClear()
}

// RestoreComposerPrompt restores an unsent prompt after a failed dispatch. Rust
// restore_agents_overview_prompt only restores into an empty composer and
// reports false so the caller can surface the text instead.
func (v *View) RestoreComposerPrompt(prompt string) bool {
	if v == nil || prompt == "" {
		return false
	}
	draft := v.ComposerState().Draft
	if draft == nil || !draft.IsEmpty() {
		return false
	}
	draft.Text = prompt
	draft.Cursor = len(prompt)
	return true
}

// composerRenderLines renders the composer block at the given width, bounded to
// three visible lines like Rust's input_height cap.
func (v *View) composerRenderLines(width int, styled bool) []string {
	if v == nil || width < 1 {
		return nil
	}
	const prefix = "\u203a "
	if v.ComposerText() == "" {
		placeholder := AgentsOverviewComposerPlaceholder
		if available := width - displayWidth(prefix); available > 0 {
			placeholder = truncateToWidth(placeholder, available)
		}
		return []string{renderLine("", []span{
			{text: prefix, style: spanCyanBold},
			{text: placeholder, style: spanDim},
		}, width, styled)}
	}
	textWidth := width - displayWidth(prefix)
	if textWidth < 1 {
		textWidth = 1
	}
	raw := strings.Split(v.ComposerText(), "\n")
	rendered := make([]string, 0, len(raw))
	for _, line := range raw {
		wrapped := wrapPreviewLines(line, textWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		rendered = append(rendered, wrapped...)
	}
	if len(rendered) > 3 {
		rendered = rendered[:3]
		rendered[2] = "\u2026"
	}
	lines := make([]string, 0, len(rendered))
	for i, line := range rendered {
		leader := "  "
		if i == 0 {
			leader = prefix
		}
		lines = append(lines, renderLine("", []span{
			{text: leader, style: spanCyanBold},
			{text: line, style: spanPlain},
		}, width, styled))
	}
	return lines
}
