package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust #58767: rejecting /fork while foreground work is pending must keep the
// composer draft instead of consuming it.
func TestModelForkBusyRejectionPreservesDraftLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	state.SetStatus("running")
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{ThreadID: "thread-forked", Title: "Forked"}, nil
		},
	})

	typeText(t, model, "/fork")
	updated, cmd := model.Update(key(keymsg.KeyEnter))
	model = updated.(*Model)
	runTeaCmd(t, model, cmd)

	if len(actions) != 0 {
		t.Fatalf("fork ran during a running turn: %#v", actions)
	}
	if got := model.composerText(); got != "/fork" {
		t.Fatalf("composer text = %q, want the rejected draft preserved", got)
	}
}

// Rust #58767: MCP startup alone does not block /fork; it dispatches and clears
// the composer once the thread state validates it.
func TestModelForkAllowedDuringMCPStartupLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{ThreadID: "thread-forked", Title: "Forked"}, nil
		},
	})
	model.mcpStartupActive = true

	typeText(t, model, "/fork")
	updated, cmd := model.Update(key(keymsg.KeyEnter))
	model = updated.(*Model)
	runTeaCmd(t, model, cmd)

	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionFork {
		t.Fatalf("fork actions = %#v", actions)
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("composer text = %q, want cleared after a successful dispatch", got)
	}
}
