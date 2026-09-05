package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/turn"
)

func TestSessionItemsForTurnPersistsTrustedConfigurationUpdates(t *testing.T) {
	router := &RuntimeRouter{}
	createdAt := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	params := &turn.TurnStartParams{
		ThreadID: "thread-cfg",
		Prompt:   "hello",
		AdditionalInputItems: []any{
			map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": "high"}},
			map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "extra"}}},
		},
	}
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{{
			ResponseID: "resp-cfg",
			Items: []model.AgentItem{{
				ID:   "msg-cfg",
				Type: "agent_message",
				Text: "done",
			}},
		}},
	}
	items := router.sessionItemsForTurn("turn-cfg", params, result, createdAt)
	if len(items) != 3 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Type != "message" || items[0].Role != "user" {
		t.Fatalf("first item = %#v", items[0])
	}
	if items[1].Type != "configuration_update" {
		t.Fatalf("second item = %#v", items[1])
	}
	metadata, ok := items[1].Data["harness_metadata"].(json.RawMessage)
	if !ok || string(metadata) != `{"harness_authored_configuration":true}` {
		t.Fatalf("configuration update metadata = %#v", items[1].Data["harness_metadata"])
	}
	reasoning, _ := items[1].Data["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("configuration update reasoning = %#v", reasoning)
	}
	if items[2].Type != "agent_message" {
		t.Fatalf("third item = %#v", items[2])
	}
}
