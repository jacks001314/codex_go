package tea

import (
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/keymsg"
)

func openAgentsOverviewForCancelTest(t *testing.T, state *codextui.State) *Model {
	t.Helper()
	model := NewModel(state, Options{
		Width: 120, Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return nil, nil
		},
	})
	openAgentsDashboard(t, model)
	return model
}

// Mirrors Rust #60271: Escape keeps an initial dashboard open when there is no
// chat, and returns to the conversation when one is displayed.
func TestAgentsOverviewCancelNeedsChatToReturnToLikeRust(t *testing.T) {
	initial := openAgentsOverviewForCancelTest(t, &codextui.State{})
	updated, _ := initial.Update(key(keymsg.KeyEsc))
	if updated.(*Model).agentsOverview == nil {
		t.Fatal("initial dashboard closed with no chat to return to")
	}

	attached := openAgentsOverviewForCancelTest(t, &codextui.State{ThreadID: "thread-1"})
	updated, _ = attached.Update(key(keymsg.KeyEsc))
	if updated.(*Model).agentsOverview != nil {
		t.Fatal("dashboard stayed open although a chat was available")
	}
}
