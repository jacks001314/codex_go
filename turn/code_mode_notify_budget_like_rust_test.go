package turn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
	"codex_go/utils"
)

// Rust #44867: a yielded Code Mode cell keeps the truncation policy of the
// model that started it. A notification emitted after the session switched
// models must use the originating budget (including the serialization
// allowance), while the raw notification callback still sees the full text.
func TestCodeModeNotifyKeepsOriginatingBudgetAfterModelSwitchLikeRust(t *testing.T) {
	const text = "A_NOTIFICATION diagnostic line\n"
	notification := strings.Repeat(text, 500)

	registry := tool.NewRegistry()
	var notify tool.CodeModeNotifyFunc
	var dispatcher *ToolDispatcher
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName(tool.CodeModeExecToolName)}, func(_ context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		notify = invocation.Context["code_mode_notify"].(tool.CodeModeNotifyFunc)
		// The receiving model changes between steps; the cell's notification
		// must not adopt its wider budget.
		dispatcher.SetStepModel("model-b")
		notify(invocation.CallID, notification)
		return &tool.Output{Success: true, Body: "final"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	var notified string
	dispatcher = NewToolDispatcher(&ToolDispatcherOptions{
		Router:     tool.NewRouter(registry),
		Truncation: truncationPolicyPointer(utils.TokensPolicy(100)),
		TruncationForModel: func(modelID string) *utils.TruncationPolicy {
			if strings.TrimSpace(modelID) == "model-b" {
				return truncationPolicyPointer(utils.TokensPolicy(400))
			}
			return truncationPolicyPointer(utils.TokensPolicy(100))
		},
		OnCodeModeNotify: func(_ context.Context, _ string, raw string) {
			notified = raw
		},
	})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{ID: "exec-id", CallID: "exec-call", Type: "custom_tool_call", Name: tool.CodeModeExecToolName, Input: `notify("x")`}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].InputItems) != 1 {
		t.Fatalf("results = %#v", results)
	}
	item, ok := results[0].InputItems[0].(*ToolResponseItem)
	if !ok || item.CallID != "exec-call" || item.Type != "custom_tool_call_output" {
		t.Fatalf("notify item = %#v", results[0].InputItems[0])
	}
	want := utils.TruncateText(notification, utils.TokensPolicy(120))
	if got := item.Output.Text(); got != want {
		t.Fatalf("notification = %q, want the originating budget %q", got, want)
	}
	if got := item.Output.Text(); got == utils.TruncateText(notification, utils.TokensPolicy(480)) {
		t.Fatal("notification adopted the receiving model's budget")
	}
	if item.HistoryTruncationTokenLimit == nil || *item.HistoryTruncationTokenLimit != 120 {
		t.Fatalf("history token limit = %v, want 120", item.HistoryTruncationTokenLimit)
	}
	if notified != notification {
		t.Fatalf("raw notification lost text: %d bytes", len(notified))
	}
}

// A cell outlives the exec call that started it, so a notification emitted
// after the call returned cannot ride that call's drained input buffer. It is
// queued as pending input for the running turn (Rust inject_if_running).
func TestCodeModeNotifyAfterExecReturnInjectsIntoActiveTurnLikeRust(t *testing.T) {
	notification := strings.Repeat("B_NOTIFICATION diagnostic line\n", 500)
	registry := tool.NewRegistry()
	var notify tool.CodeModeNotifyFunc
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName(tool.CodeModeExecToolName)}, func(_ context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		notify = invocation.Context["code_mode_notify"].(tool.CodeModeNotifyFunc)
		return &tool.Output{Success: true, Body: "Script running with cell ID cell-1"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	var injected []any
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{
		Router:     tool.NewRouter(registry),
		Truncation: truncationPolicyPointer(utils.TokensPolicy(100)),
		InjectResponseItems: func(items []any) bool {
			injected = append(injected, items...)
			return true
		},
	})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{ID: "exec-id", CallID: "exec-call", Type: "custom_tool_call", Name: tool.CodeModeExecToolName, Input: `notify("x")`}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].InputItems) != 0 {
		t.Fatalf("results = %#v", results)
	}
	notify("exec-call", notification)
	if len(injected) != 1 {
		t.Fatalf("injected = %d items, want 1", len(injected))
	}
	item, ok := injected[0].(*ToolResponseItem)
	if !ok || item.CallID != "exec-call" {
		t.Fatalf("injected item = %#v", injected[0])
	}
	if want := utils.TruncateText(notification, utils.TokensPolicy(120)); item.Output.Text() != want {
		t.Fatalf("injected notification = %q, want %q", item.Output.Text(), want)
	}
	if item.HistoryTruncationTokenLimit == nil || *item.HistoryTruncationTokenLimit != 120 {
		t.Fatalf("history token limit = %v, want 120", item.HistoryTruncationTokenLimit)
	}
}

func TestRuntimeCodeModeResponseItemInjectorQueuesPendingInputLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	runtime := &Runtime{steerMailbox: mailbox}
	inject := runtime.codeModeResponseItemInjector("thread-1", "turn-1")
	if inject == nil {
		t.Fatal("injector is nil")
	}
	item := &ToolResponseItem{Type: "custom_tool_call_output", CallID: "exec-call", Output: NewFunctionCallOutputPayload("note", boolPtr(true))}
	if !inject([]any{item}) {
		t.Fatal("active turn rejected the notification")
	}
	drained := mailbox.DrainWithMetadata(&SteerDrainParams{ThreadID: "thread-1", TurnID: "turn-1"})
	if len(drained.InputItems) != 1 || drained.InputItems[0] != item {
		t.Fatalf("drained = %#v", drained.InputItems)
	}
	release := mailbox.BeginTurnFinish("thread-1", "turn-1")
	if inject([]any{item}) {
		t.Fatal("finishing turn accepted a notification")
	}
	release()
}

// The originating budget is a sidecar on the response item, never part of the
// model-visible payload (Rust keeps it in CodexHarnessMetadata).
func TestCodeModeNotificationBudgetStaysOutOfModelRequestLikeRust(t *testing.T) {
	limit := uint64(120)
	item := &ToolResponseItem{
		Type:                        "custom_tool_call_output",
		CallID:                      "exec-call",
		Name:                        tool.CodeModeExecToolName,
		Output:                      NewFunctionCallOutputPayload("note", boolPtr(true)),
		HistoryTruncationTokenLimit: &limit,
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "120") || strings.Contains(string(encoded), "fallback_token_limit_override") {
		t.Fatalf("notification budget leaked into the model request: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"call_id":"exec-call"`) {
		t.Fatalf("response item = %s", encoded)
	}
}

func truncationPolicyPointer(policy utils.TruncationPolicy) *utils.TruncationPolicy {
	return &policy
}
