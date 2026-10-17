package tea

import (
	"strings"
	"testing"

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
	for _, r := range text {
		updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	return model
}

// TestComposerRendersSlashPrefixAsPromptMarker pins #54742: the leading `/`
// moves into the prompt marker and out of the editable body while the
// canonical draft keeps it.
func TestComposerRendersSlashPrefixAsPromptMarker(t *testing.T) {
	model := prefixTestModel(t)
	model = typeComposerRunes(t, model, "/")

	if model.composerPrefix != composerPrefixSlash {
		t.Fatalf("prefix = %v, want slash", model.composerPrefix)
	}
	if body := model.composer.Value(); body != "" {
		t.Fatalf("body = %q, want empty", body)
	}
	if text := model.composerText(); text != "/" {
		t.Fatalf("composerText() = %q, want /", text)
	}
	if model.composer.Prompt != "/ " {
		t.Fatalf("prompt = %q, want %q", model.composer.Prompt, "/ ")
	}
	if model.ComposerValue() != "/" {
		t.Fatalf("ComposerValue() = %q, want /", model.ComposerValue())
	}

	model = typeComposerRunes(t, model, "help")
	if body := model.composer.Value(); body != "help" {
		t.Fatalf("body = %q, want help", body)
	}
	if text := model.composerText(); text != "/help" {
		t.Fatalf("composerText() = %q, want /help", text)
	}
	view := model.View()
	if !strings.Contains(view, "/ help") {
		t.Fatalf("composer view is missing the slash prompt marker:\n%s", view)
	}
	if model.composer.Value() != "help" {
		t.Fatalf("body = %q, want the slash removed from the editable text", model.composer.Value())
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
		if model.composer.Prompt != "> " {
			t.Fatalf("%q: prompt = %q, want %q", marker, model.composer.Prompt, "> ")
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

// TestComposerPromptImportAbsorbsPrefix pins setComposerPrompt, the path used
// by history, backtracking, queued-message restore and the external editor.
func TestComposerPromptImportAbsorbsPrefix(t *testing.T) {
	model := prefixTestModel(t)
	model.setComposerPrompt("/review src")
	if model.composerPrefix != composerPrefixSlash || model.composer.Value() != "review src" {
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
