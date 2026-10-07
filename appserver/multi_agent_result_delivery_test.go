package appserver

import (
	"errors"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

func multiAgentResultDeliveryCounters(metrics *state.TaskMetrics) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == telemetry.MultiAgentResultDeliveryMetric {
			out = append(out, record)
		}
	}
	return out
}

// Rust #51331 (41acdad246, core/src/agent/control/completion.rs): handing a
// terminal child result to its parent increments
// `codex.multi_agent.result_delivery` with outcome=queued when the parent
// accepts the result and outcome=failed when delivery errors. Rust has no
// dedicated test for the counter; the send_inter_agent_communication result is
// what completion.rs maps onto the two arms.
func TestMultiAgentResultDeliveryRecordsOutcomeLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	home := t.TempDir()
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	for _, record := range []*session.Record{
		{ID: "parent-thread", SessionID: "parent-thread", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root", MultiAgentVersion: string(agent.VersionV2),
		}},
		{ID: "child-thread", SessionID: "child-thread", ParentThreadID: "parent-thread", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, Source: "subagent:thread_spawn", AgentPath: "/root/worker", MultiAgentVersion: string(agent.VersionV2),
		}},
	} {
		if err := store.Save(record); err != nil {
			t.Fatalf("save thread error = %v", err)
		}
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("child done"),
		ThreadStatus: NewThreadStatusManager(),
		TurnMetrics:  metrics,
		DefaultCWD:   cwd,
	})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("child-thread", false)

	// A completed child turn delivers its terminal result to the parent.
	start := router.Handle(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{
		ThreadID:     "child-thread",
		Prompt:       "child work",
		ParentTurnID: "parent-turn-1",
	}))
	if start.Error != nil {
		t.Fatalf("turn start error: %+v", start.Error)
	}
	childTurnID := start.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, childTurnID, TurnStatusCompleted)

	counters := multiAgentResultDeliveryCounters(metrics)
	if len(counters) != 1 {
		t.Fatalf("result delivery counters = %d, want 1", len(counters))
	}
	if counters[0].Kind != "counter" || counters[0].Inc != 1 ||
		counters[0].Tags["outcome"] != telemetry.MultiAgentResultDeliveryOutcomeQueued {
		t.Fatalf("delivery counter = %#v, want a counter tagged outcome=queued", counters[0])
	}

	// An active parent turn takes the mailbox branch, which reports the queue
	// outcome the same way.
	if err := router.threads.RegisterTurn("parent-thread", "parent-turn-2", nil, 0, nil); err != nil {
		t.Fatalf("register parent turn error = %v", err)
	}
	router.deliverRuntimeAgentCompletion("child-thread", agent.AgentMessageStatus{
		Kind: agent.AgentMessageStatusCompleted, Message: "child finished again",
	})
	counters = multiAgentResultDeliveryCounters(metrics)
	if len(counters) != 2 {
		t.Fatalf("result delivery counters after the mailbox delivery = %d, want 2", len(counters))
	}
	if counters[1].Tags["outcome"] != telemetry.MultiAgentResultDeliveryOutcomeQueued {
		t.Fatalf("mailbox delivery counter = %#v, want outcome=queued", counters[1])
	}
	pending := router.requireSteerMailbox().Drain(&turn.SteerDrainParams{ThreadID: "parent-thread", TurnID: "parent-turn-2"})
	if len(pending) == 0 {
		t.Fatalf("the parent turn received no queued result")
	}

	// The failed arm mirrors Rust's delivery error; Go's enqueue rejects a nil
	// mailbox, which is the only reachable failure shape.
	router.recordMultiAgentResultDelivery(errors.New("delivery failed"))
	counters = multiAgentResultDeliveryCounters(metrics)
	if len(counters) != 3 {
		t.Fatalf("result delivery counters after a failure = %d, want 3", len(counters))
	}
	if counters[2].Tags["outcome"] != telemetry.MultiAgentResultDeliveryOutcomeFailed {
		t.Fatalf("failed delivery counter = %#v, want outcome=failed", counters[2])
	}
}
