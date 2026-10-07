package appserver

import (
	"net/http"
	"reflect"
	"testing"

	"codex_go/codexapi"
	"codex_go/review"
	"codex_go/session"
)

// TestFailedReviewTurnPreservesLifecycleOrderLikeRust is the regression for
// Rust ab45264919 (#50804, "Preserve review lifecycle ordering on failure").
//
// Rust `core/src/session/review.rs::spawn_review_thread` now aborts the replaced
// tasks, clears the connector selection and emits the review-entry lifecycle
// *before* the review task starts (`sess.start_task(...)` moved below
// `emit_turn_item_started/completed`), so a review that fails immediately still
// yields `entered` -> `error` -> `exited` -> `complete`. The upstream regression
// is codex-rs/core/tests/suite/review.rs::review_overload_preserves_lifecycle_order,
// which drives a 503 `server_is_overloaded` response with retries disabled.
//
// Go already notifies the entered-review item before the review runtime starts
// (handleReviewStart: notifyEnteredReviewMode -> startReviewRuntimeAsync), and
// this test pins the ordering guarantee across the failure path: the review
// runtime's error must reach the client between the entered and exited review
// items instead of being swallowed by the fallback exit.
func TestFailedReviewTurnPreservesLifecycleOrderLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Reviews:      review.NewService(),
		ThreadStatus: NewThreadStatusManager(),
		// Mirrors the upstream mock server: HTTP 503 carrying the
		// `server_is_overloaded` code with retries disabled by the caller.
		Agent: &apiErrorRuntimeAgent{err: &codexapi.APIError{
			Kind:    codexapi.ErrorServerOverloaded,
			Status:  http.StatusServiceUnavailable,
			Message: "server_is_overloaded",
		}},
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start = %+v", threadStart)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	reviewStart := router.Handle(requestWithParams(t, IntID(2), MethodReviewStart, review.StartParams{
		ThreadID: threadID,
		Target:   review.APITarget{Type: "uncommittedChanges"},
	}))
	if reviewStart.Error != nil {
		t.Fatalf("review start = %+v", reviewStart)
	}
	turnID := reviewStart.Result.(*review.StartResponse).Turn.ID

	completed := waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	if completed.Turn.Error != nil {
		t.Fatalf("failed review turn error = %#v, want the fallback completion Rust emits", completed.Turn.Error)
	}

	var lifecycle []string
	var errorCodexInfo any
	for _, notification := range sink.List() {
		switch notification.Method {
		case NotificationItemStarted:
			payload, ok := notification.Params.(*ItemStartedNotification)
			if !ok || payload == nil || payload.TurnID != turnID {
				continue
			}
			if notificationItemMap(t, payload.Item)["type"] == "enteredReviewMode" {
				lifecycle = append(lifecycle, "entered")
			}
		case NotificationItemCompleted:
			payload, ok := notification.Params.(*ItemCompletedNotification)
			if !ok || payload == nil || payload.TurnID != turnID {
				continue
			}
			if notificationItemMap(t, payload.Item)["type"] == "exitedReviewMode" {
				lifecycle = append(lifecycle, "exited")
			}
		case NotificationError:
			payload, ok := notification.Params.(*ErrorNotification)
			if !ok || payload == nil || payload.TurnID != turnID {
				continue
			}
			errorCodexInfo = payload.Error.CodexErrorInfo
			lifecycle = append(lifecycle, "error")
		case NotificationTurnCompleted:
			payload, ok := notification.Params.(*TurnCompletedNotification)
			if !ok || payload == nil || payload.Turn.ID != turnID {
				continue
			}
			lifecycle = append(lifecycle, "complete")
		}
	}

	// codex-rs/core/tests/suite/review.rs::review_overload_preserves_lifecycle_order
	// asserts exactly this sequence.
	if want := []string{"entered", "error", "exited", "complete"}; !reflect.DeepEqual(lifecycle, want) {
		t.Fatalf("review lifecycle = %v, want %v (a failed review must not swallow its error before the exit item)", lifecycle, want)
	}
	if errorCodexInfo != "serverOverloaded" {
		t.Fatalf("review error codexErrorInfo = %q, want serverOverloaded", errorCodexInfo)
	}
}
