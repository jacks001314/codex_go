package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/turn"
)

// Mirrors Rust #45677's `Session::interrupt_turn_with_warning` behavior: an
// extension-requested interruption of the still-active turn cancels it and
// emits the interrupted thread-idle lifecycle transition.
func TestGuardianCircuitBreakerInterruptionEmitsIdleStatusLikeRust(t *testing.T) {
	router, _, childID := guardianNotificationTestRouter(t, true)
	threadID := string(childID)
	turnID := "turn-child"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	router.requireThreadStatus().NoteTurnStarted(threadID)
	if err := router.threads.ReserveTurn(threadID); err != nil {
		t.Fatalf("ReserveTurn() error = %v", err)
	}
	if err := router.threads.RegisterTurn(threadID, turnID, cancel, time.Now().UTC().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if got := router.requireThreadStatus().LoadedStatusForThread(threadID); got.Type != ActiveStatus().Type {
		t.Fatalf("status before interruption = %#v, want active", got)
	}

	router.interruptTurnForGuardianCircuitBreaker(threadID, turnID, "The action was denied three times.")

	if got := router.requireThreadStatus().LoadedStatusForThread(threadID); got.Type != IdleStatus().Type {
		t.Fatalf("status after interruption = %#v, want idle", got)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("the active turn was not cancelled")
	}
}

func TestGuardianConditionalInterruptionPreservesPendingInputLikeRust(t *testing.T) {
	tests := []struct {
		name        string
		requestedID string
		queueInput  bool
		wantAbort   bool
	}{
		{name: "current turn", requestedID: "turn-child", wantAbort: true},
		{name: "stale turn", requestedID: "turn-stale"},
		{name: "pending user input", requestedID: "turn-child", queueInput: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router, parentID, childID := guardianNotificationTestRouter(t, true)
			threadID := string(childID)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			router.requireThreadStatus().NoteTurnStarted(threadID)
			if err := router.threads.ReserveTurn(threadID); err != nil {
				t.Fatalf("ReserveTurn() error = %v", err)
			}
			if err := router.threads.RegisterTurn(threadID, "turn-child", cancel, time.Now().UTC().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
				t.Fatalf("RegisterTurn() error = %v", err)
			}
			if tc.queueInput {
				if err := router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{
					ThreadID: threadID,
					TurnID:   "turn-child",
					InputItems: []any{map[string]any{
						"type": "message",
						"role": "user",
						"content": []any{map[string]any{
							"type": "input_text",
							"text": "preserve this input",
						}},
					}},
				}); err != nil {
					t.Fatalf("Enqueue() error = %v", err)
				}
			}

			router.interruptTurnForGuardianCircuitBreaker(threadID, tc.requestedID, "denied repeatedly")

			select {
			case <-ctx.Done():
				if !tc.wantAbort {
					t.Fatal("the active turn was cancelled despite pending or stale input")
				}
			default:
				if tc.wantAbort {
					t.Fatal("the active turn was not cancelled")
				}
			}
			texts := guardianNotificationTexts(router.drainRuntimeAgentMessages(string(parentID)))
			if tc.wantAbort {
				if len(texts) != 1 {
					t.Fatalf("parent notifications = %#v, want one", texts)
				}
				return
			}
			if len(texts) != 0 {
				t.Fatalf("unexpected parent notification = %#v", texts)
			}
			if tc.queueInput && !router.requireSteerMailbox().HasPendingUserInput(threadID, "turn-child") {
				t.Fatal("pending user input was discarded")
			}
		})
	}
}
