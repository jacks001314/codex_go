package tea

import (
	"fmt"
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/keymsg"
)

// Rust #49357 (upstream 1983c48fd1), Rust test
// `blockquote_paste_continues_current_line_prefix`
// (codex-rs/tui/src/bottom_pane/chat_composer/blockquote_paste_tests.rs).
//
// The table is the Rust table verbatim, driven through the real bracketed-paste
// entry point (keymsg.KeyMsg with Paste set) rather than the helper alone.
func TestComposerBlockquotePasteContinuesCurrentLinePrefixLikeRust(t *testing.T) {
	for _, tc := range []struct {
		draft  string
		pasted string
		want   string
	}{
		{"> ", "first\nsecond", "> first\n> second\n\n"},
		// Rust #52418: a single trailing newline no longer adds an empty quoted
		// line (the CRLF/CR row below keeps its interior blank line quoted).
		{"> ", "first\n", "> first\n\n"},
		{"intro\n> ", "α\r\n\r\nβ\r", "intro\n> α\n> \n> β\n\n"},
		{"> existing ", "first\nsecond", "> existing first\n> second\n\n"},
		{"text > ", "first\nsecond", "text > first\nsecond"},
		{">", "first\nsecond", ">first\nsecond"},
		{"> quote\nplain ", "first\nsecond", "> quote\nplain first\nsecond"},
		{"> ", "single", "> single"},
		{"!> ", "out\npwd", "!> out\npwd"},
		// Rust #58412: a space before the bang keeps the draft in normal text mode.
		{" !echo hi\n> ", "out\npwd", " !echo hi\n> out\n> pwd\n\n"},
	} {
		state := codextui.NewState(nil)
		model := NewModel(state, Options{Width: 80, Height: 24})
		model.composer.SetValue(tc.draft)
		model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune(tc.pasted), Paste: true})
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
	model.composer.SetCursorColumn(len("> "))
	if got := composerCursorLinePrefix(&model.composer); got != "> " {
		t.Fatalf("cursor line prefix = %q, want %q", got, "> ")
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("first\nsecond"), Paste: true})
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
	model.composer.SetCursorColumn(1)
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("first\nsecond"), Paste: true})
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
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("first\nsecond"), Paste: true})
	model.composer.InsertString("Next block")
	if got := model.composer.Value(); got != "> first\n> second\n\nNext block" {
		t.Fatalf("embedded answer text = %q, want %q", got, "> first\n> second\n\nNext block")
	}
	answer, ok := model.asyncQuestions.AnswerText(model.composer.Value())
	if !ok || answer != "> first\n> second\n\nNext block" {
		t.Fatalf("AnswerText = %q, %v; want the quoted draft", answer, ok)
	}
}

// Rust test `blockquote_paste_large_expands_quoted_text_on_submit`: the quoted
// text is stored behind a `[Pasted Content N chars]` element and expanded on
// submit (chat_composer.rs LARGE_PASTE_CHAR_THRESHOLD, blockquote_paste_tests.rs:56).
func TestComposerBlockquotePasteLargeExpandsQuotedTextOnSubmitLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	first := strings.Repeat("x", 1000)
	model.composer.SetValue("> ")
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune(first + "\nsecond"), Paste: true})
	quoted := first + "\n> second"
	placeholder := fmt.Sprintf("[Pasted Content %d chars]", len([]rune(quoted)))
	if got, want := model.composer.Value(), "> "+placeholder+"\n\n"; got != want {
		t.Fatalf("large blockquote paste = %q, want %q", truncateForFailure(got), truncateForFailure(want))
	}
	if len(model.pendingPastes) != 1 || model.pendingPastes[0].Content != quoted {
		t.Fatalf("pending pastes = %#v, want the quoted text", model.pendingPastes)
	}
	model.composer.InsertString("Next block")
	model.clearComposerPasteWindow()
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyEnter})
	if len(model.submitRequests) != 1 {
		t.Fatalf("submissions = %d, want 1", len(model.submitRequests))
	}
	if got, want := model.submitRequests[0].Prompt, "> "+quoted+"\n\nNext block"; got != want {
		t.Fatalf("submitted = %q, want %q", truncateForFailure(got), truncateForFailure(want))
	}
}

func truncateForFailure(value string) string {
	if len(value) <= 80 {
		return value
	}
	return value[:80] + "..."
}
