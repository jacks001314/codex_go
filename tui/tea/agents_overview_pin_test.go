package tea

import (
	"errors"
	"strings"
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/utils"
)

func agentsOverviewPinnableRows() []agentsoverview.Row {
	return []agentsoverview.Row{
		{ThreadID: "root-1", Name: "alpha", Preview: "fix parser", CWD: "/work/a", Group: agentsoverview.GroupWorking, Source: "cli"},
		{ThreadID: "root-2", Name: "beta", Preview: "review pr", CWD: "/work/b", Group: agentsoverview.GroupReady, Source: "cli"},
	}
}

// TestAgentsOverviewTogglePinDispatchesLikeRust covers Rust #51500: `p` asks
// the host to pin or unpin the selected task, duplicate actions are suppressed
// while the request is in flight, a successful change updates the local pin
// order and refreshes the shared list. Rust counterpart:
// command_center_new_actions_use_selection_and_leave_metadata_text_alone
// (shortcut dispatch and duplicate-action suppression).
func TestAgentsOverviewTogglePinDispatchesLikeRust(t *testing.T) {
	type pinCall struct {
		threadID string
		pinned   bool
	}
	var calls []pinCall
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewPinnableRows(), nil
		},
		OnAgentsOverviewPinnedThreads: func() ([]string, bool, error) {
			return nil, true, nil
		},
		OnAgentsOverviewTogglePin: func(threadID string, pinned bool) error {
			calls = append(calls, pinCall{threadID: threadID, pinned: pinned})
			return nil
		},
	})
	openAgentsDashboard(t, model)
	if !model.agentsOverview.PinsSupported() {
		t.Fatal("dashboard did not learn the shared pinned section")
	}

	updated, pinCommand := model.Update(agentsKeyEvent('p'))
	model = updated.(*Model)
	if pinCommand == nil {
		t.Fatal("p returned no pin command")
	}
	// The request is in flight until its result arrives.
	if !model.agentsOverview.PinPending() {
		t.Fatal("pin request should be pending after p")
	}
	updated, duplicateCommand := model.Update(agentsKeyEvent('p'))
	model = updated.(*Model)
	if duplicateCommand != nil || len(calls) != 0 {
		t.Fatal("duplicate p must be suppressed while a pin change is pending")
	}

	message := pinCommand()
	pinResult, ok := message.(agentsOverviewPinMsg)
	if !ok {
		t.Fatalf("pin command returned %T, want agentsOverviewPinMsg", message)
	}
	if pinResult.threadID != "root-1" || !pinResult.pinned {
		t.Fatalf("pin request = %#v, want root-1 pinned", pinResult)
	}
	if len(calls) != 1 || calls[0].threadID != "root-1" || !calls[0].pinned {
		t.Fatalf("host pin calls = %#v, want one root-1 pin", calls)
	}

	updated, refreshCommand := model.Update(pinResult)
	model = updated.(*Model)
	if model.agentsOverview.PinPending() {
		t.Fatal("pin request should no longer be pending")
	}
	if !model.agentsOverview.IsPinned("root-1") {
		t.Fatal("successful pin did not update the local pin order")
	}
	if refreshCommand == nil {
		t.Fatal("pin change should refresh the shared list")
	}

	// Unpinning the same task asks the host for the opposite state.
	updated, unpinCommand := model.Update(agentsKeyEvent('p'))
	model = updated.(*Model)
	if unpinCommand == nil {
		t.Fatal("second p returned no pin command")
	}
	unpin, ok := unpinCommand().(agentsOverviewPinMsg)
	if !ok || unpin.threadID != "root-1" || unpin.pinned {
		t.Fatalf("unpin request = %#v, want root-1 unpinned", unpin)
	}
	updated, _ = model.Update(unpin)
	model = updated.(*Model)
	if model.agentsOverview.IsPinned("root-1") {
		t.Fatal("successful unpin did not update the local pin order")
	}
}

// TestAgentsOverviewPinnedThreadsRefreshLikeRust covers Rust #51500's shared pin
// discovery: the refresh loads the pinned section (including tasks outside the
// recent-task window), disables pinning when the server has no shared sections,
// and keeps the known pins when the pin listing fails. Rust counterpart:
// list_pinned_threads / check_discovery.
func TestAgentsOverviewPinnedThreadsRefreshLikeRust(t *testing.T) {
	pins := []string{"root-2"}
	supported := true
	var pinErr error
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewPinnableRows(), nil
		},
		OnAgentsOverviewPinnedThreads: func() ([]string, bool, error) {
			return pins, supported, pinErr
		},
		OnAgentsOverviewTogglePin: func(string, bool) error { return nil },
	})
	openAgentsDashboard(t, model)
	if !model.agentsOverview.IsPinned("root-2") {
		t.Fatal("pinned task from the shared section was not applied")
	}
	rendered := utils.StripANSI(model.View())
	if !strings.Contains(rendered, "Pinned") {
		t.Fatalf("dashboard does not render the Pinned group:\n%s", rendered)
	}

	// A failed pin listing keeps the pins the dashboard already knows about.
	pinErr = errors.New("shared sections unavailable")
	refreshCommand := model.refreshAgentsOverviewCmd()
	if refreshCommand == nil {
		t.Fatal("refresh returned no command")
	}
	updated, _ := model.Update(refreshCommand())
	model = updated.(*Model)
	if !model.agentsOverview.IsPinned("root-2") {
		t.Fatal("a failed pin listing must keep the known pins")
	}

	// Servers without shared thread sections disable pinning entirely.
	pinErr = nil
	supported = false
	pins = nil
	updated, _ = model.Update(model.refreshAgentsOverviewCmd()())
	model = updated.(*Model)
	if model.agentsOverview.PinsSupported() {
		t.Fatal("pinning must be disabled without shared section support")
	}
	updated, command := model.Update(agentsKeyEvent('p'))
	model = updated.(*Model)
	if command != nil {
		t.Fatal("p must do nothing when the server has no shared sections")
	}
}
