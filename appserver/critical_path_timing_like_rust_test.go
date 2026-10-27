package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/mcp"
	"codex_go/model"
	"codex_go/state"
	"codex_go/tooltiming"
	"codex_go/turn"
)

// Rust #57207: an interactive wait is charged to the direct-tool invocation
// that triggered it, and an unattributable wait invalidates the turn's capture
// instead of charging an unrelated call.

func newCriticalPathTestTiming(t *testing.T) (*TimingHandle, time.Time) {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{})
	params := &turn.TurnStartParams{ThreadID: "thread-1"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	active := router.activeRuntimeTurnStateSnapshot("thread-1", "turn-1")
	if active == nil || active.Timing == nil {
		t.Fatal("registered turn has no timing state")
	}
	start := time.Now()
	active.Timing.EnableCriticalPath()
	active.Timing.MarkTurnStarted(start)
	return &TimingHandle{router: router, timing: active.Timing}, start
}

type TimingHandle struct {
	router *RuntimeRouter
	timing *turn.TimingState
}

func TestUserWaitTimingAttributesBlockingTimeToOwningCallLikeRust(t *testing.T) {
	handle, start := newCriticalPathTestTiming(t)
	batch := tooltiming.NewBatch()
	blocking := handle.timing.BeginToolBlocking(start, batch)
	work := handle.timing.DirectToolTimings().StartTurnDirectCall("call-1", "turn-1", batch, start, handle.timing.SnapshotPath(start))
	owner := handle.router.userWaitTimingOwner("thread-1", "turn-1", "call-1")
	if owner == nil {
		t.Fatal("userWaitTimingOwner() = nil, want the registered invocation")
	}
	wait := handle.router.startUserWaitTiming("thread-1", "turn-1", owner)
	if wait == nil {
		t.Fatal("startUserWaitTiming() = nil, want a human-wait guard")
	}
	time.Sleep(6 * time.Millisecond)
	wait.End()
	work.Finish()
	blocking.CloseAt(time.Now())

	profile := handle.timing.CompleteProfile(time.Now())
	if profile.CriticalPath.Status != turn.CriticalPathStatusComplete {
		t.Fatalf("critical path status = %q, want complete", profile.CriticalPath.Status)
	}
	if profile.CriticalPath.Durations == nil || profile.CriticalPath.Durations.UserWaitMS < 3 {
		t.Fatalf("critical path durations = %#v, want a user-wait segment", profile.CriticalPath.Durations)
	}
}

func TestUserWaitTimingWithoutOwnerInvalidatesCaptureLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		callID  string
		foreign bool
	}{
		{name: "unknown call", callID: "missing"},
		{name: "foreign turn owner", callID: "call-1", foreign: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handle, start := newCriticalPathTestTiming(t)
			if testCase.foreign {
				handle.timing.DirectToolTimings().StartTurnDirectCall(testCase.callID, "another-turn", tooltiming.NewBatch(), start, handle.timing.SnapshotPath(start))
			}
			owner := handle.router.userWaitTimingOwner("thread-1", "turn-1", testCase.callID)
			if owner != nil {
				t.Fatalf("userWaitTimingOwner() = %#v, want nil", owner)
			}
			if wait := handle.router.startUserWaitTiming("thread-1", "turn-1", owner); wait != nil {
				t.Fatalf("startUserWaitTiming() = %#v, want nil", wait)
			}
			profile := handle.timing.CompleteProfile(time.Now())
			if profile.CriticalPath.Status != turn.CriticalPathStatusIncomplete {
				t.Fatalf("critical path status = %q, want incomplete", profile.CriticalPath.Status)
			}
		})
	}
}

// Rust guardian/decision.rs attributes a blocking automated review to the
// reviewed tool call, including when a caller supplies an explicit timing owner
// that differs from the review's display item.
func TestGuardianReviewTimingUsesOwningCallLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		ownedCallID string
		targetItem  string
		ctxCallID   string
		wantStatus  turn.CriticalPathStatus
		wantReview  bool
	}{
		{name: "target item owns the call", ownedCallID: "call-1", targetItem: "call-1", wantStatus: turn.CriticalPathStatusComplete, wantReview: true},
		{name: "context override wins", ownedCallID: "call-1", targetItem: "display-item", ctxCallID: "call-1", wantStatus: turn.CriticalPathStatusComplete, wantReview: true},
		{name: "unknown owner invalidates", ownedCallID: "call-1", targetItem: "display-item", wantStatus: turn.CriticalPathStatusIncomplete},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			timing := turn.NewTimingState()
			start := time.Now()
			timing.EnableCriticalPath()
			timing.MarkTurnStarted(start)
			batch := tooltiming.NewBatch()
			blocking := timing.BeginToolBlocking(start, batch)
			work := timing.DirectToolTimings().StartTurnDirectCall(testCase.ownedCallID, "turn-1", batch, start, timing.SnapshotPath(start))
			reviewer := &modelGuardianReviewer{
				agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
					t.Fatal("the full-access decision must not run the model review")
					return nil, nil
				}),
				store:   state.NewReviewStore(),
				metrics: state.NewTaskMetrics(),
				fullAccess: func(string, string) bool {
					time.Sleep(5 * time.Millisecond)
					return true
				},
				turnTiming: func(threadID, turnID string) *turn.TimingState {
					return timing
				},
			}
			ctx := context.Background()
			if testCase.ctxCallID != "" {
				ctx = withGuardianTimingCallID(ctx, testCase.ctxCallID)
			}
			decision, _, err := reviewer.Review(ctx, "thread-1", "turn-1", testCase.targetItem, state.Action{Type: "command", Command: "ls"})
			if err != nil || decision != state.DecisionApproved {
				t.Fatalf("Review() = (%q, %v), want the full-access approval", decision, err)
			}
			work.Finish()
			blocking.CloseAt(time.Now())
			profile := timing.CompleteProfile(time.Now())
			if profile.CriticalPath.Status != testCase.wantStatus {
				t.Fatalf("critical path status = %q, want %q", profile.CriticalPath.Status, testCase.wantStatus)
			}
			if testCase.wantReview {
				if profile.CriticalPath.Durations == nil || profile.CriticalPath.Durations.AutoReviewMS < 3 {
					t.Fatalf("critical path durations = %#v, want an auto-review segment", profile.CriticalPath.Durations)
				}
			}
		})
	}
}

// Rust session/mcp.rs: an elicitation request has no reliable owning tool
// invocation, so its human wait invalidates the turn's capture.
func TestMCPElicitationInvalidatesCriticalPathLikeRust(t *testing.T) {
	var gotThread, gotTurn string
	handler := &appserverMCPElicitationHandler{
		invalidateTiming: func(threadID, turnID string) {
			gotThread, gotTurn = threadID, turnID
		},
	}
	if _, err := handler.requestMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName: "docs", ThreadID: "thread-1", TurnID: "turn-1", Method: "elicitation/create",
	}); err != nil {
		t.Fatalf("requestMCPElicitation() error = %v", err)
	}
	if gotThread != "thread-1" || gotTurn != "turn-1" {
		t.Fatalf("invalidateTiming() = (%q, %q), want the elicitation's turn", gotThread, gotTurn)
	}
}

// Rust #57207 attributes network approvals to the trigger call, never to the
// approval request ID used for response correlation.
func TestNetworkApprovalTimingCallIDUsesTriggerCallIDLikeRust(t *testing.T) {
	owner := &activeNetworkApprovalCall{
		callID:  "approval-id",
		trigger: &guardianNetworkAccessTrigger{CallID: "call-1"},
	}
	if got := networkApprovalTimingCallID(owner); got != "call-1" {
		t.Fatalf("networkApprovalTimingCallID() = %q, want the trigger call", got)
	}
	if got := networkApprovalTimingCallID(nil); got != "" {
		t.Fatalf("networkApprovalTimingCallID(nil) = %q, want empty", got)
	}
}
