package appserver

import (
	"testing"
	"time"

	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

func TestSessionItemForAppAsyncMessageCarriesDelivery(t *testing.T) {
	createdAt := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.PlainName("send_user_message_async")},
		Output: &tool.Output{
			Success: true,
			Data: map[string]any{
				"async_message": map[string]any{"message": "still working", "delivery": "async"},
			},
		},
	}
	item, ok := sessionItemForAppAsyncMessage("turn-1", execution, createdAt, nil)
	if !ok {
		t.Fatal("async message item not produced")
	}
	if item.Type != "agent_message" || item.Role != "assistant" || item.Text != "still working" {
		t.Fatalf("item = %#v", item)
	}
	if got, _ := item.Metadata["delivery"].(string); got != "async" {
		t.Fatalf("delivery metadata = %q, want async", got)
	}
	threadItem := BuildThreadItem(item)
	if threadItem.Delivery != "async" {
		t.Fatalf("thread item delivery = %q, want async", threadItem.Delivery)
	}
}

func TestFinalAgentMessageSummarySkipsAsyncMessages(t *testing.T) {
	items := []ThreadItem{
		{ID: "async-1", Type: "agent_message", Text: "still working", Delivery: "async"},
		{ID: "final-1", Type: "agent_message", Text: "done"},
	}
	if summary := finalAgentMessageSummary(items); len(summary) != 1 || summary[0].ID != "final-1" {
		t.Fatalf("final summary = %#v, want final-1 only", summary)
	}
	asyncOnly := []ThreadItem{{ID: "async-1", Type: "agent_message", Text: "still working", Delivery: "async"}}
	if summary := finalAgentMessageSummary(asyncOnly); len(summary) != 0 {
		t.Fatalf("async-only summary = %#v, want empty", summary)
	}
	if got := lastAgentMessageFromThreadItems(asyncOnly); got != "" {
		t.Fatalf("last async message = %q, want empty", got)
	}
}

func TestSessionItemForAppAsyncMessageIgnoresOtherTools(t *testing.T) {
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.PlainName("exec_command")},
		Output:     &tool.Output{Success: true},
	}
	if item, ok := sessionItemForAppAsyncMessage("turn-1", execution, time.Now(), nil); ok {
		t.Fatalf("unexpected async message item = %#v", item)
	}
}

var _ = session.Item{}
