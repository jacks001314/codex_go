package tea

import (
	"testing"

	composerhistory "codex_go/tui/bottom_pane/chat_composer"
	"codex_go/tui/keymsg"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer.rs #58412
// (275a9a89b6) "Keep exposed leading bangs literal in the chat composer".
// Shell mode starts when an edit inserts a leading `!`; deleting the start of
// `Yes! I agree.` exposes a `!` that must stay ordinary message text.

func TestDeletingPrefixKeepsBangLiteralLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model = typeComposerRunes(t, model, "Yes! I agree.")
	// Go routes Home to transcript paging, so the composer cursor moves directly;
	// the edit sequence is Rust's typing/Home/Delete.
	model.setComposerCursorOffset(0)
	updated, _ := model.Update(key(keymsg.KeyDelete))
	model = updated.(*Model)
	for i := 0; i < 3; i++ {
		if i > 0 {
			updated, _ = model.Update(key(keymsg.KeyDelete))
		}
		model = updated.(*Model)
	}
	if got := model.composerText(); got != "! I agree." {
		t.Fatalf("composerText() = %q, want %q", got, "! I agree.")
	}
	if model.composerPrefix != composerPrefixNone {
		t.Fatalf("prefix = %v, want none: deletion must not start shell mode", model.composerPrefix)
	}
	if model.isBangShellCommand() {
		t.Fatal("isBangShellCommand() = true, want false for an exposed bang")
	}

	// Moving elsewhere and inserting another bang must not reinterpret the draft.
	// Go routes End to transcript paging, so the cursor moves to the body end
	// directly; Rust reaches the same state with End.
	model.setComposerCursorOffset(len([]rune(model.composer.Value())))
	model = typeComposerRunes(t, model, "!")
	if model.composerPrefix != composerPrefixNone {
		t.Fatalf("prefix after trailing bang = %v, want none", model.composerPrefix)
	}
	if got := model.composerText(); got != "! I agree.!" {
		t.Fatalf("composerText() = %q, want %q", got, "! I agree.!")
	}

	updated, _ = model.Update(key(keymsg.KeyEnter))
	model = updated.(*Model)
	requests := model.SubmittedRequests()
	if len(requests) != 1 {
		t.Fatalf("submitted requests = %#v, want one", requests)
	}
	if requests[0].Prompt != "! I agree.!" {
		t.Fatalf("prompt = %q, want the literal bang kept", requests[0].Prompt)
	}
	if !requests[0].LiteralInput {
		t.Fatal("literal bang was not marked LiteralInput (Rust QueuedInputAction::Literal)")
	}
}

func TestDeletingWholePrefixLeavesNoBangLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model = typeComposerRunes(t, model, "Yes! I agree.")
	model.setComposerCursorOffset(0)
	updated, _ := model.Update(key(keymsg.KeyDelete))
	model = updated.(*Model)
	for i := 0; i < 4; i++ {
		if i > 0 {
			updated, _ = model.Update(key(keymsg.KeyDelete))
		}
		model = updated.(*Model)
	}
	if got := model.composerText(); got != " I agree." {
		t.Fatalf("composerText() = %q, want %q", got, " I agree.")
	}
	if model.composerPrefix != composerPrefixNone {
		t.Fatalf("prefix = %v, want none", model.composerPrefix)
	}
}

func TestTypedLeadingBangStartsShellModeLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model = typeComposerRunes(t, model, "!echo hi")
	if model.composerPrefix != composerPrefixShell {
		t.Fatalf("prefix = %v, want shell", model.composerPrefix)
	}
	if !model.isBangShellCommand() {
		t.Fatal("isBangShellCommand() = false, want true for a typed leading bang")
	}
	updated, _ := model.Update(key(keymsg.KeyEnter))
	model = updated.(*Model)
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != "!echo hi" || requests[0].LiteralInput {
		t.Fatalf("submitted = %#v, want shell prompt", requests)
	}

	// History recall restores the same shell mode (Rust ComposerDraft::is_bash_mode).
	updated, _ = model.Update(key(keymsg.KeyUp))
	model = updated.(*Model)
	if model.composerPrefix != composerPrefixShell || model.composer.Value() != "echo hi" {
		t.Fatalf("recalled prefix=%v body=%q, want shell body", model.composerPrefix, model.composer.Value())
	}
}

func TestLeadingSpaceBangStaysLiteralLikeRust(t *testing.T) {
	// Rust: `" !echo hi"` queues as literal input, `"!echo hi"` as shell input.
	literal := prefixTestModel(t)
	updated, _ := literal.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune(" !echo hi"), Paste: true})
	literal = updated.(*Model)
	if literal.composerPrefix != composerPrefixNone || literal.isBangShellCommand() {
		t.Fatalf("leading-space paste: prefix=%v shell=%v, want literal", literal.composerPrefix, literal.isBangShellCommand())
	}
	if got := literal.composerText(); got != " !echo hi" {
		t.Fatalf("composerText() = %q, want %q", got, " !echo hi")
	}
	literal.submitComposer()
	requests := literal.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != "!echo hi" || !requests[0].LiteralInput {
		t.Fatalf("submitted = %#v, want trimmed literal prompt", requests)
	}

	shell := prefixTestModel(t)
	updated, _ = shell.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("!echo hi"), Paste: true})
	shell = updated.(*Model)
	if shell.composerPrefix != composerPrefixShell {
		t.Fatalf("bang paste at the start: prefix=%v, want shell", shell.composerPrefix)
	}
}

func TestHistoryRecallKeepsExposedBangLiteralLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model.setComposerPromptLiteral("! I agree.")
	updated, _ := model.Update(key(keymsg.KeyEnter))
	model = updated.(*Model)
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != "! I agree." || !requests[0].LiteralInput {
		t.Fatalf("submitted = %#v, want literal bang prompt", requests)
	}

	updated, _ = model.Update(key(keymsg.KeyUp))
	model = updated.(*Model)
	if model.composerPrefix != composerPrefixNone {
		t.Fatalf("recalled prefix = %v, want none", model.composerPrefix)
	}
	if got := model.composerText(); got != "! I agree." {
		t.Fatalf("recalled composerText() = %q, want the literal draft", got)
	}
}

func TestHistorySearchDraftRestoresSavedBangModeLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	draft := *composerhistory.NewDraftState()
	draft.Text = "! I agree."
	draft.Cursor = len(draft.Text)
	draft.IsBashMode = false
	model.restoreHistorySearchDraft(draft, nil)
	if model.composerPrefix != composerPrefixNone || model.composerText() != "! I agree." {
		t.Fatalf("literal restore: prefix=%v text=%q", model.composerPrefix, model.composerText())
	}

	draft.IsBashMode = true
	model.restoreHistorySearchDraft(draft, nil)
	if model.composerPrefix != composerPrefixShell || model.composer.Value() != " I agree." {
		t.Fatalf("shell restore: prefix=%v body=%q", model.composerPrefix, model.composer.Value())
	}
}

func TestVimDeleteKeepsExposedBangLiteralLikeRust(t *testing.T) {
	model := vimTestModel("Yes! I agree.")
	for i := 0; i < 3; i++ {
		model = vimKeyPress(model, 'x')
	}
	if got := model.composer.Value(); got != "! I agree." {
		t.Fatalf("vim value = %q, want %q", got, "! I agree.")
	}
	if model.composerPrefix != composerPrefixNone || model.isBangShellCommand() {
		t.Fatalf("vim delete started shell mode: prefix=%v", model.composerPrefix)
	}

	// Vim undo restores the deleted prefix without entering shell mode either.
	model = vimKeyPress(model, 'u')
	if got := model.composer.Value(); got != "s! I agree." {
		t.Fatalf("vim undo value = %q, want the last deletion undone", got)
	}
	if model.composerPrefix != composerPrefixNone {
		t.Fatalf("vim undo prefix = %v, want none", model.composerPrefix)
	}
}

func TestTabSubmitsLiteralBangDraftLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model.setComposerPromptLiteral("! I agree.")
	updated, _ := model.Update(keymsg.KeyMsg{Type: keymsg.KeyTab})
	model = updated.(*Model)
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != "! I agree." || !requests[0].LiteralInput {
		t.Fatalf("tab submission = %#v, want the literal message submitted", requests)
	}
}
