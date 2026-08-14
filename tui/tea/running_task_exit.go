package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/tui/chatwidget"
)

const (
	runningTaskExitViewID      = "running_task_exit"
	runningTaskExitCancel      = "cancel_task"
	runningTaskExitBackground  = "run_in_background"
	runningTaskExitStopAndQuit = "exit"
)

// openRunningTaskExitMenu presents the "task is still running" choices when
// Ctrl-C is pressed during a running task in a local daemon session with an
// empty composer (Rust #38447).
func (m *Model) openRunningTaskExitMenu() {
	if m == nil {
		return
	}
	items := []chatwidget.SelectionItem{
		{
			ID:              runningTaskExitCancel,
			Name:            "Cancel task",
			Description:     "Stop the current task and stay in Codex",
			DismissOnSelect: true,
		},
		{
			ID:              runningTaskExitBackground,
			Name:            "Run in background",
			Description:     "Exit Codex and leave the task running",
			DismissOnSelect: true,
		},
		{
			ID:              runningTaskExitStopAndQuit,
			Name:            "Exit",
			Description:     "Stop the current task and exit Codex",
			DismissOnSelect: true,
		},
	}
	m.openSelectionViewModal(ModalKindRunningTaskExit, chatwidget.SelectionView{
		ViewID:      runningTaskExitViewID,
		Title:       "Task is still running",
		Subtitle:    "Choose what happens to the current task.",
		Items:       items,
		AllowCancel: true,
	})
}

// applyRunningTaskExit applies a running-task exit choice.
func (m *Model) applyRunningTaskExit(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch strings.TrimSpace(optionID) {
	case runningTaskExitBackground:
		return bubbletea.Quit
	case runningTaskExitStopAndQuit:
		interrupt := m.interruptRunningTask()
		return bubbletea.Sequence(interrupt, bubbletea.Quit)
	default:
		return m.interruptRunningTask()
	}
}
