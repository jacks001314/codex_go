package appserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
)

// TestRuntimeAgentControllerHonorsPriorCancellationLikeRust mirrors Rust
// cacdc46619 (#51063, "Honor prior cancellation before starting Codex
// delegates"). codex_delegate.rs::run_codex_thread_interactive now checks the
// cancellation token *before* `startup.hold_membership(runtime.admit_start()?)`
// and before the child session starts, so an already-cancelled delegate spawn
// reports `TurnAborted` instead of a runtime admission or startup error. The
// upstream regression,
// codex_delegate_tests.rs::run_codex_thread_interactive_respects_pre_cancelled_spawn,
// covers normal operation and a requested runtime shutdown.
//
// Go's delegate start path is runtimeAgentController.SpawnAgent, whose first
// statement honours prior cancellation (`if err := ctx.Err(); err != nil`) —
// before the spawn overrides are resolved, before an agent slot is reserved
// (the analogue of Rust's runtime admission, agent.ErrAgentLimitReached) and
// before the child turn is started. This test pins that guard: with the spawn
// budget exhausted the admission error wins for a live context, while the same
// exhausted budget still yields the cancellation for a pre-cancelled context.
func TestRuntimeAgentControllerHonorsPriorCancellationLikeRust(t *testing.T) {
	for _, admissionExhausted := range []bool{false, true} {
		name := "normal"
		if admissionExhausted {
			// Mirrors Rust's `local_agent_runtime.request_shutdown()` case: the
			// admission step would fail if the cancelled spawn ever reached it.
			name = "admission-exhausted"
		}
		t.Run(name, func(t *testing.T) {
			store := session.NewStore(t.TempDir())
			now := time.Now().UTC()
			parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
			if err := store.Create(parent); err != nil {
				t.Fatal(err)
			}
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
			defer router.Close()
			controller := newRuntimeAgentController(router, "parent", parent.Metadata.CWD, 1).(*runtimeAgentController)

			if admissionExhausted {
				// Fill the single spawn slot the controller admits into.
				slot, err := router.runtimeAgentRegistry(controller.rootID).ReserveSpawnSlot(1)
				if err != nil {
					t.Fatal(err)
				}
				defer slot.Cancel()
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			done := make(chan error, 1)
			go func() {
				_, err := controller.SpawnAgent(ctx, &agent.SpawnAgentArgs{})
				done <- err
			}()

			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("pre-cancelled delegate spawn error = %v, want context.Canceled (#51063)", err)
				}
			case <-time.After(time.Second):
				t.Fatal("pre-cancelled delegate spawn should not hang (#51063)")
			}

			records, err := store.AllRecords()
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				if record.ID != "parent" {
					t.Fatalf("pre-cancelled delegate spawn started thread %s", record.ID)
				}
			}

			if admissionExhausted {
				// The same admission budget rejects a live-context spawn, so the
				// cancellation above really did win over the admission error.
				if _, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{}); !errors.Is(err, agent.ErrAgentLimitReached) {
					t.Fatalf("live-context delegate spawn error = %v, want the admission error %v", err, agent.ErrAgentLimitReached)
				}
			}
		})
	}
}
