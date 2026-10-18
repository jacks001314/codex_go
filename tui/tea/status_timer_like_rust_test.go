package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func statusTimerModel(t *testing.T, now *time.Time) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.now = func() time.Time { return *now }
	state.SetStatus("running")
	model.syncTaskRunningTimer()
	return model
}

// Rust #41913: modal wait time is excluded while a turn clock keeps running
// across status-row visibility changes.
func TestStatusTimerPausesForModalsLikeRust(t *testing.T) {
	base := time.Unix(100, 0)
	now := base
	model := statusTimerModel(t, &now)
	model.taskStartedAt = base
	model.statusTimer.ResetAt(0, base)

	now = base.Add(10 * time.Second)
	model.openModal(ModalRequestMsg{
		ID:      "approval",
		Title:   "Approve",
		Options: []ModalOption{{ID: "ok", Label: "OK"}},
	})
	if got := model.statusTimer.ElapsedAt(base.Add(30 * time.Second)); got != 10*time.Second {
		t.Fatalf("paused status elapsed = %v, want 10s", got)
	}

	now = base.Add(20 * time.Second)
	model.respondModal(true)
	if got := model.statusTimer.ElapsedAt(base.Add(25 * time.Second)); got != 15*time.Second {
		t.Fatalf("resumed status elapsed = %v, want 15s", got)
	}
}

func TestStatusTimerFeedsWorkingRowAndCompletionFallbackLikeRust(t *testing.T) {
	base := time.Unix(200, 0)
	now := base
	model := statusTimerModel(t, &now)
	model.taskStartedAt = base
	model.statusTimer.ResetAt(42*time.Second, base)
	now = base
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "42s") {
		t.Fatalf("working row did not use independent timer:\n%s", view)
	}

	model.statusTimer.ResetAt(125*time.Second, base)
	model.applyTurnCompleted(TurnCompletedMsg{})
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Worked for 2m 5s") {
		t.Fatalf("completion footer did not use status timer fallback:\n%s", view)
	}
}

func TestWindowsSandboxSetupStartsFreshStatusClockLikeRust(t *testing.T) {
	base := time.Unix(300, 0)
	now := base
	model := statusTimerModel(t, &now)
	model.statusTimer.ResetAt(125*time.Second, base)
	model.startWindowsSandboxSetupStatus("")
	if got := model.statusTimer.ElapsedAt(base); got != 0 {
		t.Fatalf("sandbox setup status elapsed = %v, want 0", got)
	}
}
