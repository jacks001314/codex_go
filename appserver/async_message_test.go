package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// TestFreeformAsyncToolAvailableLikeRust pins Rust #45124: the free-form
// send_message_to_user_async tool is offered only to root agents, and a root
// agent opts in through either the model catalog or the
// send_message_to_user_async feature flag. The retired send_async_message flag
// and the legacy question tool names never enable it.
func TestFreeformAsyncToolAvailableLikeRust(t *testing.T) {
	catalog := []string{tool.DefaultSendMessageToUserAsyncToolName}
	legacy := []string{tool.DefaultSendUserMessageAsyncToolName, tool.DefaultRequestUserInputAsyncToolName}
	cases := []struct {
		name     string
		root     bool
		catalog  []string
		feature  bool
		expected bool
	}{
		{"root_with_catalog_opt_in", true, catalog, false, true},
		{"root_with_feature_opt_in", true, nil, true, true},
		{"root_with_both_opt_ins", true, catalog, true, true},
		{"root_without_opt_in", true, nil, false, false},
		{"root_with_legacy_tools", true, legacy, false, false},
		{"subagent_with_catalog_opt_in", false, catalog, false, false},
		{"subagent_with_feature_opt_in", false, nil, true, false},
		{"subagent_with_both_opt_ins", false, catalog, true, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := freeformAsyncToolAvailable(testCase.root, testCase.catalog, testCase.feature); got != testCase.expected {
				t.Fatalf("freeformAsyncToolAvailable(root=%v, catalog=%v, feature=%v) = %v, want %v",
					testCase.root, testCase.catalog, testCase.feature, got, testCase.expected)
			}
		})
	}
}

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

func TestSessionItemForAppFreeformAsyncMessageCarriesDelivery(t *testing.T) {
	createdAt := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.PlainName(tool.DefaultSendMessageToUserAsyncToolName)},
		Output: &tool.Output{
			Success: true,
			Data: map[string]any{
				"async_message": map[string]any{"message": "blocker found", "delivery": "async"},
			},
		},
	}
	item, ok := sessionItemForAppAsyncMessage("turn-1", execution, createdAt, nil)
	if !ok || item.Type != "agent_message" || item.Text != "blocker found" {
		t.Fatalf("free-form async item = %#v, ok=%v", item, ok)
	}
	if got, _ := item.Metadata["delivery"].(string); got != "async" {
		t.Fatalf("delivery metadata = %q, want async", got)
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

// TestSessionItemForAppStructuredAsyncQuestions covers Rust #42178: the
// structured request_user_input_async tool becomes an async agent message whose
// item carries the questions for the TUI to render.
func TestSessionItemForAppStructuredAsyncQuestions(t *testing.T) {
	createdAt := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	questions := []any{
		map[string]any{"title": "Which database?", "options": []any{"Postgres", "SQLite"}},
		map[string]any{"title": "Deadline?"},
	}
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.PlainName(tool.DefaultRequestUserInputAsyncToolName)},
		Output: &tool.Output{
			Success: true,
			Data: map[string]any{
				"async_questions": map[string]any{
					"message":   "Which database?\n- Postgres\n- SQLite\n\nDeadline?",
					"questions": questions,
					"delivery":  "async",
				},
			},
		},
	}
	item, ok := sessionItemForAppAsyncMessage("turn-1", execution, createdAt, nil)
	if !ok {
		t.Fatal("structured async question item not produced")
	}
	if item.Type != "agent_message" || item.Role != "assistant" || item.Text != "Which database?\n- Postgres\n- SQLite\n\nDeadline?" {
		t.Fatalf("item = %#v", item)
	}
	if got, _ := item.Metadata["delivery"].(string); got != "async" {
		t.Fatalf("delivery metadata = %q, want async", got)
	}
	threadItem := BuildThreadItem(item)
	if threadItem.Delivery != "async" {
		t.Fatalf("thread item delivery = %q, want async", threadItem.Delivery)
	}
	emitted, _ := threadItem.Data["questions"].([]any)
	if len(emitted) != 2 {
		t.Fatalf("thread item questions = %#v", threadItem.Data["questions"])
	}
	first, _ := emitted[0].(map[string]any)
	if first["title"] != "Which database?" {
		t.Fatalf("first question = %#v", first)
	}
}

var _ = session.Item{}

// TestThreadItemAgentMessageWireShapeMatchesRust pins the v2
// `ThreadItem::AgentMessage` wire shape: Rust's schema always serializes
// `phase`, `memoryCitation`, `delivery`, and `questions`, with absent optionals
// as explicit nulls. The async question payload rides on `questions` (#42178),
// and async agent messages carry `delivery: "async"` (#39312).
func TestThreadItemAgentMessageWireShapeMatchesRust(t *testing.T) {
	questions := []any{
		map[string]any{"title": "Which database?", "options": []any{"Postgres", "SQLite"}},
		map[string]any{"title": "Deadline?"},
	}
	async := BuildThreadItem(session.Item{
		ID:   "agent-message-1",
		Type: "agent_message",
		Role: "assistant",
		Text: "need input",
		Metadata: map[string]any{
			"delivery":  "async",
			"questions": questions,
		},
	})
	decoded := marshaledThreadItem(t, async)
	if decoded["delivery"] != "async" {
		t.Fatalf("delivery = %#v, want async", decoded["delivery"])
	}
	payload, ok := decoded["questions"].([]any)
	if !ok || len(payload) != 2 {
		t.Fatalf("questions = %#v, want two entries", decoded["questions"])
	}
	first, _ := payload[0].(map[string]any)
	if first["title"] != "Which database?" {
		t.Fatalf("first question = %#v", first)
	}

	plain := marshaledThreadItem(t, BuildThreadItem(session.Item{
		ID:   "agent-message-2",
		Type: "agent_message",
		Role: "assistant",
		Text: "done",
	}))
	for _, key := range []string{"phase", "memoryCitation", "delivery", "questions"} {
		value, present := plain[key]
		if !present || value != nil {
			t.Fatalf("%s = %#v (present=%v), want explicit null", key, value, present)
		}
	}
}

func marshaledThreadItem(t *testing.T, item ThreadItem) map[string]any {
	t.Helper()
	raw, err := json.Marshal(&item)
	if err != nil {
		t.Fatalf("marshal thread item: %v", err)
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal thread item: %v", err)
	}
	return decoded
}
