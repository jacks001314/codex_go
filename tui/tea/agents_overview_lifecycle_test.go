package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	agentsoverview "codex_go/tui/agents_overview"
)

type agentsLifecycleCalls struct {
	archived []string
	deleted  []string
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

// TestModelAgentsLifecycleConfirmationLikeRust covers #44433: ctrl+e/deletes
// require confirmation with Cancel selected by default, and cancel runs nothing.
func TestModelAgentsLifecycleConfirmationLikeRust(t *testing.T) {
	calls := &agentsLifecycleCalls{}
	model := newAgentsLifecycleModel(t, calls, nil, nil)
	openAgentsDashboard(t, model)

	model.Update(key(bubbletea.KeyCtrlE))
	if model.modal == nil || !strings.Contains(model.modal.title, "Archive") {
		t.Fatalf("ctrl+e did not open the archive confirmation: %#v", model.modal)
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

	model.Update(key(bubbletea.KeyCtrlE))
	if model.modal == nil {
		t.Fatalf("second ctrl+e did not reopen the confirmation (selected=%q rows=%d)", model.agentsOverview.SelectedThreadID(), len(model.agentsOverview.Rows))
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

	model.Update(key(bubbletea.KeyDelete))
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
	archive.Update(key(bubbletea.KeyCtrlE))
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
	remove.Update(key(bubbletea.KeyDelete))
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

	model.Update(key(bubbletea.KeyCtrlE))
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
