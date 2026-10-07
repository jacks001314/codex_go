package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

type subAgentCompletedActivityAgent struct{}

func (a *subAgentCompletedActivityAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	return &model.AgentResponse{
		ResponseID: "resp-subagent-completed",
		Message:    "child done",
		Items:      []model.AgentItem{{ID: "msg-child", Type: "agent_message", Text: "child done"}},
	}, nil
}

// Mirrors Rust #40437 / #46552: a successful Multi-Agent V2 thread-spawn child
// records a completed sub-agent activity item on the parent turn that spawned
// it, even though that parent turn has already finished.
func TestRuntimeRouterChildCompletionRecordsActivityOnParentTurnLikeRust(t *testing.T) {
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	records := []*session.Record{
		{ID: "parent-thread", SessionID: "parent-thread", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root", MultiAgentVersion: string(agent.VersionV2),
		}},
		{ID: "child-thread", SessionID: "child-thread", ParentThreadID: "parent-thread", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, Source: "subagent:thread_spawn", ThreadSource: string(ThreadSourceKindSubAgentThreadSpawn),
			AgentPath: "/root/worker", MultiAgentVersion: string(agent.VersionV2),
		}},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        &subAgentCompletedActivityAgent{},
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("child-thread", false)

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

	record, err := store.Read("parent-thread", true, true)
	if err != nil {
		t.Fatalf("read parent thread: %v", err)
	}
	var activity *session.Item
	for i := range record.Items {
		if strings.EqualFold(record.Items[i].Type, "subAgentActivity") {
			activity = &record.Items[i]
		}
	}
	if activity == nil {
		t.Fatalf("parent thread has no completed sub-agent activity: %#v", record.Items)
	}
	if activity.ID != "subagent-completed-"+childTurnID {
		t.Fatalf("activity id = %q, want subagent-completed-%s", activity.ID, childTurnID)
	}
	if activity.Data["kind"] != "completed" || activity.Data["agentPath"] != "/root/worker" || activity.Data["agentThreadId"] != "child-thread" {
		t.Fatalf("activity data = %#v", activity.Data)
	}
	if activity.Metadata["turnId"] != "parent-turn-1" {
		t.Fatalf("activity turn = %#v, want parent-turn-1", activity.Metadata)
	}

	var started, completed bool
	for _, notification := range sink.List() {
		switch notification.Method {
		case NotificationItemStarted:
			payload, ok := notification.Params.(*ItemStartedNotification)
			if !ok || payload.ThreadID != "parent-thread" || payload.TurnID != "parent-turn-1" {
				continue
			}
			item := notificationItemMap(t, payload.Item)
			if item["type"] == "subAgentActivity" && item["kind"] == "completed" && item["agentPath"] == "/root/worker" {
				started = true
			}
		case NotificationItemCompleted:
			payload, ok := notification.Params.(*ItemCompletedNotification)
			if !ok || payload.ThreadID != "parent-thread" || payload.TurnID != "parent-turn-1" {
				continue
			}
			item := notificationItemMap(t, payload.Item)
			if item["type"] == "subAgentActivity" && item["kind"] == "completed" && item["agentPath"] == "/root/worker" {
				completed = true
			}
		}
	}
	if !started || !completed {
		t.Fatalf("parent-turn activity notifications started=%v completed=%v", started, completed)
	}
}

// subAgentCompletedRoutingFixture builds the minimal runtime fixture for the
// #51402 initiating-agent completion routing: a root thread with two sibling
// thread-spawn children (`/root/worker` and `/root/requester`).
type subAgentCompletedRoutingFixture struct {
	store  *session.Store
	router *RuntimeRouter
	sink   *NotificationBuffer
}

func newSubAgentCompletedRoutingFixture(t *testing.T) *subAgentCompletedRoutingFixture {
	t.Helper()
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	records := []*session.Record{
		{ID: "parent-thread", SessionID: "parent-thread", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root", MultiAgentVersion: string(agent.VersionV2),
		}},
		{ID: "worker-thread", SessionID: "worker-thread", ParentThreadID: "parent-thread", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, Source: "subagent:thread_spawn", ThreadSource: string(ThreadSourceKindSubAgentThreadSpawn),
			AgentPath: "/root/worker", MultiAgentVersion: string(agent.VersionV2),
		}},
		{ID: "requester-thread", SessionID: "requester-thread", ParentThreadID: "parent-thread", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, Source: "subagent:thread_spawn", ThreadSource: string(ThreadSourceKindSubAgentThreadSpawn),
			AgentPath: "/root/requester", MultiAgentVersion: string(agent.VersionV2),
		}},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatalf("save %s: %v", record.ID, err)
		}
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        &subAgentCompletedActivityAgent{},
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	router.requireThreadStatus().UpsertThread("worker-thread", false)
	// The live agent registry owns agent-path -> thread resolution
	// (Rust `agent/control/target.rs` resolve_path_reference).
	router.agentRegistry.RegisterSpawnedThread(agent.Metadata{ThreadID: "worker-thread", Path: "/root/worker"})
	router.agentRegistry.RegisterSpawnedThread(agent.Metadata{ThreadID: "requester-thread", Path: "/root/requester"})
	return &subAgentCompletedRoutingFixture{store: store, router: router, sink: sink}
}

func (f *subAgentCompletedRoutingFixture) startWorkerTurn(t *testing.T, params turn.TurnStartParams) string {
	t.Helper()
	params.ThreadID = "worker-thread"
	if strings.TrimSpace(params.Prompt) == "" {
		params.Prompt = "follow-up work"
	}
	start := f.router.Handle(requestWithInternalParams(MethodTurnStart, params))
	if start.Error != nil {
		t.Fatalf("turn start error: %+v", start.Error)
	}
	turnID := start.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, f.sink, turnID, TurnStatusCompleted)
	return turnID
}

func (f *subAgentCompletedRoutingFixture) completedActivity(t *testing.T, threadID string) *session.Item {
	t.Helper()
	record, err := f.store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read %s: %v", threadID, err)
	}
	for i := range record.Items {
		if strings.EqualFold(record.Items[i].Type, "subAgentActivity") {
			item := record.Items[i]
			return &item
		}
	}
	return nil
}

// Mirrors Rust #51402 (551bd409eb) `core/src/agent/control/completion.rs:44-70`
// and its counterpart suite test
// `multi_agent_v2_peer_followup_completion_notifies_initiating_turn`
// (core/tests/suite/subagent_notifications.rs): a child turn started by another
// agent's triggered communication reports its completed activity to that
// initiating agent's thread rather than to the child's direct parent, while a
// triggered sender equal to the direct parent keeps the original routing and an
// initiator that no live agent owns drops the notice.
func TestSubAgentCompletedActivityRoutesToInitiatingAgentLikeRust(t *testing.T) {
	t.Run("initiating peer receives the activity", func(t *testing.T) {
		fixture := newSubAgentCompletedRoutingFixture(t)
		childTurnID := fixture.startWorkerTurn(t, turn.TurnStartParams{
			ParentTurnID:        "requester-turn-1",
			InitiatingAgentPath: "/root/requester",
		})
		activity := fixture.completedActivity(t, "requester-thread")
		if activity == nil {
			t.Fatalf("initiating requester thread has no completed sub-agent activity")
		}
		if activity.ID != "subagent-completed-"+childTurnID {
			t.Fatalf("activity id = %q, want subagent-completed-%s", activity.ID, childTurnID)
		}
		if activity.Data["kind"] != "completed" || activity.Data["agentThreadId"] != "worker-thread" || activity.Data["agentPath"] != "/root/worker" {
			t.Fatalf("activity data = %#v", activity.Data)
		}
		if activity.Metadata["turnId"] != "requester-turn-1" {
			t.Fatalf("activity turn = %#v, want requester-turn-1", activity.Metadata)
		}
		if parentActivity := fixture.completedActivity(t, "parent-thread"); parentActivity != nil {
			t.Fatalf("direct parent must not receive the rerouted activity: %#v", parentActivity.Data)
		}
	})

	t.Run("initiating equal to the direct parent stays on the parent", func(t *testing.T) {
		fixture := newSubAgentCompletedRoutingFixture(t)
		childTurnID := fixture.startWorkerTurn(t, turn.TurnStartParams{
			ParentTurnID:        "parent-turn-1",
			InitiatingAgentPath: "/root",
		})
		activity := fixture.completedActivity(t, "parent-thread")
		if activity == nil {
			t.Fatalf("direct parent thread has no completed sub-agent activity")
		}
		if activity.ID != "subagent-completed-"+childTurnID {
			t.Fatalf("activity id = %q, want subagent-completed-%s", activity.ID, childTurnID)
		}
		if activity.Metadata["turnId"] != "parent-turn-1" {
			t.Fatalf("activity turn = %#v, want parent-turn-1", activity.Metadata)
		}
		if requesterActivity := fixture.completedActivity(t, "requester-thread"); requesterActivity != nil {
			t.Fatalf("requester must not receive a direct-parent activity: %#v", requesterActivity.Data)
		}
	})

	t.Run("unresolvable initiator drops the activity", func(t *testing.T) {
		fixture := newSubAgentCompletedRoutingFixture(t)
		fixture.startWorkerTurn(t, turn.TurnStartParams{
			ParentTurnID:        "requester-turn-1",
			InitiatingAgentPath: "/root/ghost",
		})
		for _, threadID := range []string{"parent-thread", "requester-thread"} {
			if activity := fixture.completedActivity(t, threadID); activity != nil {
				t.Fatalf("unresolvable initiator must drop the activity, but %s received %#v", threadID, activity.Data)
			}
		}
	})
}
