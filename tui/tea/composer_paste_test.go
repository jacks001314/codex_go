package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Rust #49357 (upstream 1983c48fd1), Rust test
// `blockquote_paste_continues_current_line_prefix`
// (codex-rs/tui/src/bottom_pane/chat_composer/blockquote_paste_tests.rs).
//
// The table is the Rust table verbatim, driven through the real bracketed-paste
// entry point (bubbletea.KeyMsg with Paste set) rather than the helper alone.
func TestComposerBlockquotePasteContinuesCurrentLinePrefixLikeRust(t *testing.T) {
	for _, tc := range []struct {
		draft  string
		pasted string
		want   string
	}{
		{"> ", "first\nsecond", "> first\n> second\n\n"},
		{"intro\n> ", "α\r\n\r\nβ\r", "intro\n> α\n> \n> β\n> \n\n"},
		{"> existing ", "first\nsecond", "> existing first\n> second\n\n"},
		{"text > ", "first\nsecond", "text > first\nsecond"},
		{">", "first\nsecond", ">first\nsecond"},
		{"> quote\nplain ", "first\nsecond", "> quote\nplain first\nsecond"},
		{"> ", "single", "> single"},
		{"!> ", "out\npwd", "!> out\npwd"},
		{" !echo hi\n> ", "out\npwd", " !echo hi\n> out\npwd"},
	} {
		state := codextui.NewState(nil)
		model := NewModel(state, Options{Width: 80, Height: 24})
		model.composer.SetValue(tc.draft)
		model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune(tc.pasted), Paste: true})
		if got := model.composer.Value(); got != tc.want {
			t.Errorf("draft %q + paste %q = %q, want %q", tc.draft, tc.pasted, got, tc.want)
		}
	}
}

// Rust test `blockquote_paste_uses_cursor_line_and_preserves_suffix`: the
// continuation follows the line under the cursor, and the text after the cursor
// survives the inserted quote and its two trailing newlines.
func TestComposerBlockquotePasteUsesCursorLineAndPreservesSuffixLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.composer.SetValue("intro\n> suffix\nend")
	// Cursor lands right after the "> " marker on the second line.
	model.composer.CursorUp()
	model.composer.SetCursor(len("> "))
	if got := composerCursorLinePrefix(&model.composer); got != "> " {
		t.Fatalf("cursor line prefix = %q, want %q", got, "> ")
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("first\nsecond"), Paste: true})
	if got := model.composer.Value(); got != "intro\n> first\n> second\n\nsuffix\nend" {
		t.Fatalf("composer text = %q, want %q", got, "intro\n> first\n> second\n\nsuffix\nend")
	}
	model.composer.InsertString("next ")
	if got := model.composer.Value(); got != "intro\n> first\n> second\n\nnext suffix\nend" {
		t.Fatalf("composer text = %q, want %q", got, "intro\n> first\n> second\n\nnext suffix\nend")
	}
}

// The cursor sits inside the marker (after ">" but before the space), which Rust
// treats as literal because the prefix under the insertion point is not "> ".
func TestComposerBlockquotePasteRejectsPartialMarkerLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.composer.SetValue("> abc")
	model.composer.SetCursor(1)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("first\nsecond"), Paste: true})
	if got := model.composer.Value(); got != ">first\nsecond abc" {
		t.Fatalf("composer text = %q, want literal paste at the cursor", got)
	}
}

// Rust test `blockquote_paste_embedded_answer_starts_next_block`: Go keeps one
// composer for the main prompt and the embedded answer fields (Rust keeps a
// separate `plain_text()` composer), so the async-question free-text answer
// inherits the same continuation.
func TestComposerBlockquotePasteEmbeddedAnswerStartsNextBlockLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.asyncQuestions.Append("msg-1", []bottompane.AsyncUserInputQuestion{{
		Title:   "Pick a target",
		Options: []string{"one", "two"},
	}})
	model.asyncQuestions.SelectOther()
	model.composer.SetValue("> ")
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("first\nsecond"), Paste: true})
	model.composer.InsertString("Next block")
	if got := model.composer.Value(); got != "> first\n> second\n\nNext block" {
		t.Fatalf("embedded answer text = %q, want %q", got, "> first\n> second\n\nNext block")
	}
	answer, ok := model.asyncQuestions.AnswerText(model.composer.Value())
	if !ok || answer != "> first\n> second\n\nNext block" {
		t.Fatalf("AnswerText = %q, %v; want the quoted draft", answer, ok)
	}
}

// Rust test `blockquote_paste_large_expands_quoted_text_on_submit`: Rust stores
// the quoted text behind a `[Pasted Content N chars]` element and expands it on
// submit. The Go composer inserts pastes directly (no element/placeholder path),
// so the quoted text stays inline; the continuation itself is identical.
func TestComposerBlockquotePasteLargePasteStaysInlineLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	first := strings.Repeat("x", 1200)
	model.composer.SetValue("> ")
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune(first + "\nsecond"), Paste: true})
	want := "> " + first + "\n> second\n\n"
	if got := model.composer.Value(); got != want {
		t.Fatalf("large blockquote paste = %q, want the quoted text inline", truncateForFailure(got))
	}
	if got := model.composer.Value(); strings.Contains(got, "[Pasted Content") {
		t.Fatalf("Go composer has no large-paste placeholder path, got %q", truncateForFailure(got))
	}
}

func truncateForFailure(value string) string {
	if len(value) <= 80 {
		return value
	}
	return value[:80] + "..."
}
