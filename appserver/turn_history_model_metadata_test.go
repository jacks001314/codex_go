package appserver

import (
	"testing"
	"time"

	"codex_go/model"
	"codex_go/turn"
)

// TestSessionItemsUseOriginatingResponseModelLikeRust covers #44243: every
// recorded response item carries the model that produced that sampling step,
// not the model captured at turn start.
func TestSessionItemsUseOriginatingResponseModelLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	params := &turn.TurnStartParams{ThreadID: "thread-model", Model: "turn-start-model"}
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{
			{Model: "step-one-model", Items: []model.AgentItem{{ID: "one", Type: "message", Text: "one"}}},
			{Model: "step-two-model", Items: []model.AgentItem{{ID: "two", Type: "message", Text: "two"}}},
		},
	}
	items := router.sessionItemsForTurn("turn-model", params, result, time.Now().UTC())
	byID := map[string]map[string]any{}
	for _, item := range items {
		byID[item.ID] = item.Metadata
	}
	if got := byID["one"]["model"]; got != "step-one-model" {
		t.Fatalf("step one model = %#v", got)
	}
	if got := byID["two"]["model"]; got != "step-two-model" {
		t.Fatalf("step two model = %#v", got)
	}
}
