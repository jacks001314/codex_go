package tea

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
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
	for _, want := range []string{"Agent command center", "alpha", "beta", "gamma", "1 need input", "x stop"} {
		if !strings.Contains(output, want) {
			t.Errorf("dashboard view missing %q:\n%s", want, output)
		}
	}
}

// agentsKeyEvent builds a plain-character key event, which is how the command
// center's single-letter shortcuts arrive (Rust #45255).
func agentsKeyEvent(r rune) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}}
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
	model.Update(agentsKeyEvent('f'))
	if !model.agentsOverview.State.Searching {
		t.Fatal("f did not enter search")
	}
	typeText(t, model, "gamma")
	if visible := model.agentsOverview.VisibleIndices(); len(visible) != 1 {
		t.Fatalf("search visible = %v, want single gamma row", visible)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.agentsOverview.State.Searching || model.agentsOverview.State.Search != "" {
		t.Fatalf("esc did not exit search: %#v", model.agentsOverview.State)
	}
	model.Update(agentsKeyEvent('g'))
	if model.agentsOverview.State.Grouping != agentsoverview.GroupingStatus {
		t.Fatalf("g grouping = %v, want status", model.agentsOverview.State.Grouping)
	}
	// Rust #44957: the toggle cycles on to model grouping.
	model.Update(agentsKeyEvent('g'))
	if model.agentsOverview.State.Grouping != agentsoverview.GroupingModel {
		t.Fatalf("second g grouping = %v, want model", model.agentsOverview.State.Grouping)
	}
}

// Mirrors Rust #44344: Right opens the highlighted task from an empty
// composer, while a non-empty draft keeps Right for the editor.
func TestModelAgentsDashboardRightOpensSelectedTask(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	model.Update(key(bubbletea.KeyDown))
	if got := model.agentsOverview.SelectedThreadID(); got != "root-2" {
		t.Fatalf("selection before right = %q, want root-2", got)
	}
	updated, _ := model.Update(key(bubbletea.KeyRight))
	model = updated.(*Model)
	if model.agentsOverview != nil {
		t.Fatal("right from an empty composer did not open the selected task")
	}

	// Rust #45255: metadata editing keeps Right for the editor.
	editing := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, editing)
	editing.Update(agentsKeyEvent('f'))
	updated, _ = editing.Update(key(bubbletea.KeyRight))
	editing = updated.(*Model)
	if editing.agentsOverview == nil {
		t.Fatal("right while searching must keep the editor, not open the task")
	}
	updated, _ = editing.Update(key(bubbletea.KeyEsc))
	editing = updated.(*Model)
	editing.Update(agentsKeyEvent('r'))
	updated, _ = editing.Update(key(bubbletea.KeyRight))
	editing = updated.(*Model)
	if editing.agentsOverview == nil {
		t.Fatal("right while renaming must keep the editor, not open the task")
	}
}

// Rust #45255: `n` opens a blank session in the selected checkout without
// sending a turn, and the started session is retained until its first turn.
func TestModelAgentsDashboardNewSessionOpensBlankSession(t *testing.T) {
	var newSessionCwd string
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnAgentsOverviewNewSession: func(cwd string) (AgentThreadSwitchResponse, error) {
			newSessionCwd = cwd
			return AgentThreadSwitchResponse{
				Entry:  codextui.AgentThreadEntry{ThreadID: "new-1", AgentNickname: "New session"},
				Status: "idle",
			}, nil
		},
	})
	openAgentsDashboard(t, model)
	updated, command := model.Update(agentsKeyEvent('n'))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("n returned no new-session command")
	}
	message := command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if newSessionCwd != "/work/a" {
		t.Fatalf("new session cwd = %q, want selected project cwd /work/a", newSessionCwd)
	}
	if model.agentsOverview != nil {
		t.Fatal("starting a session must close the dashboard")
	}
	if got := model.State.ThreadID; got != "new-1" {
		t.Fatalf("attached thread = %q, want new-1", got)
	}
	if _, ok := model.agentsOverviewBlankSessions["new-1"]; !ok {
		t.Fatal("the started session must be retained until its first turn")
	}
	// The first turn materializes the rollout, so the retained snapshot drops.
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started", ThreadID: "new-1"}})
	if _, ok := model.agentsOverviewBlankSessions["new-1"]; ok {
		t.Fatal("the retained session must be cleared once its first turn starts")
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
	updated, command := model.Update(agentsKeyEvent('x'))
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
	model.Update(agentsKeyEvent('r'))
	if !model.agentsOverview.State.Renaming {
		t.Fatal("r did not start renaming")
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

// TestModelAgentsDashboardOpenReadOnlyAppliesSnapshot covers Rust #44969: a
// task managed by another app server opens as a frozen read-only history
// snapshot with the resumed settings applied and the composer protected.
func TestModelAgentsDashboardOpenReadOnlyAppliesSnapshot(t *testing.T) {
	cwd := "D:/repo"
	state := codextui.NewState(nil)
	state.SetThreadID("root-1")
	model := NewModel(state, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:          codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "agent"},
				Status:         "running",
				ReadOnly:       true,
				ThreadSettings: &appserver.Settings{CWD: cwd},
			}, nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 1 // root-2
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("open returned no attach command")
	}
	model.Update(command())
	if !model.readOnlyThread {
		t.Fatal("a task managed elsewhere must open read-only")
	}
	if model.State == nil || model.State.CWD != cwd {
		t.Fatalf("read-only snapshot did not apply the resumed settings: %#v", model.State)
	}
	if strings.TrimSpace(model.composer.Value()) != "" {
		t.Fatalf("read-only composer = %q, want empty", model.composer.Value())
	}
	if notice := model.renderReadOnlyThreadNotice(); !strings.Contains(notice, "open in another app") {
		t.Fatalf("read-only notice = %q", notice)
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

func TestModelAgentsDashboardPreservesDraftAcrossSwitch(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("root-1")
	model := NewModel(state, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:  codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "agent"},
				Status: "idle",
			}, nil
		},
	})

	// root-2 carries a preserved draft from a previous switch; attaching must
	// restore it (Rust restore_thread_input_state).
	model.agentsOverviewDrafts = map[string]string{"root-2": "saved B"}
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 1
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	message := command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if got := model.composer.Value(); got != "saved B" {
		t.Fatalf("composer after attach to root-2 = %q, want restored %q", got, "saved B")
	}
	if model.agentsOverviewPendingDraft != nil {
		t.Fatal("pending draft should be consumed after attach")
	}
	if _, exists := model.agentsOverviewDrafts["root-2"]; exists {
		t.Fatal("restored draft must be consumed from the map (Rust remove)")
	}

	// captureAgentsOverviewDraft stores the composer value per thread (the
	// /agents slash command is composer-exclusive like Rust, so the capture
	// hook is exercised directly here; it runs on every dashboard attach).
	model.composer.SetValue("draft C")
	model.captureAgentsOverviewDraft("root-2")
	if got := model.agentsOverviewDrafts["root-2"]; got != "draft C" {
		t.Fatalf("captured draft for root-2 = %q, want %q", got, "draft C")
	}
	// Attaching to root-3 (no saved draft) clears the composer instead of
	// carrying root-2's draft.
	model.composer.SetValue("")
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 2 // root-3
	updated, command = model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	message = command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer after attach to root-3 = %q, want empty", got)
	}
}

func TestModelAgentsDashboardAttachClearsComposerWithoutSavedDraft(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("root-1")
	model := NewModel(state, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:  codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "agent"},
				Status: "idle",
			}, nil
		},
	})
	// A draft composed on root-1 is captured per-thread (Rust input_states);
	// attaching to a thread without a saved draft must not carry it along.
	model.composer.SetValue("stale draft from root-1")
	model.captureAgentsOverviewDraft("root-1")
	if got := model.agentsOverviewDrafts["root-1"]; got != "stale draft from root-1" {
		t.Fatalf("captured draft for root-1 = %q", got)
	}
	model.composer.SetValue("")
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 2 // root-3 has no saved draft
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	message := command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if model == nil {
		t.Fatal("model became nil after attach")
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer after attach to root-3 = %q, want empty (Rust fresh chat widget)", got)
	}
}

func TestModelAgentsDashboardSwitchFailureDiscardsPendingDraft(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("root-1")
	model := NewModel(state, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{}, errors.New("boom")
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 1
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	message := command()
	updated, _ = model.Update(message)
	model = updated.(*Model)
	if model.agentsOverviewPendingDraft != nil {
		t.Fatal("failed switch must discard the pending draft")
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer after failed switch = %q, want empty", got)
	}
}
