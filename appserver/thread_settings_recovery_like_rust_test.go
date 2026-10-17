package appserver

import (
	"testing"

	"codex_go/turn"
)

// Rust #53879 (core/src/session/thread_settings.rs): a standalone settings
// update only invalidates continuation lineage when no task is registered.
// A running task keeps its lineage so the daemon can recover it after a
// restart; idle, reserved and finishing turns have no interrupted work.
//
// Go keeps no SessionState::last_started_turn_id: the recovery candidate is the
// live registered turn plus its recorded input (daemonRecoveryInterruptedTurn),
// so a settings update can only diverge if it drops or rewrites that turn.
func TestThreadSettingsUpdatePreservesRunningTurnRecoveryLikeRust(t *testing.T) {
	record := daemonRecoveryTestRootRecord("thread-root", "turn-1", true)
	router := daemonRecoveryTestRouter(t, record)
	params := &turn.TurnStartParams{
		ThreadID: "thread-root",
		Prompt:   "finish the task",
		Model:    "gpt-frozen",
	}
	if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if _, ok := router.DaemonRecoverySnapshot().Interrupted["thread-root"]; !ok {
		t.Fatalf("the running turn must be a recovery candidate before the settings update")
	}

	service := router.requireThreadExtras()
	nextModel := "gpt-next"
	nextEffort := "high"
	if _, err := service.UpdateSettings(&SettingsUpdateParams{
		ThreadID: "thread-root",
		Model:    &nextModel,
		Effort:   &nextEffort,
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}

	// The running turn keeps its continuation lineage: the daemon snapshot still
	// names the interrupted turn.
	interrupted, ok := router.DaemonRecoverySnapshot().Interrupted["thread-root"]
	if !ok {
		t.Fatalf("the settings update dropped the running turn's recovery lineage")
	}
	if interrupted.TurnID != "turn-1" {
		t.Fatalf("interrupted turn = %q, want turn-1", interrupted.TurnID)
	}
	// The turn's frozen settings are unaffected by the thread-level change.
	active := router.threads.ActiveTurn("thread-root")
	if active == nil || active.Params == nil || active.Params.Model != "gpt-frozen" {
		t.Fatalf("running turn params = %#v, want the frozen model", active)
	}
	if settings := service.Settings("thread-root"); settings.Model != "gpt-next" || settings.Effort == nil || *settings.Effort != "high" {
		t.Fatalf("thread settings = %#v, want the new model and effort", settings)
	}

	// Once the task is no longer registered, the same update behaves like the
	// idle case: nothing is left to recover.
	if _, ok := router.threads.ConsumeTurn("thread-root", "turn-1", false); !ok {
		t.Fatalf("ConsumeTurn() did not find the registered turn")
	}
	if _, err := service.UpdateSettings(&SettingsUpdateParams{ThreadID: "thread-root", Model: &nextModel}); err != nil {
		t.Fatalf("idle UpdateSettings() error = %v", err)
	}
	if snapshot := router.DaemonRecoverySnapshot(); len(snapshot.Interrupted) != 0 {
		t.Fatalf("Interrupted = %#v, want no candidate after the turn finished", snapshot.Interrupted)
	}
}
