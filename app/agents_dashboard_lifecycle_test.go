package app

import (
	"context"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// TestAgentsDashboardArchiveAndDeleteRequireConfirmationLikeRust covers
// #44433 for the standalone dashboard: ctrl+e archives and Delete deletes the
// selected task, both only after explicit confirmation.
func TestAgentsDashboardArchiveAndDeleteRequireConfirmationLikeRust(t *testing.T) {
	source := newFakeDashboardSource()
	model := newAgentsDashboardModel(context.Background(), source)
	model.view.ApplyRefresh(source.rows, "")
	if got := model.view.SelectedThreadID(); got != "root-1" {
		t.Fatalf("initial selection = %q, want root-1", got)
	}

	// Cancel is the safe default: esc clears the pending action without a call.
	model.Update(keyPress(bubbletea.KeyCtrlE))
	if model.pendingLifecycle != "archive" {
		t.Fatalf("ctrl+e pending action = %q, want archive", model.pendingLifecycle)
	}
	if !strings.Contains(model.View(), "Archive this task") {
		t.Fatalf("archive confirmation missing:\n%s", model.View())
	}
	model.Update(keyPress(bubbletea.KeyEsc))
	if model.pendingLifecycle != "" || len(source.archived) != 0 {
		t.Fatalf("cancel archived %v pending=%q", source.archived, model.pendingLifecycle)
	}

	// Confirm archives the selected task and refreshes.
	model.Update(keyPress(bubbletea.KeyCtrlE))
	updated, command := model.Update(keyRunes('y'))
	if command == nil {
		t.Fatal("confirm returned no archive command")
	}
	if _, _ = updated.Update(command()); len(source.archived) != 1 || source.archived[0] != "root-1" {
		t.Fatalf("archived = %#v, want [root-1]", source.archived)
	}

	// Delete uses the Delete key and the same confirmation.
	model.Update(keyPress(bubbletea.KeyDelete))
	if model.pendingLifecycle != "delete" {
		t.Fatalf("delete pending action = %q, want delete", model.pendingLifecycle)
	}
	if !strings.Contains(model.View(), "Permanently delete this task") {
		t.Fatalf("delete confirmation missing:\n%s", model.View())
	}
	updated, command = model.Update(keyPress(bubbletea.KeyEnter))
	if command == nil {
		t.Fatal("confirm returned no delete command")
	}
	if _, _ = updated.Update(command()); len(source.deleted) != 1 || source.deleted[0] != "root-1" {
		t.Fatalf("deleted = %#v, want [root-1]", source.deleted)
	}
}
