package appserver

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// TestInterruptClosesLateCommandApprovalFromCompletedTurnLikeRust pins Rust
// #61841: a background command can ask for approval after its turn completed.
// The approval opens the command item, and interrupting a later turn cancels the
// pending request with a turn-transition error. The item must then complete as
// `declined`, keeping the original turn id and start time, instead of staying
// `inProgress` forever.
func TestInterruptClosesLateCommandApprovalFromCompletedTurnLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-test\"\napproval_policy = \"on-request\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{})
	t.Cleanup(func() { _ = router.Close() })
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	requests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests <- request
	}))

	const threadID = "thread-late-command"
	const firstTurnID = "turn-late-1"
	const secondTurnID = "turn-late-2"
	if err := router.threads.RegisterTurn(threadID, firstTurnID, nil, 0, &turn.TurnStartParams{ThreadID: threadID, CWD: cwd, Model: "gpt-test"}); err != nil {
		t.Fatalf("RegisterTurn(first) error = %v", err)
	}
	// The first turn already finished when the background command asked.
	router.requireThreadStatus().NoteTurnStartedForTurn(threadID, firstTurnID)
	router.requireThreadStatus().NoteTurnCompletedForTurn(threadID, firstTurnID)

	type approvalResult struct {
		decision tool.ShellApprovalDecision
		err      error
	}
	done := make(chan approvalResult, 1)
	go func() {
		decision, err := router.shellApprovalForTurn(threadID, firstTurnID, false)(
			context.Background(),
			&tool.ShellApprovalRequest{
				Request: &tool.ShellRequest{
					HookCommand:      "echo late",
					CWD:              cwd,
					ApprovalRequired: true,
				},
				Invocation: &tool.Invocation{CallID: "call-late"},
			},
		)
		done <- approvalResult{decision: decision, err: err}
	}()

	pending := waitForServerRequestFromChannel(t, requests, ServerRequestCommandExecutionApproval)
	params, ok := pending.Params.(*CommandExecutionRequestApprovalParams)
	if !ok || params.TurnID != firstTurnID || params.ItemID != "call-late" {
		t.Fatalf("approval params = %#v", pending.Params)
	}
	started := waitForItemStarted(t, sink, "call-late")
	startedAtMS, _ := started.Item["startedAtMs"].(int64)
	if started.Item["status"] != string(CommandExecutionInProgress) || startedAtMS == 0 {
		t.Fatalf("approval-opened item = %#v", started.Item)
	}

	// A second turn starts and is interrupted, which aborts every request the
	// thread still has pending.
	if _, ok := router.threads.ConsumeTurn(threadID, firstTurnID, false); !ok {
		t.Fatalf("first turn %q was not released", firstTurnID)
	}
	if err := router.threads.RegisterTurn(threadID, secondTurnID, nil, 0, &turn.TurnStartParams{ThreadID: threadID, CWD: cwd, Model: "gpt-test"}); err != nil {
		t.Fatalf("RegisterTurn(second) error = %v", err)
	}
	router.requireThreadStatus().NoteTurnStartedForTurn(threadID, secondTurnID)
	if !router.notifyTurnCompletedOnce(&TurnCompletedNotification{
		ThreadID: threadID,
		Turn:     Turn{ID: secondTurnID, Status: TurnStatusInterrupted},
	}) {
		t.Fatal("interrupted turn did not claim its terminal event")
	}

	select {
	case result := <-done:
		if !errors.Is(result.err, tool.ErrApprovalTurnTransition) {
			t.Fatalf("approval error = %v, want the turn-transition cancellation", result.err)
		}
		if result.decision.Approved {
			t.Fatalf("approval decision = %#v, want not approved", result.decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late approval kept waiting after the turn transition")
	}

	completed := waitForItemCompleted(t, sink, "call-late")
	if completed.TurnID != firstTurnID {
		t.Fatalf("completed turn = %q, want the original turn %q", completed.TurnID, firstTurnID)
	}
	if completed.Item["status"] != string(CommandExecutionDeclined) {
		t.Fatalf("completed item = %#v, want declined", completed.Item)
	}
	if got, _ := completed.Item["startedAtMs"].(int64); got != startedAtMS {
		t.Fatalf("completed startedAtMs = %v, want the original %v", completed.Item["startedAtMs"], startedAtMS)
	}

	// The tool result that arrives after the transition must not publish a
	// second completion for the same item.
	before := len(sink.List())
	router.notify(NotificationItemCompleted, &ItemCompletedNotification{
		Item: ThreadItemPayload{
			"id":     "call-late",
			"type":   "commandExecution",
			"status": string(CommandExecutionFailed),
		},
		ThreadID:      threadID,
		TurnID:        firstTurnID,
		CompletedAtMS: time.Now().UTC().UnixMilli(),
	})
	if after := len(sink.List()); after != before {
		t.Fatalf("duplicate completion was published: %d -> %d notifications", before, after)
	}
}

// TestLateCommandCompletionReachesReplacementSubscriberLikeRust pins the other
// half of Rust #61841: the completion must reach the thread's current
// subscribers even though the approval belonged to a connection that is gone.
func TestLateCommandCompletionReachesReplacementSubscriberLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:   NewRouter(session.NewStore(t.TempDir())),
		Turns:          turn.NewTurnService(),
		ThreadStatus:   NewThreadStatusManager(),
		ServerRequests: NewServerRequestBroker(),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	const threadID = "thread-late-subscriber"
	router.subscribeThreadConnection(threadID, "conn-original")
	router.subscribeThreadConnection(threadID, "conn-replacement")
	// The original connection that asked for the approval disconnected.
	router.unsubscribeThreadConnection(threadID, "conn-original")

	command := "echo late"
	params := &CommandExecutionRequestApprovalParams{
		ThreadID:    threadID,
		TurnID:      "turn-late-1",
		ItemID:      "call-late",
		StartedAtMS: 1_000,
		Command:     &command,
	}
	if !router.openCommandApprovalItem(threadID, params.TurnID, params) {
		t.Fatal("approval did not open the command item")
	}
	if !router.completeCommandApprovalItemDeclined(threadID, params.TurnID, params) {
		t.Fatal("late command approval was not completed")
	}
	completed := waitForItemCompleted(t, sink, "call-late")
	if completed.TurnID != "turn-late-1" || completed.Item["status"] != string(CommandExecutionDeclined) {
		t.Fatalf("completed item = %#v", completed)
	}
	if got, _ := completed.Item["startedAtMs"].(int64); got != 1_000 {
		t.Fatalf("startedAtMs = %v, want the original 1000", completed.Item["startedAtMs"])
	}
}
