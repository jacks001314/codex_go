package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// agentsF3Event is the f3 key event, the upstream default for the moved
// `agents.search` shortcut (Rust #49517) and for `global.find_transcript`.
func agentsF3Event() bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyF3}
}

// TestAgentsOverviewForkShortcutLikeRust covers Rust #49517 (d42056091a): in the
// command center `f` forks the selected conversation and opens the new session,
// while the search shortcut moved to f3 and `/`. The fork runs through the same
// onSessionAction hook as `/fork`, so deleting the dispatch would leave the new
// catalog entry with no consumer — which this test rejects.
func TestAgentsOverviewForkShortcutLikeRust(t *testing.T) {
	var actions []codextui.SessionSelection
	var responses []ModalResponse
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{ThreadID: "root-1-fork", Title: "alpha (fork)"}, nil
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	openAgentsDashboard(t, model)
	if got := model.agentsOverview.SelectedThreadID(); got != "root-1" {
		t.Fatalf("initial selection = %q, want root-1", got)
	}

	model.Update(agentsKeyEvent('f'))
	if model.agentsOverview != nil {
		t.Fatal("f forked but left the command center open")
	}
	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionFork || actions[0].Target.ThreadID != "root-1" {
		t.Fatalf("fork selections = %#v, want one fork of root-1", actions)
	}
	if got := model.State.ThreadID; got != "root-1-fork" {
		t.Fatalf("thread after fork = %q, want the forked session", got)
	}
	// The new session is handed back to the shell the way /fork hands it back,
	// so the host can attach and load its transcript.
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Value != "root-1-fork" {
		t.Fatalf("modal responses = %#v, want one fork picker decision", responses)
	}
}

// TestAgentsOverviewForkGuardsLikeRust covers the two Rust #49517 preconditions:
// an open side conversation must be closed before forking another task, and a
// pending permission-profile switch blocks the fork.
func TestAgentsOverviewForkGuardsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		setup  func(*Model)
		notice string
	}{
		{
			name:   "side conversation open",
			setup:  func(m *Model) { m.activeSide = &activeSideConversation{ShowingSide: true} },
			notice: "Close the side conversation before forking a task.",
		},
		{
			name:   "permissions pending",
			setup:  func(m *Model) { m.pendingServerProfile = "work" },
			notice: "Wait for permissions to update before forking.",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			forks := 0
			model := NewModel(nil, Options{
				Width:  120,
				Height: 24,
				OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
					return agentsOverviewTestRows(), nil
				},
				OnSessionAction: func(codextui.SessionSelection) (*codextui.SessionSummary, error) {
					forks++
					return &codextui.SessionSummary{ThreadID: "root-1-fork"}, nil
				},
			})
			openAgentsDashboard(t, model)
			testCase.setup(model)
			model.Update(agentsKeyEvent('f'))
			if forks != 0 {
				t.Fatalf("fork ran despite %s", testCase.name)
			}
			if model.agentsOverview == nil {
				t.Fatalf("dashboard closed despite %s", testCase.name)
			}
			if got := model.agentsOverviewNotice; !strings.Contains(got, testCase.notice) {
				t.Fatalf("notice = %q, want %q", got, testCase.notice)
			}
		})
	}
}

// TestAgentsOverviewSearchDefaultMovedLikeRust pins the other half of #49517:
// search owns f3 and `/`, and `f` no longer toggles it.
func TestAgentsOverviewSearchDefaultMovedLikeRust(t *testing.T) {
	for _, binding := range []string{"f3", "/"} {
		model := NewModel(nil, Options{
			Width:  120,
			Height: 24,
			OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
				return agentsOverviewTestRows(), nil
			},
		})
		openAgentsDashboard(t, model)
		switch binding {
		case "f3":
			model.Update(agentsF3Event())
		default:
			model.Update(agentsKeyEvent('/'))
		}
		if !model.agentsOverview.State.Searching {
			t.Fatalf("%s did not enter command-center search (Rust #49517 moved search to f3 and /)", binding)
		}
	}
}
