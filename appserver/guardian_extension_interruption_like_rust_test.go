package appserver

import (
	"context"
	"os"
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

// TestGuardianCircuitBreakStructuredErrorLikeRust mirrors Rust #48796: strict
// interruptions expose tooManyDenials on the live and saved interrupted turn,
// while the default action preserves the legacy nil error. The error is carried
// by turn/completed, never by a separate error notification.
func TestGuardianCircuitBreakStructuredErrorLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		action string
	}{
		{name: "unset", action: ""},
		{name: "default", action: "default"},
		{name: "strict", action: "strict"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router, _, childID := guardianNotificationTestRouterWithCircuitBreakAction(t, testCase.action)
			sink := NewNotificationBuffer()
			router.SetNotificationSink(sink)
			threadID := string(childID)
			turnID := "turn-child"
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			record, err := router.threadRecord(childID, true, false)
			if err != nil {
				t.Fatalf("threadRecord() error = %v", err)
			}
			if err := router.services.ThreadRouter.createThreadRollout(record, record.CreatedAt); err != nil {
				t.Fatalf("createThreadRollout() error = %v", err)
			}
			router.requireThreadStatus().NoteTurnStarted(threadID)
			if err := router.threads.ReserveTurn(threadID); err != nil {
				t.Fatalf("ReserveTurn() error = %v", err)
			}
			if err := router.threads.RegisterTurn(threadID, turnID, cancel, time.Now().UTC().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
				t.Fatalf("RegisterTurn() error = %v", err)
			}

			wantMessage := "Guardian denied the action three times."
			router.interruptTurnForGuardianCircuitBreaker(threadID, turnID, wantMessage)
			select {
			case <-ctx.Done():
			default:
				t.Fatal("the active turn was not cancelled")
			}

			var completed *TurnCompletedNotification
			for _, notification := range sink.List() {
				if notification.Method == NotificationError {
					t.Fatalf("unexpected standalone error notification: %#v", notification.Params)
				}
				if notification.Method != NotificationTurnCompleted {
					continue
				}
				payload, ok := notification.Params.(*TurnCompletedNotification)
				if ok && payload != nil && payload.Turn.ID == turnID {
					completed = payload
				}
			}
			if completed == nil {
				t.Fatal("missing interrupted turn/completed notification")
			}
			if completed.Turn.Status != TurnStatusInterrupted {
				t.Fatalf("live status = %q, want interrupted", completed.Turn.Status)
			}
			if testCase.action == "strict" {
				if completed.Turn.Error == nil || completed.Turn.Error.Message != wantMessage || completed.Turn.Error.CodexErrorInfo != "tooManyDenials" {
					t.Fatalf("live interrupted error = %#v", completed.Turn.Error)
				}
			} else if completed.Turn.Error != nil {
				t.Fatalf("default interrupted error = %#v, want nil", completed.Turn.Error)
			}

			record, err = router.threadRecord(childID, true, true)
			if err != nil {
				t.Fatalf("threadRecord() error = %v", err)
			}
			var saved *Turn
			for _, candidate := range turnsFromRecord(record) {
				if candidate.ID == turnID {
					value := candidate
					saved = &value
					break
				}
			}
			if saved == nil {
				t.Fatalf("saved turns = %#v", turnsFromRecord(record))
			}
			if saved.Status != TurnStatusInterrupted {
				path := router.services.ThreadRouter.threadRolloutPath(record)
				contents, _ := os.ReadFile(path)
				t.Fatalf("saved status = %q, want interrupted; snapshots=%#v path=%s contents=%s", saved.Status, record.Metadata.RolloutTurns, path, contents)
			}
			if testCase.action == "strict" {
				if saved.Error == nil || saved.Error.Message != wantMessage || saved.Error.CodexErrorInfo != "tooManyDenials" {
					t.Fatalf("saved interrupted error = %#v", saved.Error)
				}
			} else if saved.Error != nil {
				t.Fatalf("saved default interrupted error = %#v, want nil", saved.Error)
			}
		})
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
