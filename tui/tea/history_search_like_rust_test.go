package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestModelHistorySearchNavigationAcceptAndCancelLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.inputHistory = []string{"git status", "git diff"}
	model.composer.SetValue("draft")

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlR})
	if model.historySearch == nil || !model.historySearch.Active {
		t.Fatal("Ctrl+R did not open history search")
	}
	if view := strings.ToLower(model.View()); !strings.Contains(view, "reverse-i-search:") {
		t.Fatalf("history search footer missing:\n%s", view)
	}

	typeText(t, model, "git")
	if got := model.composerText(); got != "git diff" {
		t.Fatalf("first match = %q, want newest git diff", got)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlR})
	if got := model.composerText(); got != "git status" {
		t.Fatalf("older match = %q, want git status", got)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if model.historySearch != nil || model.composerText() != "git status" {
		t.Fatalf("accepted search = %#v composer=%q", model.historySearch, model.composerText())
	}

	cancelModel := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	cancelModel.inputHistory = []string{"git status", "git diff"}
	cancelModel.composer.SetValue("draft")
	cancelModel.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlR})
	typeText(t, cancelModel, "git")
	cancelModel.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if cancelModel.historySearch != nil || cancelModel.composerText() != "draft" {
		t.Fatalf("cancelled search = %#v composer=%q", cancelModel.historySearch, cancelModel.composerText())
	}

	miss := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	miss.inputHistory = []string{"git status"}
	miss.composer.SetValue("draft")
	miss.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlR})
	typeText(t, miss, "missing")
	miss.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if miss.historySearch == nil || !miss.historySearch.Active {
		t.Fatal("Enter without a match closed history search")
	}
	if got := miss.composerText(); got != "draft" {
		t.Fatalf("no-match preview = %q, want original draft", got)
	}
}

func TestHistorySearchBackgroundRecoveryPreservesSearchLikeRust(t *testing.T) {
	newModel := func() *Model {
		state := codextui.NewState(nil)
		model := NewModel(state, Options{Width: 100, Height: 30})
		model.inputHistory = []string{"earlier prompt"}
		model.composer.SetValue("$tool main draft")
		model.registerComposerElement("$tool main draft", 0, len("$tool"))
		model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlR})
		typeText(t, model, "earlier")
		if got := model.composerText(); got != "earlier prompt" {
			t.Fatalf("search preview = %q, want earlier prompt", got)
		}
		return model
	}

	cancel := newModel()
	cancel.appendQuestionDrafts([]string{"> Which way?\n\nanswer"})
	if cancel.historySearch == nil || !cancel.historySearch.Active {
		t.Fatal("background recovery closed history search")
	}
	if got := cancel.composerText(); got != "earlier prompt" {
		t.Fatalf("background recovery changed visible preview = %q", got)
	}
	typeText(t, cancel, "x")
	if got := cancel.composerText(); got != "$tool main draft" {
		t.Fatalf("query update leaked recovered draft into preview = %q", got)
	}
	cancel.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if got := cancel.composerText(); got != "$tool main draft\n\n> Which way?\n\nanswer" {
		t.Fatalf("cancel restored composer = %q", got)
	}
	if len(cancel.composerElements) != 1 || cancel.composerElements[0].Placeholder != "$tool" {
		t.Fatalf("composer elements after cancel = %#v", cancel.composerElements)
	}

	accept := newModel()
	accept.appendQuestionDrafts([]string{"> Which way?\n\nanswer"})
	accept.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if accept.historySearch != nil || accept.composerText() != "earlier prompt" {
		t.Fatalf("accepted preview = %#v composer=%q", accept.historySearch, accept.composerText())
	}

	fresh := newModel()
	fresh.resetComposer()
	fresh.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if fresh.historySearch != nil || fresh.composerText() != "" {
		t.Fatalf("fresh replacement retained search draft: %#v %q", fresh.historySearch, fresh.composerText())
	}
}
