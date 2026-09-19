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
