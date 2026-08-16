package appserver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// prewarmingRuntimeAgent records the agent requests like recordingRuntimeAgent
// and additionally implements the prewarm interface (guardianPrewarmer),
// optionally with a delay to simulate a slow startup websocket connect.
type prewarmingRuntimeAgent struct {
	*recordingRuntimeAgent
	prewarmDelay        time.Duration
	prewarmedID         string
	prewarmCalls        atomic.Int32
	sessionStartupCalls atomic.Int32
}

func (a *prewarmingRuntimeAgent) Prewarm(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.prewarmCalls.Add(1)
	if request != nil && request.Originator == "session_startup" {
		a.sessionStartupCalls.Add(1)
	}
	if a.prewarmDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(a.prewarmDelay):
		}
	}
	return &model.AgentResponse{ResponseID: a.prewarmedID}, nil
}

// TestRuntimeRouterSessionStartupPrewarmFeedsFirstTurnLikeRust verifies the
// session-startup prewarm (Rust core/src/session_startup_prewarm.rs): creating
// a thread schedules an asynchronous websocket prewarm, and the first regular
// turn consumes its response id as PreviousResponseID.
func TestRuntimeRouterSessionStartupPrewarmFeedsFirstTurnLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := &prewarmingRuntimeAgent{recordingRuntimeAgent: newRecordingRuntimeAgent("ok"), prewarmedID: "prewarm-resp-1"}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Model: "gpt-test"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	// The prewarm is scheduled asynchronously; wait for it to finish.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if state := router.startupPrewarmsSnapshot()[threadID]; state != nil && state.finished {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup prewarm did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The guardian-reviewer setup also prewarms the same agent; the
	// session-startup prewarm is distinguished by its originator.
	if got := agent.sessionStartupCalls.Load(); got != 1 {
		t.Fatalf("session-startup prewarm calls = %d, want 1 (total prewarm calls = %d)", got, agent.prewarmCalls.Load())
	}

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent.recordingRuntimeAgent)
	if request.PreviousResponseID != "prewarm-resp-1" {
		t.Fatalf("first turn PreviousResponseID = %q, want prewarm-resp-1", request.PreviousResponseID)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
}

// TestRuntimeRouterSessionStartupPrewarmSlowTurnProceedsWithoutLikeRust pins
// the Unavailable resolution: when the first turn starts before the prewarm
// finishes, the turn proceeds without it (empty PreviousResponseID).
func TestRuntimeRouterSessionStartupPrewarmSlowTurnProceedsWithoutLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := &prewarmingRuntimeAgent{recordingRuntimeAgent: newRecordingRuntimeAgent("ok"), prewarmDelay: 5 * time.Second, prewarmedID: "prewarm-resp-1"}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Model: "gpt-test"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent.recordingRuntimeAgent)
	if request.PreviousResponseID != "" {
		t.Fatalf("first turn PreviousResponseID = %q, want empty (prewarm not finished)", request.PreviousResponseID)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
}

// TestRuntimeRouterSessionStartupPrewarmSkipsWithoutCapableAgent pins the
// no-op path: an agent that does not implement the prewarm interface receives
// no prewarm call and the first turn proceeds without a previous response id.
func TestRuntimeRouterSessionStartupPrewarmSkipsWithoutCapableAgent(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Model: "gpt-test"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if request.PreviousResponseID != "" {
		t.Fatalf("first turn PreviousResponseID = %q, want empty", request.PreviousResponseID)
	}
	if len(router.startupPrewarmsSnapshot()) != 0 {
		t.Fatalf("prewarm state registered for a non-prewarming agent: %#v", router.startupPrewarmsSnapshot())
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
}
