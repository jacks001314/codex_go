package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

func TestAgentsOverviewRenameUsesVimSingleLineEditorLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.agentsOverview = agentsoverview.New([]agentsoverview.Row{{
		ThreadID: "t-1",
		Name:     "alpha",
	}}, "t-1", false)
	model.agentsOverview.State.VimEnabled = true
	if !model.agentsOverview.BeginRename() {
		t.Fatal("BeginRename returned false")
	}
	model.updateAgentsOverviewKey(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.agentsOverview.State.VimInsert {
		t.Fatal("Esc did not enter Vim normal mode")
	}
	model.updateAgentsOverviewKey(runes("0"))
	model.updateAgentsOverviewKey(runes("x"))
	if got := model.agentsOverview.State.Input; got != "lpha" {
		t.Fatalf("normal-mode delete = %q, want lpha", got)
	}
	model.updateAgentsOverviewKey(runes("i"))
	model.updateAgentsOverviewKey(runes("Z"))
	if got := model.agentsOverview.State.Input; got != "Zlpha" {
		t.Fatalf("insert mode = %q, want Zlpha", got)
	}
	model.updateAgentsOverviewKey(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	model.updateAgentsOverviewKey(runes("A"))
	model.updateAgentsOverviewKey(runes("!"))
	if got := model.agentsOverview.State.Input; got != "Zlpha!" {
		t.Fatalf("append at line end = %q, want Zlpha!", got)
	}
}
