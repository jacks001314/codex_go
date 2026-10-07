package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/model"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust parity: #48982
// (core/tests/suite/scenarios_agent_message_board.rs::board_notifications_do_not_reopen_a_final_answer).
// A message-board notification must not make a recipient sample again after its
// final answer: mail that arrives while the turn still accepts delivery is kept
// for a later turn, and mail that arrives once delivery closed is skipped.

const notificationDeliveryMarker = "Late update."

func notificationDeliveryStore(t *testing.T, home string) *session.Store {
	t.Helper()
	store := session.NewStore(filepath.Join(home, "sessions"))
	for _, record := range []*session.Record{
		{ID: session.ThreadID("root-thread"), SessionID: "root-thread", CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), Metadata: session.Metadata{CWD: home, AgentPath: "/root"}},
		{ID: session.ThreadID("child-thread"), SessionID: "root-thread", ParentThreadID: session.ThreadID("root-thread"), CreatedAt: time.Unix(2, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(), Metadata: session.Metadata{CWD: home, AgentPath: "/root/worker", AgentDepth: 1}},
	} {
		if err := store.Save(record); err != nil {
			t.Fatalf("save record %s: %v", record.ID, err)
		}
	}
	return store
}

func notificationDeliveryPost() agentboard.PostPreview {
	return agentboard.PostPreview{
		PostMetadata: agentboard.PostMetadata{
			MessageID: "11111111-1111-4111-8111-111111111111", ChannelName: "design",
			Author: agent.AgentPath("/root/worker"), ThreadID: "child-thread",
		},
		TextPreview: notificationDeliveryMarker,
	}
}

func notificationDeliveryLanded(request model.AgentRequest) bool {
	raw, err := json.Marshal(request.InputItems)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), notificationDeliveryMarker)
}

// notificationBeforeFinalAgent posts to the recipient's board through the
// production host while its first sampling request is in flight, then answers
// with a final answer: the notification arrives while the turn still accepts
// mailbox delivery.
type notificationBeforeFinalAgent struct {
	mu         sync.Mutex
	host       *messageBoardHost
	requests   []model.AgentRequest
	deliveries []agentboard.NotificationDelivery
	deliverErr error
}

func (a *notificationBeforeFinalAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.requests = append(a.requests, *request)
	call := len(a.requests)
	a.mu.Unlock()
	if call == 1 && a.host != nil {
		delivery, err := a.host.Notify(ctx, "root-thread", notificationDeliveryPost())
		a.mu.Lock()
		a.deliveries = append(a.deliveries, delivery)
		a.deliverErr = err
		a.mu.Unlock()
	}
	return &model.AgentResponse{
		ResponseID: fmt.Sprintf("resp-%d", call),
		Message:    "Finished.",
		Items: []model.AgentItem{{
			ID: fmt.Sprintf("final-%d", call), Type: "agent_message", Text: "Finished.",
			Data: map[string]any{"phase": "final_answer"},
		}},
	}, nil
}

func TestMessageBoardNotificationBeforeFinalDoesNotReopenAnswerLikeRust(t *testing.T) {
	home := t.TempDir()
	runner := &notificationBeforeFinalAgent{}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(notificationDeliveryStore(t, home)),
		Turns:        turn.NewTurnService(),
		Agent:        runner,
		ThreadStatus: NewThreadStatusManager(),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	defer router.Close()
	runner.host = &messageBoardHost{router: router, tree: "root-thread", caller: "child-thread"}

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: "root-thread", Prompt: "Create the channel."}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start error: %+v", turnStart.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)

	runner.mu.Lock()
	count := len(runner.requests)
	deliveries := append([]agentboard.NotificationDelivery(nil), runner.deliveries...)
	deliverErr := runner.deliverErr
	runner.mu.Unlock()
	if deliverErr != nil {
		t.Fatalf("Notify error = %v", deliverErr)
	}
	if len(deliveries) != 1 || deliveries[0] != agentboard.NotificationAccepted {
		t.Fatalf("deliveries = %v, want [accepted] while the turn accepts delivery", deliveries)
	}
	if count != 1 {
		t.Fatalf("recipient sampled %d times, want 1 (notification reopened the final answer)", count)
	}
}

// notificationAfterFinalAgent answers with a final answer plus a tool call, so
// the turn keeps running (dispatching the rest of the response) after the phase
// switched to the next turn. The tool posts through the production host at that
// moment: delivery is closed, so the notification must be skipped.
type notificationAfterFinalAgent struct {
	mu       sync.Mutex
	host     *messageBoardHost
	requests []model.AgentRequest
}

func (a *notificationAfterFinalAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.requests = append(a.requests, *request)
	call := len(a.requests)
	a.mu.Unlock()
	if call == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-final-with-call",
			Items: []model.AgentItem{
				{ID: "final-1", Type: "agent_message", Text: "Finished.", Data: map[string]any{"phase": "final_answer"}},
				{ID: "call-1", Type: "function_call", Name: "runtime_notify", CallID: "call-1", Arguments: "{}"},
			},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: fmt.Sprintf("resp-%d", call),
		Message:    "done",
		Items:      []model.AgentItem{{ID: fmt.Sprintf("final-%d", call), Type: "agent_message", Text: "done", Data: map[string]any{"phase": "final_answer"}}},
	}, nil
}

func TestMessageBoardNotificationAfterFinalIsSkippedLikeRust(t *testing.T) {
	home := t.TempDir()
	runner := &notificationAfterFinalAgent{}
	mailbox := turn.NewSteerMailbox()
	var deliveries []agentboard.NotificationDelivery
	var deliverErr error
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("runtime_notify")}, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		delivery, err := runner.host.Notify(ctx, "root-thread", notificationDeliveryPost())
		deliveries = append(deliveries, delivery)
		deliverErr = err
		return &tool.Output{Success: true, Body: "posted"}, nil
	})); err != nil {
		t.Fatalf("register runtime_notify: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(notificationDeliveryStore(t, home)),
		Turns:        turn.NewTurnService(),
		Agent:        runner,
		ThreadStatus: NewThreadStatusManager(),
		SteerMailbox: mailbox,
		TurnRuntime:  turn.NewRuntime(&turn.RuntimeOptions{Agent: runner, Router: tool.NewRouter(registry), SteerMailbox: mailbox}),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	defer router.Close()
	runner.host = &messageBoardHost{router: router, tree: "root-thread", caller: "child-thread"}

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: "root-thread", Prompt: "Create the channel."}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start error: %+v", turnStart.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)

	runner.mu.Lock()
	count := len(runner.requests)
	markerInFollowUp := count > 1 && notificationDeliveryLanded(runner.requests[1])
	runner.mu.Unlock()
	if deliverErr != nil {
		t.Fatalf("Notify error = %v", deliverErr)
	}
	if markerInFollowUp {
		t.Fatalf("the closed-delivery notification was queued and reached the next sampling request (deliveries = %v)", deliveries)
	}
	if len(deliveries) != 1 || deliveries[0] != agentboard.NotificationSkippedInactive {
		t.Fatalf("deliveries = %v, want [skipped] once the turn's answer was finalized", deliveries)
	}
}
