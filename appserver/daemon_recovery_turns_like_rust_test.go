package appserver

import (
	"testing"

	"codex_go/turn"
)

// Mirrors Rust #45807's turn-kind matrix: a managed daemon recovery snapshot
// records only a regular turn that is still running, and never a completed,
// cancelled, review, or compaction task.
func TestDaemonRecoverySnapshotTurnStatesLikeRust(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		router := daemonRecoveryTestRouter(t, daemonRecoveryTestRootRecord("thread-root", "turn-1", true))
		if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, &turn.TurnStartParams{
			ThreadID: "thread-root",
			Prompt:   "finish the task",
		}); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		snapshot := router.DaemonRecoverySnapshot()
		if interrupted, ok := snapshot.Interrupted["thread-root"]; !ok || interrupted.TurnID != "turn-1" {
			t.Fatalf("Interrupted = %#v, want the running turn recorded", snapshot.Interrupted)
		}
	})

	t.Run("completed", func(t *testing.T) {
		router := daemonRecoveryTestRouter(t, daemonRecoveryTestRootRecord("thread-root", "turn-1", true))
		if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, &turn.TurnStartParams{
			ThreadID: "thread-root",
			Prompt:   "finish the task",
		}); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		if _, ok := router.threads.ConsumeTurn("thread-root", "turn-1", false); !ok {
			t.Fatal("ConsumeTurn() did not find the active turn")
		}
		snapshot := router.DaemonRecoverySnapshot()
		if len(snapshot.Loaded) != 1 || snapshot.Loaded[0] != "thread-root" {
			t.Fatalf("Loaded = %#v, want the loaded root thread", snapshot.Loaded)
		}
		if len(snapshot.Interrupted) != 0 {
			t.Fatalf("Interrupted = %#v, want no resume for a completed turn", snapshot.Interrupted)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		router := daemonRecoveryTestRouter(t, daemonRecoveryTestRootRecord("thread-root", "turn-1", true))
		if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, &turn.TurnStartParams{
			ThreadID: "thread-root",
			Prompt:   "finish the task",
		}); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		if _, ok := router.cancelActiveRuntimeTurn("thread-root", "turn-1"); !ok {
			t.Fatal("cancelActiveRuntimeTurn() did not find the active turn")
		}
		snapshot := router.DaemonRecoverySnapshot()
		if len(snapshot.Loaded) != 1 || snapshot.Loaded[0] != "thread-root" {
			t.Fatalf("Loaded = %#v, want the loaded root thread", snapshot.Loaded)
		}
		if len(snapshot.Interrupted) != 0 {
			t.Fatalf("Interrupted = %#v, want no resume for a cancelled turn", snapshot.Interrupted)
		}
	})

	t.Run("compacting", func(t *testing.T) {
		router := daemonRecoveryTestRouter(t, daemonRecoveryTestRootRecord("thread-root", "turn-1", true))
		if err := router.threads.RegisterTurn("thread-root", "turn-1", nil, 0, &turn.TurnStartParams{
			ThreadID:   "thread-root",
			Originator: "compact",
		}); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		snapshot := router.DaemonRecoverySnapshot()
		if len(snapshot.Loaded) != 1 || snapshot.Loaded[0] != "thread-root" {
			t.Fatalf("Loaded = %#v, want the loaded root thread", snapshot.Loaded)
		}
		if len(snapshot.Interrupted) != 0 {
			t.Fatalf("Interrupted = %#v, want no resume for a compaction task", snapshot.Interrupted)
		}
	})
}
