package tea

import (
	"errors"
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// Rust #63146: the command center keeps its outgoing frame visible, with the
// selected row marked "Opening…", until the destination is ready; a failed
// resume returns to the list without the opening mark.
func TestOpeningTaskKeepsCommandCenterFrameLikeRust(t *testing.T) {
	newModel := func(switcher func(string) (AgentThreadSwitchResponse, error)) *Model {
		state := codextui.NewState(nil)
		state.SetThreadID("source")
		model := NewModel(state, Options{Width: 120, Height: 24, OnSwitchAgent: switcher})
		// The modal Command Center closes after the destination attaches;
		// the docked behaviour is covered by agents_overview_dock_like_rust_test.go.
		model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: false}
		model.agentsOverview = agentsoverview.New([]agentsoverview.Row{{
			ThreadID: "target", Name: "target", CWD: "work", Model: "gpt-test",
		}}, "target", false)
		return model
	}

	model := newModel(func(threadID string) (AgentThreadSwitchResponse, error) {
		return AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: threadID},
			Status: "idle",
		}, nil
	})
	cmd := model.openAgentsOverviewThread("target")
	if cmd == nil {
		t.Fatal("opening a target returned no switch command")
	}
	if model.agentsOverview == nil {
		t.Fatal("the command center closed before the destination was ready")
	}
	if got := model.agentsOverview.OpeningThread(); got != "target" {
		t.Fatalf("OpeningThread() = %q, want target", got)
	}
	updated, _ := model.Update(cmd())
	model = updated.(*Model)
	if model.agentsOverview != nil {
		t.Fatal("the command center stayed open after the destination attached")
	}
	if model.agentsOverviewOpening != "" {
		t.Fatalf("opening state = %q, want cleared", model.agentsOverviewOpening)
	}

	failed := newModel(func(string) (AgentThreadSwitchResponse, error) {
		return AgentThreadSwitchResponse{}, errors.New("resume failed")
	})
	cmd = failed.openAgentsOverviewThread("target")
	if cmd == nil {
		t.Fatal("failed open returned no switch command")
	}
	updated, _ = failed.Update(cmd())
	failed = updated.(*Model)
	if failed.agentsOverview == nil {
		t.Fatal("a failed resume dropped the command center")
	}
	if got := failed.agentsOverview.OpeningThread(); got != "" {
		t.Fatalf("OpeningThread() = %q after failure, want cleared", got)
	}
	if failed.agentsOverviewOpening != "" {
		t.Fatalf("opening state = %q after failure, want cleared", failed.agentsOverviewOpening)
	}
}
