package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func prefixTestModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	return model
}

func typeComposerRunes(t *testing.T, model *Model, text string) *Model {
	t.Helper()
	// Space the keystrokes like a person typing: the paste-burst detector treats
	// sub-8ms character runs as pasted input and would turn the trailing Enter
	// into a newline.
	clock := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	model.now = func() time.Time { return clock }
	for _, r := range text {
		clock = clock.Add(20 * time.Millisecond)
		updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	return model
}

// TestComposerKeepsSlashPrefixInEditableText pins #56021: the leading `/`
// stays in the textarea and renders after the normal composer prompt.
func TestComposerKeepsSlashPrefixInEditableText(t *testing.T) {
	model := prefixTestModel(t)
	model = typeComposerRunes(t, model, "/")

	if model.composerPrefix != composerPrefixNone {
		t.Fatalf("prefix = %v, want none", model.composerPrefix)
	}
	if body := model.composer.Value(); body != "/" {
		t.Fatalf("body = %q, want /", body)
	}
	if text := model.composerText(); text != "/" {
		t.Fatalf("composerText() = %q, want /", text)
	}
	if model.composer.Prompt != "› " {
		t.Fatalf("prompt = %q, want %q", model.composer.Prompt, "› ")
	}
	if model.ComposerValue() != "/" {
		t.Fatalf("ComposerValue() = %q, want /", model.ComposerValue())
	}

	model = typeComposerRunes(t, model, "help")
	if body := model.composer.Value(); body != "/help" {
		t.Fatalf("body = %q, want /help", body)
	}
	if text := model.composerText(); text != "/help" {
		t.Fatalf("composerText() = %q, want /help", text)
	}
	view := model.View()
	if !strings.Contains(view, "› /help") {
		t.Fatalf("composer view is missing the editable slash command:\n%s", view)
	}
	if model.composer.Value() != "/help" {
		t.Fatalf("body = %q, want the slash kept in the editable text", model.composer.Value())
	}
}

// TestComposerAbsorbsShellPrefix pins the shared `!` prompt state.
func TestComposerAbsorbsShellPrefix(t *testing.T) {
	model := prefixTestModel(t)
	model = typeComposerRunes(t, model, "!")

	if model.composerPrefix != composerPrefixShell {
		t.Fatalf("prefix = %v, want shell", model.composerPrefix)
	}
	if model.composer.Value() != "" {
		t.Fatalf("body = %q, want empty", model.composer.Value())
	}
	if model.composer.Prompt != "! " {
		t.Fatalf("prompt = %q, want %q", model.composer.Prompt, "! ")
	}
	if model.composerText() != "!" {
		t.Fatalf("composerText() = %q, want !", model.composerText())
	}
}

// TestComposerBackspaceRemovesPromptPrefix pins backward deletion at the start
// of the body: the marker disappears and the body is untouched.
func TestComposerBackspaceRemovesPromptPrefix(t *testing.T) {
	for _, marker := range []string{"/", "!"} {
		model := prefixTestModel(t)
		model = typeComposerRunes(t, model, marker)
		updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
		model = updated.(*Model)
		if model.composerPrefix != composerPrefixNone {
			t.Fatalf("%q: prefix = %v, want none", marker, model.composerPrefix)
		}
		if model.composerText() != "" {
			t.Fatalf("%q: composerText() = %q, want empty", marker, model.composerText())
		}
		if model.composer.Prompt != "› " {
			t.Fatalf("%q: prompt = %q, want %q", marker, model.composer.Prompt, "› ")
		}
	}

	// A marker with a body only drops the marker.
	model := prefixTestModel(t)
	model = typeComposerRunes(t, model, "!echo")
	if model.composer.Value() != "echo" {
		t.Fatalf("body = %q, want echo", model.composer.Value())
	}
	model.composer.SetCursor(0)
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
	model = updated.(*Model)
	if model.composerPrefix != composerPrefixNone || model.composer.Value() != "echo" {
		t.Fatalf("after backspace: prefix=%v body=%q", model.composerPrefix, model.composer.Value())
	}
}

// TestComposerSubmitAndHistoryKeepPromptPrefix pins the canonical round trip:
// submission stores `!cmd`, and history recall puts the marker back into the
// prompt instead of the body.
func TestComposerSubmitAndHistoryKeepPromptPrefix(t *testing.T) {
	model := prefixTestModel(t)
	model = typeComposerRunes(t, model, "!echo hi")
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)

	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != "!echo hi" {
		t.Fatalf("submitted = %#v, want !echo hi", requests)
	}
	if model.composerPrefix != composerPrefixNone || model.composer.Value() != "" {
		t.Fatalf("composer not reset: prefix=%v body=%q", model.composerPrefix, model.composer.Value())
	}

	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp})
	model = updated.(*Model)
	if model.composerPrefix != composerPrefixShell {
		t.Fatalf("recalled prefix = %v, want shell", model.composerPrefix)
	}
	if body := model.composer.Value(); body != "echo hi" {
		t.Fatalf("recalled body = %q, want %q", body, "echo hi")
	}
	if text := model.composerText(); text != "!echo hi" {
		t.Fatalf("recalled composerText() = %q, want %q", text, "!echo hi")
	}
}

// TestComposerPromptImportKeepsSlashText pins setComposerPrompt, the path used
// by history, backtracking, queued-message restore and the external editor.
func TestComposerPromptImportAbsorbsPrefix(t *testing.T) {
	model := prefixTestModel(t)
	model.setComposerPrompt("/review src")
	if model.composerPrefix != composerPrefixNone || model.composer.Value() != "/review src" {
		t.Fatalf("imported prefix=%v body=%q", model.composerPrefix, model.composer.Value())
	}

	// Rust matches the first extended grapheme cluster, so `/` plus a combining
	// mark stays literal text.
	model.setComposerPrompt("/\u0301help")
	if model.composerPrefix != composerPrefixNone {
		t.Fatalf("combining mark absorbed: prefix = %v", model.composerPrefix)
	}
	if model.composer.Value() != "/\u0301help" {
		t.Fatalf("combining-mark body = %q", model.composer.Value())
	}

	model.setComposerPrompt("plain")
	if model.composerPrefix != composerPrefixNone || model.composer.Value() != "plain" {
		t.Fatalf("plain import prefix=%v body=%q", model.composerPrefix, model.composer.Value())
	}
}

// TestComposerSlashPromptKeepsTextElementOffsetsLikeRust pins that slash text
// stays in the editable body, so tracked ranges need no prompt shift.
func TestComposerSlashPromptKeepsTextElementOffsetsLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model.setComposerPrompt("/tmp/$imagegen file")
	model.registerComposerElement(model.composer.Value(), len("/tmp/"), len("/tmp/$imagegen"))
	if got := model.composerText(); got != "/tmp/$imagegen file" {
		t.Fatalf("draft = %q, want /tmp/$imagegen file", got)
	}
	if model.readOnlyThread {
		t.Fatal("test model unexpectedly read-only")
	}

	model.submitComposer()
	requests := model.SubmittedRequests()
	if len(requests) != 1 {
		t.Fatalf("submitted requests = %#v, want one", requests)
	}
	request := requests[0]
	if request.Prompt != "/tmp/$imagegen file" {
		t.Fatalf("prompt = %q, want /tmp/$imagegen file", request.Prompt)
	}
	if len(request.TextElements) != 1 {
		t.Fatalf("text elements = %#v, want one", request.TextElements)
	}
	element := request.TextElements[0]
	wantStart := len("/tmp/")
	if element.Start != wantStart || element.End != wantStart+len("$imagegen") || element.Placeholder != "$imagegen" {
		t.Fatalf("text element = %#v, want canonical range %d..%d", element, wantStart, wantStart+len("$imagegen"))
	}
}

// TestComposerHistoryRecallRestoresTextElementsLikeRust covers the history
// round trip: recalling a slash-command prompt keeps structured ranges aligned
// with the editable body.
func TestComposerHistoryRecallRestoresTextElementsLikeRust(t *testing.T) {
	model := prefixTestModel(t)
	model.setComposerPrompt("/tmp/$imagegen file")
	model.registerComposerElement(model.composer.Value(), len("/tmp/"), len("/tmp/$imagegen"))

	model.submitComposer()
	updated, _ := model.Update(key(bubbletea.KeyUp))
	model = updated.(*Model)

	if model.composerPrefix != composerPrefixNone || model.composer.Value() != "/tmp/$imagegen file" {
		t.Fatalf("recalled prefix=%v body=%q", model.composerPrefix, model.composer.Value())
	}
	if len(model.composerElements) != 1 {
		t.Fatalf("recalled elements = %#v, want one", model.composerElements)
	}
	element := model.composerElements[0]
	if element.Start != len("/tmp/") || element.End != len("/tmp/$imagegen") || element.Placeholder != "$imagegen" {
		t.Fatalf("recalled element = %#v", element)
	}
}

// TestComposerPrefixCountsAsNonEmpty pins is_empty: a bare marker is a draft,
// so the passive warning row and queued-message restore must not treat it as
// an empty composer.
func TestComposerPrefixCountsAsNonEmpty(t *testing.T) {
	model := prefixTestModel(t)
	if !model.composerIsEmpty() {
		t.Fatal("fresh composer should be empty")
	}
	model = typeComposerRunes(t, model, "/")
	if model.composerIsEmpty() {
		t.Fatal("a bare slash marker must not count as empty")
	}
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
	model = updated.(*Model)
	if !model.composerIsEmpty() {
		t.Fatal("clearing the marker should restore the empty state")
	}
}
