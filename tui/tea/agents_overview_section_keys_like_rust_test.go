package tea

import (
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/keymsg"
)

// Rust agents.next_group / previous_group default to shift+down|shift+j and
// shift+up|shift+k and jump to the first task of an adjacent group
// (keymap.rs:2206, agents_overview_view.rs:726).
func TestModelAgentsSectionKeysLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return []agentsoverview.Row{
				{ThreadID: "a1", Name: "alpha", CWD: "/work/a", Group: agentsoverview.GroupWorking},
				{ThreadID: "a2", Name: "alpha2", CWD: "/work/a", Group: agentsoverview.GroupWorking},
				{ThreadID: "b1", Name: "beta", CWD: "/work/b", Group: agentsoverview.GroupReady},
				{ThreadID: "b2", Name: "beta2", CWD: "/work/b", Group: agentsoverview.GroupReady},
			}, nil
		},
	})
	openAgentsDashboard(t, model)
	if got := model.agentsOverview.SelectedThreadID(); got != "a1" {
		t.Fatalf("initial selection = %q, want the first task", got)
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyShiftDown})
	if got := model.agentsOverview.SelectedThreadID(); got != "b1" {
		t.Fatalf("shift+down selected %q, want the next group's first task", got)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyShiftDown})
	if got := model.agentsOverview.SelectedThreadID(); got != "b1" {
		t.Fatalf("shift+down at the end selected %q, want to clamp", got)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyShiftUp})
	if got := model.agentsOverview.SelectedThreadID(); got != "a1" {
		t.Fatalf("shift+up selected %q, want the previous group's first task", got)
	}

	// The letter bindings resolve from the uppercase rune, like Rust's
	// shift+char chords.
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("J")})
	if got := model.agentsOverview.SelectedThreadID(); got != "b1" {
		t.Fatalf("shift+j selected %q, want the next group's first task", got)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("K")})
	if got := model.agentsOverview.SelectedThreadID(); got != "a1" {
		t.Fatalf("shift+k selected %q, want the previous group's first task", got)
	}
}
