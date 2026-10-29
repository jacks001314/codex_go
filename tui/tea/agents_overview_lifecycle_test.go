package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/utils"
)

func TestAgentsOverviewLifecycleProgressKeepsBackdropLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 20})
	model.agentsOverview = agentsoverview.New([]agentsoverview.Row{{
		ThreadID: "t-1",
		Name:     "Selected task",
	}}, "t-1", false)
	model.agentsOverviewLifecycleProgress = "Archiving task\u2026  You can still browse."
	rendered := utils.StripANSI(model.renderAgentsOverview())
	for _, want := range []string{"Selected task", "Archiving task", "You can still browse"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("lifecycle progress missing %q:\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{"Please wait", "\u256d"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("lifecycle progress still renders the blocking dialog %q:\n%s", unwanted, rendered)
		}
	}
}

type agentsLifecycleCalls struct {
	archived []string
	deleted  []string
}

// TestModelAgentsLifecyclePendingKeepsDashboardBrowsableLikeRust covers Rust
// #53908: the removed task disappears immediately behind an inline progress
// notice, while navigation, search, help, and status tabs keep working and
// task switching, new tasks, and leaving the dashboard stay suppressed.
func TestModelAgentsLifecyclePendingKeepsDashboardBrowsableLikeRust(t *testing.T) {
	calls := &agentsLifecycleCalls{}
	newSessions := 0
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnAgentsOverviewArchive: func(threadID string) error {
			calls.archived = append(calls.archived, threadID)
			return nil
		},
		OnAgentsOverviewNewSession: func(string) (AgentThreadSwitchResponse, error) {
			newSessions++
			return AgentThreadSwitchResponse{}, nil
		},
	})
	openAgentsDashboard(t, model)

	model.Update(agentsKeyEvent('a'))
	model.Update(key(bubbletea.KeyDown))
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("confirmed archive returned no lifecycle command")
	}
	if !strings.Contains(model.agentsOverviewLifecycleProgress, "You can still browse") {
		t.Fatalf("lifecycle progress = %q", model.agentsOverviewLifecycleProgress)
	}
	if visible := model.agentsOverview.VisibleIndices(); len(visible) != 2 {
		t.Fatalf("removed task was not hidden immediately: visible=%v", visible)
	}
	if got := model.agentsOverview.SelectedThreadID(); got != "root-2" {
		t.Fatalf("selection after immediate removal = %q, want root-2", got)
	}

	model.Update(key(bubbletea.KeyDown))
	if got := model.agentsOverview.SelectedThreadID(); got != "root-3" {
		t.Fatalf("down during removal selected %q, want root-3", got)
	}
	model.Update(key(bubbletea.KeyUp))
	if got := model.agentsOverview.SelectedThreadID(); got != "root-2" {
		t.Fatalf("up during removal selected %q, want root-2", got)
	}

	model.Update(agentsF3Event())
	typeText(t, model, "gamma")
	if visible := model.agentsOverview.VisibleIndices(); len(visible) != 1 {
		t.Fatalf("search during removal visible = %v, want gamma only", visible)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.agentsOverview.State.Searching || model.agentsOverview.State.Search != "" {
		t.Fatalf("escape did not close search during removal: %#v", model.agentsOverview.State)
	}

	model.Update(agentsKeyEvent('?'))
	if !model.agentsOverview.State.Help {
		t.Fatal("help did not open during removal")
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.agentsOverview.State.Help {
		t.Fatal("escape did not close help during removal")
	}

	model.Update(key(bubbletea.KeyEnter))
	if model.agentsOverview == nil {
		t.Fatal("enter switched tasks while the removal was pending")
	}
	model.Update(agentsKeyEvent('n'))
	if newSessions != 0 {
		t.Fatalf("new task ran while the removal was pending: %d", newSessions)
	}
	if model.agentsOverview == nil {
		t.Fatal("n left the dashboard while the removal was pending")
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.agentsOverview == nil {
		t.Fatal("escape left the dashboard while the removal was pending")
	}
	if _, quit := model.Update(key(bubbletea.KeyCtrlC)); quit != nil {
		t.Fatal("ctrl+c quit the dashboard while the removal was pending")
	}

	model.Update(bubbletea.WindowSizeMsg{Width: 100, Height: 30})
	if rendered := utils.StripANSI(model.renderAgentsOverview()); !strings.Contains(rendered, "You can still browse") {
		t.Fatalf("resize dropped the inline progress notice:\n%s", rendered)
	}

	updated, refresh := model.Update(command())
	model = updated.(*Model)
	if refresh == nil {
		t.Fatal("completed archive did not refresh the dashboard")
	}
	if model.agentsOverviewLifecycleProgress != "" || model.agentsOverviewBusy {
		t.Fatalf("lifecycle state not cleared: progress=%q busy=%v", model.agentsOverviewLifecycleProgress, model.agentsOverviewBusy)
	}
	updated, _ = model.Update(refresh())
	model = updated.(*Model)
	if visible := model.agentsOverview.VisibleIndices(); len(visible) != 2 {
		t.Fatalf("archived task returned after refresh: visible=%v", visible)
	}
}

func newAgentsLifecycleModel(t *testing.T, calls *agentsLifecycleCalls, archiveErr, deleteErr error) *Model {
	t.Helper()
	return NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(currentThreadID string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnAgentsOverviewArchive: func(threadID string) error {
			calls.archived = append(calls.archived, threadID)
			return archiveErr
		},
		OnAgentsOverviewDelete: func(threadID string) error {
			calls.deleted = append(calls.deleted, threadID)
			return deleteErr
		},
	})
}

// TestModelAgentsLifecycleConfirmationLikeRust covers #44433: a/deletes
// require confirmation with Cancel selected by default, and cancel runs nothing.
func TestModelAgentsLifecycleConfirmationLikeRust(t *testing.T) {
	calls := &agentsLifecycleCalls{}
	model := newAgentsLifecycleModel(t, calls, nil, nil)
	openAgentsDashboard(t, model)

	model.Update(agentsKeyEvent('a'))
	if model.modal == nil || !strings.Contains(model.modal.title, "Archive") {
		t.Fatalf("a did not open the archive confirmation: %#v", model.modal)
	}
	// Cancel is selected by default, so plain Enter cancels.
	updated, cmd := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if cmd != nil {
		t.Fatalf("cancel returned a command: %#v", cmd)
	}
	if len(calls.archived) != 0 {
		t.Fatalf("cancel archived %#v", calls.archived)
	}
	if model.agentsOverview == nil {
		t.Fatal("cancel closed the dashboard")
	}

	model.Update(agentsKeyEvent('a'))
	if model.modal == nil {
		t.Fatalf("second a did not reopen the confirmation (selected=%q rows=%d)", model.agentsOverview.SelectedThreadID(), len(model.agentsOverview.Rows))
	}
	model.Update(key(bubbletea.KeyDown))
	updated, cmd = model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if cmd == nil {
		t.Fatalf("confirm returned no lifecycle command (modal=%#v request=%#v busy=%v archiveCb=%v)", model.modal, model.agentsOverviewLifecycle, model.agentsOverviewBusy, model.onAgentsOverviewArchive != nil)
	}
	msg := cmd()
	updated, refresh := model.Update(msg)
	model = updated.(*Model)
	if len(calls.archived) != 1 || calls.archived[0] != "root-1" {
		t.Fatalf("archived = %#v, want [root-1]", calls.archived)
	}
	if refresh == nil {
		t.Fatal("confirmed archive did not refresh the dashboard")
	}
	if model.agentsOverviewLifecycleProgress != "" || model.agentsOverviewBusy {
		t.Fatalf("lifecycle state not cleared: progress=%q busy=%v", model.agentsOverviewLifecycleProgress, model.agentsOverviewBusy)
	}
}

func TestModelAgentsDeleteDetachesRemovedCurrentTaskLikeRust(t *testing.T) {
	calls := &agentsLifecycleCalls{}
	model := newAgentsLifecycleModel(t, calls, nil, nil)
	model.State.SetThreadID("root-1")
	openAgentsDashboard(t, model)

	model.Update(key(bubbletea.KeyBackspace))
	if model.modal == nil || !strings.Contains(model.modal.title, "delete") {
		t.Fatalf("delete did not open the confirmation: %#v", model.modal)
	}
	model.Update(key(bubbletea.KeyDown))
	updated, cmd := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	// Rust #44744: permanent deletion keeps an explicit second confirmation.
	if cmd != nil || len(calls.deleted) != 0 {
		t.Fatalf("first confirmation ran the deletion: cmd=%#v deleted=%#v", cmd, calls.deleted)
	}
	if model.modal == nil {
		t.Fatal("delete confirmation closed before the explicit confirmation")
	}
	updated, cmd = model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("second confirmation returned no lifecycle command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(*Model)
	if len(calls.deleted) != 1 || calls.deleted[0] != "root-1" {
		t.Fatalf("deleted = %#v, want [root-1]", calls.deleted)
	}
	// The dashboard stays open but unattached.
	if model.agentsOverview == nil {
		t.Fatal("dashboard closed after deleting the current task")
	}
	if strings.TrimSpace(model.State.ThreadID) != "" {
		t.Fatalf("current thread not detached: %q", model.State.ThreadID)
	}
}

// TestModelAgentsConfirmationNumberKeysLikeRust covers #44744: the archive
// confirmation's number shortcut acts immediately, while permanent deletion
// keeps an explicit second confirmation.
func TestModelAgentsConfirmationNumberKeysLikeRust(t *testing.T) {
	runeKey := func(r rune) bubbletea.KeyMsg {
		return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}}
	}

	calls := &agentsLifecycleCalls{}
	archive := newAgentsLifecycleModel(t, calls, nil, nil)
	openAgentsDashboard(t, archive)
	archive.Update(agentsKeyEvent('a'))
	updated, cmd := archive.Update(runeKey('2'))
	archive = updated.(*Model)
	if cmd == nil {
		t.Fatal("the archive number shortcut should act immediately")
	}
	updated, _ = archive.Update(cmd())
	archive = updated.(*Model)
	if len(calls.archived) != 1 || calls.archived[0] != "root-1" {
		t.Fatalf("archived = %#v, want [root-1]", calls.archived)
	}

	deleteCalls := &agentsLifecycleCalls{}
	remove := newAgentsLifecycleModel(t, deleteCalls, nil, nil)
	openAgentsDashboard(t, remove)
	remove.Update(key(bubbletea.KeyBackspace))
	updated, cmd = remove.Update(runeKey('2'))
	remove = updated.(*Model)
	if cmd != nil || len(deleteCalls.deleted) != 0 {
		t.Fatalf("the delete number shortcut must not act on the first press: cmd=%v deleted=%#v", cmd != nil, deleteCalls.deleted)
	}
	if remove.modal == nil {
		t.Fatal("delete confirmation closed before the explicit confirmation")
	}
	updated, cmd = remove.Update(runeKey('2'))
	remove = updated.(*Model)
	if cmd == nil {
		t.Fatal("the second delete confirmation should run the action")
	}
	updated, _ = remove.Update(cmd())
	remove = updated.(*Model)
	if len(deleteCalls.deleted) != 1 || deleteCalls.deleted[0] != "root-1" {
		t.Fatalf("deleted = %#v, want [root-1]", deleteCalls.deleted)
	}
}

func TestModelAgentsLifecycleFailureKeepsAttachmentLikeRust(t *testing.T) {
	calls := &agentsLifecycleCalls{}
	model := newAgentsLifecycleModel(t, calls, errors.New("server rejected archive"), nil)
	model.State.SetThreadID("root-1")
	openAgentsDashboard(t, model)

	model.Update(agentsKeyEvent('a'))
	model.Update(key(bubbletea.KeyDown))
	updated, cmd := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	updated, _ = model.Update(cmd())
	model = updated.(*Model)
	if got := strings.TrimSpace(model.agentsOverviewNotice); !strings.Contains(got, "Failed to archive task") {
		t.Fatalf("failure notice = %q", got)
	}
	if model.State.ThreadID != "root-1" {
		t.Fatalf("attachment dropped on failure: %q", model.State.ThreadID)
	}
}

// TestModelAgentsArchiveKeepsSelectionAdjacentLikeRust covers Rust #50505
// (47379efd52): archiving the selected task keeps the Command Center selection
// on the next displayed task instead of jumping to the task at the top of the
// list. Rust test:
// agents_overview_actions_tests.rs::archiving_selects_the_next_displayed_task
// (its selections snapshot walks Task 3 -> Other 2 -> Task 1 -> Task 4).
func TestModelAgentsArchiveKeepsSelectionAdjacentLikeRust(t *testing.T) {
	calls := &agentsLifecycleCalls{}
	rows := agentsOverviewTestRows()
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return rows, nil
		},
		OnAgentsOverviewArchive: func(threadID string) error {
			calls.archived = append(calls.archived, threadID)
			return nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 1 // root-2 (beta)
	if got := model.agentsOverview.SelectedThreadID(); got != "root-2" {
		t.Fatalf("selected task = %q, want root-2", got)
	}

	model.Update(agentsKeyEvent('a'))
	model.Update(key(bubbletea.KeyDown)) // move the confirmation off Cancel
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("confirmed archive returned no lifecycle command")
	}
	message := command()
	if len(calls.archived) != 1 || calls.archived[0] != "root-2" {
		t.Fatalf("archived = %#v, want [root-2]", calls.archived)
	}

	// The archived task is gone from the next shared-task listing.
	rows = []agentsoverview.Row{rows[0], rows[2]}
	updated, refresh := model.Update(message)
	model = updated.(*Model)
	if refresh == nil {
		t.Fatal("archived task did not refresh the dashboard")
	}
	updated, _ = model.Update(refresh())
	model = updated.(*Model)

	if got := model.agentsOverview.SelectedThreadID(); got != "root-3" {
		t.Fatalf("selection after archive = %q, want the adjacent root-3", got)
	}
	if got := model.agentsOverview.PendingSelectionAfterRemoval(); got != "" {
		t.Fatalf("pending successor was not consumed: %q", got)
	}
}
