package protocol

import (
	"encoding/json"
	"testing"
)

func TestThreadStartedJSONShape(t *testing.T) {
	data, err := json.Marshal(ThreadStarted("thread-1"))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got := string(data)
	want := `{"type":"thread.started","thread_id":"thread-1"}`
	if got != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}

func TestAgentMessageItemCompletedJSONShape(t *testing.T) {
	event := ItemCompleted(AgentMessageItem("item-1", "hello"))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	got := string(data)
	want := `{"type":"item.completed","item":{"id":"item-1","type":"agent_message","text":"hello"}}`
	if got != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}

func TestToolItemJSONShape(t *testing.T) {
	started := ItemStarted(ToolCallItem("tool-call-1", "exec_command", `{"cmd":"date"}`))
	data, err := json.Marshal(started)
	if err != nil {
		t.Fatalf("Marshal started returned error: %v", err)
	}
	wantStarted := `{"type":"item.started","item":{"id":"tool-call-1","type":"tool_call","tool_name":"exec_command","input":"{\"cmd\":\"date\"}"}}`
	if string(data) != wantStarted {
		t.Fatalf("started json = %s, want %s", data, wantStarted)
	}

	completed := ItemCompleted(ToolOutputItem("tool-output-1", "exec_command", "ok", true))
	data, err = json.Marshal(completed)
	if err != nil {
		t.Fatalf("Marshal completed returned error: %v", err)
	}
	wantCompleted := `{"type":"item.completed","item":{"id":"tool-output-1","type":"tool_output","tool_name":"exec_command","output":"ok","success":true}}`
	if string(data) != wantCompleted {
		t.Fatalf("completed json = %s, want %s", data, wantCompleted)
	}

	withMetadata := ItemCompleted(ToolOutputItemWithMetadata("tool-output-2", "exec_command", "Approval required", false, map[string]any{
		"approval_required": true,
		"reason":            "command requested sandbox permissions",
	}))
	data, err = json.Marshal(withMetadata)
	if err != nil {
		t.Fatalf("Marshal metadata completed returned error: %v", err)
	}
	wantWithMetadata := `{"type":"item.completed","item":{"id":"tool-output-2","type":"tool_output","tool_name":"exec_command","output":"Approval required","success":false,"metadata":{"approval_required":true,"reason":"command requested sandbox permissions"}}}`
	if string(data) != wantWithMetadata {
		t.Fatalf("metadata completed json = %s, want %s", data, wantWithMetadata)
	}
}

func TestTodoListItemJSONShape(t *testing.T) {
	event := ItemCompleted(TodoListItem("todo-list-call-1", []TodoItem{
		{Text: "step one", Completed: false},
		{Text: "step two", Completed: true},
	}))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.completed","item":{"id":"todo-list-call-1","type":"todo_list","items":[{"text":"step one","completed":false},{"text":"step two","completed":true}]}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}

func TestDeltaEventJSONShape(t *testing.T) {
	event := AgentMessageDelta("msg-1", "hello ")
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"item.delta","delta":{"item_id":"msg-1","text":"hello "}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}

	event = ToolCallInputDelta("call-1", "call-1", "patch")
	data, err = json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want = `{"type":"item.delta","delta":{"item_id":"call-1","input":"patch","call_id":"call-1"}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}

func TestTurnTerminalEventJSONShape(t *testing.T) {
	completed := TurnCompleted(Usage{
		InputTokens:           1,
		CachedInputTokens:     2,
		OutputTokens:          3,
		ReasoningOutputTokens: 4,
	})
	data, err := json.Marshal(completed)
	if err != nil {
		t.Fatalf("Marshal completed returned error: %v", err)
	}
	wantCompleted := `{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":2,"output_tokens":3,"reasoning_output_tokens":4}}`
	if string(data) != wantCompleted {
		t.Fatalf("completed json = %s, want %s", data, wantCompleted)
	}

	failed := TurnFailed("model exploded")
	data, err = json.Marshal(failed)
	if err != nil {
		t.Fatalf("Marshal failed returned error: %v", err)
	}
	wantFailed := `{"type":"turn.failed","error":{"message":"model exploded"}}`
	if string(data) != wantFailed {
		t.Fatalf("failed json = %s, want %s", data, wantFailed)
	}
}

func TestRateLimitSnapshotEventJSONShape(t *testing.T) {
	minutes := int64(5 * 60)
	reset := int64(1710000000)
	balance := "0"
	event := RateLimitSnapshotEvent(RateLimitSnapshot{
		LimitID:   "codex",
		LimitName: "Codex",
		Primary: &RateLimitWindow{
			UsedPercent:        90,
			WindowDurationMins: &minutes,
			ResetsAt:           &reset,
		},
		Credits: &CreditsSnapshot{
			HasCredits: true,
			Unlimited:  false,
			Balance:    &balance,
		},
		PlanType:             "plus",
		RateLimitReachedType: "primary",
	})
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := `{"type":"response.rate_limits","rateLimit":{"limitId":"codex","limitName":"Codex","primary":{"usedPercent":90,"windowDurationMins":300,"resetsAt":1710000000},"credits":{"hasCredits":true,"unlimited":false,"balance":"0"},"planType":"plus","rateLimitReachedType":"primary"}}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}
