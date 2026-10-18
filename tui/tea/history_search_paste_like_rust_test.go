package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/bottom_pane/chat_composer"
)

func historySearchPasteModel(history []string, draft string) *Model {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.inputHistory = append([]string(nil), history...)
	model.composer.SetValue(draft)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlR})
	return model
}

func pasteIntoModel(model *Model, text string) {
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune(text), Paste: true})
}

func TestHistorySearchPasteAppendsQueryAndAcceptsLikeRust(t *testing.T) {
	model := historySearchPasteModel([]string{"git status", "git log"}, "draft")
	pasteIntoModel(model, "git")
	if model.historySearch == nil || model.historySearch.Query != "git" {
		t.Fatalf("query after first paste = %#v", model.historySearch)
	}
	if got := model.composerText(); got != "git log" {
		t.Fatalf("newest preview = %q, want git log", got)
	}

	pasteIntoModel(model, " status")
	if model.historySearch.Query != "git status" {
		t.Fatalf("query after second paste = %q", model.historySearch.Query)
	}
	if got := model.composerText(); got != "git status" {
		t.Fatalf("matching preview = %q, want git status", got)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if model.historySearch != nil || model.composerText() != "git status" {
		t.Fatalf("accepted paste = %#v composer=%q", model.historySearch, model.composerText())
	}
}

func TestHistorySearchEmptyPastePreservesSelectedMatchLikeRust(t *testing.T) {
	model := historySearchPasteModel([]string{"git status", "git log"}, "draft")
	pasteIntoModel(model, "git")
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlR})
	selected := model.composerText()
	query := model.historySearch.Query

	pasteIntoModel(model, "")
	pasteIntoModel(model, "\x1b[31m\x1b[0m")

	if model.historySearch == nil || model.historySearch.Query != query {
		t.Fatalf("query changed by empty paste: %#v", model.historySearch)
	}
	if got := model.composerText(); got != selected {
		t.Fatalf("selected draft = %q, want %q", got, selected)
	}
}

func TestHistorySearchPasteShowsSeparatorsAndConsumesLargeTextLikeRust(t *testing.T) {
	model := historySearchPasteModel([]string{"first", "second"}, "draft")
	separators := "foo\nbar\tbaz"
	pasteIntoModel(model, separators)
	if model.historySearch.Query != separators {
		t.Fatalf("separator query = %q, want %q", model.historySearch.Query, separators)
	}
	footer, ok := model.historySearchFooterLine()
	if !ok || !strings.Contains(footer, "foo\u21b5bar\u21e5baz") {
		t.Fatalf("footer = %q ok=%v, want visible separators", footer, ok)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if got := model.composerText(); got != "draft" {
		t.Fatalf("cancel restored composer = %q", got)
	}

	large := strings.Repeat("x", chatcomposer.LargePasteCharThreshold+1)
	largeModel := historySearchPasteModel([]string{large}, "draft")
	pasteIntoModel(largeModel, large)
	if largeModel.historySearch == nil || largeModel.historySearch.Query != large {
		t.Fatalf("large paste was not appended as the full query")
	}
	if got := largeModel.composerText(); got != large {
		t.Fatalf("large paste preview length = %d, want %d", len([]rune(got)), len([]rune(large)))
	}
}

func TestHistorySearchPasteDoesNotAttachImagePathLikeRust(t *testing.T) {
	model := historySearchPasteModel([]string{"history.png"}, "draft")
	pasteIntoModel(model, "/tmp/history.png")
	if model.historySearch == nil || model.historySearch.Query != "/tmp/history.png" {
		t.Fatalf("image path query = %#v", model.historySearch)
	}
	if len(model.attachments) != 0 {
		t.Fatalf("history-search paste attached images: %#v", model.attachments)
	}
}
