package appserver

import (
	"testing"
	"time"

	"codex_go/model"
	"codex_go/turn"

	"github.com/google/uuid"
)

func TestPlanIdentitiesRetainUnfinishedAndRotateCompleted(t *testing.T) {
	ids := &planItemIdentities{}
	first := ids.begin("attempt-1")
	parsed, err := uuid.Parse(first)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("plan ID=%q err=%v", first, err)
	}
	if retry := ids.begin("attempt-2"); retry != first {
		t.Fatalf("unfinished retry changed identity: %q != %q", retry, first)
	}
	if done := ids.complete("attempt-2"); done != first {
		t.Fatal("completion changed identity")
	}
	second := ids.begin("continuation")
	if second == first {
		t.Fatal("completed identity reused")
	}
	if old := ids.complete("attempt-2"); old != first {
		t.Fatal("persisting old completion changed identity")
	}
	if current := ids.begin("continuation"); current != second {
		t.Fatal("old completion changed active identity")
	}
}

func TestPlanStreamRetryAndContinuationMatchSavedItems(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	state := newResponsesStreamNotificationState(true, "turn-plan")
	emit := func(event *model.ResponsesStreamEvent) {
		router.notifyResponsesStreamEvent("thread-plan", "turn-plan", event, state)
	}
	start := func(response, item, text string) string {
		emit(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventCreated, ResponseID: response})
		emit(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputText, ItemID: item, Delta: text})
		return state.planItemID
	}
	text := "<proposed_plan>\nfirst\n</proposed_plan>"
	first := start("failed", "msg-failed", "<proposed_plan>\npartial")
	retry := start("retry", "msg-retry", text)
	if retry != first {
		t.Fatalf("unfinished retry ID %q != %q", retry, first)
	}
	emit(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, ItemID: "msg-retry", Item: &model.AgentItem{ID: "msg-retry", Type: "agent_message", Text: text}})
	secondText := "<proposed_plan>\nsecond\n</proposed_plan>"
	second := start("continuation", "msg-next", secondText)
	if first == second {
		t.Fatal("completed plan ID reused in continuation")
	}
	emit(&model.ResponsesStreamEvent{Kind: model.ResponsesStreamEventOutputDone, ItemID: "msg-next", Item: &model.AgentItem{ID: "msg-next", Type: "agent_message", Text: secondText}})
	result := &turn.AgentLoopResult{Responses: []*model.AgentResponse{
		{ResponseID: "retry", Message: text},
		{ResponseID: "continuation", Message: secondText},
	}}
	params := &turn.TurnStartParams{ThreadID: "thread-plan", CollaborationMode: map[string]any{"mode": "plan"}}
	for pass := 0; pass < 2; pass++ {
		items := router.sessionItemsForTurn("turn-plan", params, result, time.Now(), state.planIdentities)
		var saved []string
		for _, item := range items {
			if item.Type == "plan" {
				saved = append(saved, item.ID)
			}
		}
		if len(saved) != 2 || saved[0] != first || saved[1] != second {
			t.Fatalf("saved plan identities=%v, want %q and %q", saved, first, second)
		}
	}
	waitForItemStarted(t, sink, first)
	waitForItemStarted(t, sink, second)
}
