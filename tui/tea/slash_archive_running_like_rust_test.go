package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust #50764 (dde5f8c5ae, codex-rs/tui/src/slash_command.rs +
// chatwidget/slash_dispatch.rs): `/archive` is available while a turn is
// running. Mirrors the Rust tests `slash_archive_is_disabled_while_task_running`
// (replaced by `slash_archive_cancellation_keeps_task_running`) and
// `slash_archive_confirmation_requests_current_thread_archive` in
// codex-rs/tui/src/chatwidget/tests/slash_commands.rs.
func TestSlashArchiveAvailableDuringTaskLikeRust(t *testing.T) {
	if !commandAvailableDuringTask(codextui.CommandArchive) {
		t.Fatal("commandAvailableDuringTask(archive) = false, want true while a task is running")
	}
	if !chatwidget.CommandAvailableDuringTask(codextui.CommandArchive) {
		t.Fatal("chatwidget.CommandAvailableDuringTask(archive) = false, want true")
	}
	item := bottompane.SlashCommandItem{Command: codextui.CommandArchive}
	if !item.AvailableDuringTask() {
		t.Fatal("SlashCommandItem.AvailableDuringTask(archive) = false, want true")
	}
	// Control: the neighbouring session commands stay disabled during a task.
	for _, command := range []codextui.Command{codextui.CommandNew, codextui.CommandDelete, codextui.CommandFork} {
		if commandAvailableDuringTask(command) {
			t.Fatalf("commandAvailableDuringTask(%s) = true, want false", command)
		}
	}
}

// Rust #50764 snapshot `slash_archive_running_confirmation_popup`: the running
// confirm dialog reads "This will stop the current turn and archive the
// session.", while the idle dialog keeps the original wording.
func TestSlashArchiveRunningConfirmationWarnsLikeRust(t *testing.T) {
	running := codextui.NewState(nil)
	running.SetStatus("running")
	model := NewModel(running, Options{Width: 80, Height: 24})
	model.openCurrentSessionActionConfirmation(codextui.SessionSelectionArchive)
	if model.modal == nil {
		t.Fatal("expected the archive confirmation modal while running")
	}
	if model.modal.body != "This will stop the current turn and archive the session." {
		t.Fatalf("running archive body = %q, want the running-turn warning", model.modal.body)
	}

	idle := codextui.NewState(nil)
	idle.SetStatus("idle")
	idleModel := NewModel(idle, Options{Width: 80, Height: 24})
	idleModel.openCurrentSessionActionConfirmation(codextui.SessionSelectionArchive)
	if idleModel.modal == nil {
		t.Fatal("expected the archive confirmation modal while idle")
	}
	if !strings.Contains(idleModel.modal.body, "Are you sure? This will archive the current session") {
		t.Fatalf("idle archive body = %q, want the original wording", idleModel.modal.body)
	}

	// Delete keeps its own wording even while a task runs.
	deleteState := codextui.NewState(nil)
	deleteState.SetStatus("running")
	deleteModel := NewModel(deleteState, Options{Width: 80, Height: 24})
	deleteModel.openCurrentSessionActionConfirmation(codextui.SessionSelectionDelete)
	if deleteModel.modal == nil || !strings.Contains(deleteModel.modal.body, "Cannot be undone") {
		t.Fatalf("delete body while running = %+v, want the delete wording", deleteModel.modal)
	}
}

// Rust #50764 `slash_archive_cancellation_keeps_task_running`: dismissing the
// confirmation leaves the turn running.
func TestSlashArchiveCancellationKeepsTaskRunningLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.openCurrentSessionActionConfirmation(codextui.SessionSelectionArchive)
	if model.modal == nil {
		t.Fatal("expected the archive confirmation modal while running")
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.modal != nil {
		t.Fatalf("esc left the archive confirmation open")
	}
	if !model.isTaskRunning() {
		t.Fatal("cancelling the archive confirmation stopped the running task")
	}
}
