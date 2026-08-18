package tea

import (
	"context"
	"runtime"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/utils"
)

func agentsOverviewTestRows() []agentsoverview.Row {
	return []agentsoverview.Row{
		{ThreadID: "root-1", Name: "alpha", Preview: "fix parser", CWD: "/work/a", Group: agentsoverview.GroupWorking, StatusActive: true},
		{ThreadID: "root-2", Name: "beta", Preview: "review pr", CWD: "/work/a", Group: agentsoverview.GroupReady},
		{ThreadID: "root-3", Name: "gamma", Preview: "needs approval", CWD: "/work/b", Group: agentsoverview.GroupNeedsYou, StatusActive: true},
	}
}

func openAgentsDashboard(t *testing.T, model *Model) {
	t.Helper()
	typeText(t, model, "/agents")
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if model.agentsOverview == nil {
		t.Fatal("/agents did not open the dashboard")
	}
	if command == nil {
		t.Fatal("/agents returned no refresh command")
	}
	message := command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if model.agentsOverview == nil {
		t.Fatal("dashboard closed after refresh")
	}
}

func TestModelAgentsCommandOpensDashboardAndLoads(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	output := utils.StripANSI(model.View())
	for _, want := range []string{"Agent command center", "alpha", "beta", "gamma", "1 need input", "ctrl+x stop"} {
		if !strings.Contains(output, want) {
			t.Errorf("dashboard view missing %q:\n%s", want, output)
		}
	}
}

func TestModelAgentsDashboardNavigationAndSearch(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	if got := model.agentsOverview.SelectedThreadID(); got != "root-1" {
		t.Fatalf("initial selection = %q, want root-1", got)
	}
	model.Update(key(bubbletea.KeyDown))
	if got := model.agentsOverview.SelectedThreadID(); got != "root-2" {
		t.Fatalf("selection after down = %q, want root-2", got)
	}
	model.Update(key(bubbletea.KeyCtrlF))
	if !model.agentsOverview.State.Searching {
		t.Fatal("ctrl+f did not enter search")
	}
	typeText(t, model, "gamma")
	if visible := model.agentsOverview.VisibleIndices(); len(visible) != 1 {
		t.Fatalf("search visible = %v, want single gamma row", visible)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.agentsOverview.State.Searching || model.agentsOverview.State.Search != "" {
		t.Fatalf("esc did not exit search: %#v", model.agentsOverview.State)
	}
	model.Update(key(bubbletea.KeyCtrlS))
	if !model.agentsOverview.State.StatusGrouping {
		t.Fatal("ctrl+s did not enable status grouping")
	}
}

func TestModelAgentsDashboardDispatchTask(t *testing.T) {
	var dispatched []string
	var dispatchedCwd string
	refreshed := 0
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			refreshed++
			return agentsOverviewTestRows(), nil
		},
		OnAgentsOverviewDispatch: func(prompt string, cwd string) (string, error) {
			dispatched = append(dispatched, prompt)
			dispatchedCwd = cwd
			return "new-1", nil
		},
	})
	openAgentsDashboard(t, model)
	refreshed = 0
	typeText(t, model, "add tests")
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("enter with input returned no dispatch command")
	}
	message := command()
	updated, refreshCommand := model.Update(message)
	model = updated.(*Model)
	if !strings.Contains(utils.StripANSI(model.View()), "Dispatched task new-1") {
		t.Fatalf("notice missing dispatch confirmation:\n%s", utils.StripANSI(model.View()))
	}
	if len(dispatched) != 1 || dispatched[0] != "add tests" {
		t.Fatalf("dispatched = %v", dispatched)
	}
	if dispatchedCwd != "/work/a" {
		t.Fatalf("dispatch cwd = %q, want selected project cwd /work/a", dispatchedCwd)
	}
	if refreshCommand != nil {
		refreshMessage := refreshCommand()
		updated, _ = model.Update(refreshMessage)
		model = updated.(*Model)
	}
	if refreshed == 0 {
		t.Fatal("dispatch did not trigger a refresh")
	}
}

func TestModelAgentsDashboardStopSelected(t *testing.T) {
	var stopped []string
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnAgentsOverviewStop: func(threadID string) error {
			stopped = append(stopped, threadID)
			return nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 2 // gamma is active
	updated, command := model.Update(key(bubbletea.KeyCtrlX))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("ctrl+x returned no stop command")
	}
	message := command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if len(stopped) != 1 || stopped[0] != "root-3" {
		t.Fatalf("stopped = %v, want [root-3]", stopped)
	}
}

func TestModelAgentsDashboardRenameSelected(t *testing.T) {
	renamed := map[string]string{}
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnAgentsOverviewRename: func(threadID string, name string) error {
			renamed[threadID] = name
			return nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 1
	model.Update(key(bubbletea.KeyCtrlR))
	if !model.agentsOverview.State.Renaming {
		t.Fatal("ctrl+r did not start renaming")
	}
	typeText(t, model, " v2")
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("enter returned no rename command")
	}
	message := command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if got := renamed["root-2"]; got != "beta v2" {
		t.Fatalf("renamed = %q, want %q", got, "beta v2")
	}
}

func TestModelAgentsDashboardEscCloses(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	model.Update(key(bubbletea.KeyEsc))
	if model.agentsOverview != nil {
		t.Fatal("esc did not close the dashboard")
	}
	output := utils.StripANSI(model.View())
	if strings.Contains(output, "Agent command center") {
		t.Fatalf("dashboard still rendered after esc:\n%s", output)
	}
}

func TestModelAgentsDashboardOpenAttachesViaSwitchAgent(t *testing.T) {
	var switched []string
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			switched = append(switched, threadID)
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "agent"},
				Messages: nil,
				Status:   "idle",
			}, nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 1 // root-2
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if model.agentsOverview != nil {
		t.Fatal("open did not close the dashboard")
	}
	if command == nil {
		t.Fatal("open returned no attach command")
	}
	message := command()
	model.Update(message)
	if len(switched) != 1 || switched[0] != "root-2" {
		t.Fatalf("switched = %v, want [root-2]", switched)
	}
}

func TestModelAgentsDashboardOpenCurrentThreadIsNoop(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("root-1")
	var switched []string
	model := NewModel(state, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			switched = append(switched, threadID)
			return AgentThreadSwitchResponse{Entry: codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "agent"}, Status: "idle"}, nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 0 // root-1 is the current thread
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if model.agentsOverview != nil {
		t.Fatal("open did not close the dashboard")
	}
	if command != nil {
		t.Fatal("opening the current thread should not attach")
	}
	if len(switched) != 0 {
		t.Fatalf("switched = %v, want none", switched)
	}
}

func TestModelAgentsEmbeddedShowsUnavailableSelection(t *testing.T) {
	model := NewModel(nil, Options{
		AgentsOverviewEmbedded: true,
		Width:                  120,
		Height:                 24,
	})
	typeText(t, model, "/agents")
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command != nil {
		t.Fatal("embedded /agents returned a command")
	}
	if model.modal == nil || model.modal.id != "agents-unavailable" || model.modal.kind != ModalKindAgents {
		t.Fatalf("embedded /agents modal = %#v", model.modal)
	}
	// "Return to this session" closes the modal without side effects.
	model.modal.selected = modalOptionIndexByID(t, model.modal.options, "return")
	updated, command = model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command != nil || model.modal != nil {
		t.Fatalf("return did not close modal: modal=%#v cmd=%v", model.modal, command)
	}
}

func TestModelAgentsEmbeddedStartDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("start background server option is Unix-only (Rust cfg(unix))")
	}
	started := false
	model := NewModel(nil, Options{
		AgentsOverviewEmbedded: true,
		Width:                  120,
		Height:                 24,
		OnStartAgentsDaemon: func() error {
			started = true
			return nil
		},
	})
	typeText(t, model, "/agents")
	updated, _ := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if model.modal == nil || model.modal.id != "agents-unavailable" {
		t.Fatalf("embedded modal = %#v", model.modal)
	}
	model.modal.selected = modalOptionIndexByID(t, model.modal.options, "start-daemon")
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("start-daemon returned no command")
	}
	message := command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if !started {
		t.Fatal("start-daemon callback was not invoked")
	}
	if !strings.Contains(model.notice, "Background server started") {
		t.Fatalf("notice = %q", model.notice)
	}
}

func TestModelAgentsDashboardRefreshErrorShowsNotice(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return nil, context.DeadlineExceeded
		},
	})
	openAgentsDashboard(t, model)
	output := utils.StripANSI(model.View())
	if !strings.Contains(output, "Failed to load shared agents") {
		t.Fatalf("notice missing error:\n%s", output)
	}
}

func TestModelAgentsDashboardRefreshesOnThreadEventsAndCoalesces(t *testing.T) {
	refreshed := 0
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			refreshed++
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	refreshed = 0

	// A thread event while the dashboard is open starts a refresh.
	updated, command := model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "thread/statusChanged", ThreadID: "root-3"}})
	model = updated.(*Model)
	if command == nil {
		t.Fatal("thread event returned no refresh command")
	}
	if !model.agentsOverviewInflight {
		t.Fatal("thread event did not mark the dashboard refresh inflight")
	}

	// Direct coalescing check at the refresh layer.
	model.agentsOverviewInflight = false
	model.agentsOverviewPending = false
	refresh1 := model.refreshAgentsOverviewCmd()
	if refresh1 == nil || !model.agentsOverviewInflight {
		t.Fatal("first refresh did not become inflight")
	}
	// A second refresh request while one is in flight coalesces into pending.
	if pending := model.refreshAgentsOverviewCmd(); pending != nil {
		t.Fatal("refresh while inflight returned a command, want coalesce")
	}
	if !model.agentsOverviewPending {
		t.Fatal("coalesced refresh did not set pending")
	}
	// Completing the in-flight refresh triggers the pending one.
	message := refresh1()
	updated, pendingCommand := model.Update(message)
	model = updated.(*Model)
	if pendingCommand == nil {
		t.Fatal("completing the inflight refresh did not run the pending refresh")
	}
	pendingMessage := pendingCommand()
	updated, _ = model.Update(pendingMessage)
	model = updated.(*Model)
	if refreshed != 2 {
		t.Fatalf("refreshed = %d, want 2", refreshed)
	}
}
