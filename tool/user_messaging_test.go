package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	codexctx "codex_go/context"
)

type userMessagingHookTestExecutor struct {
	*ExecutorFunc
	hookName string
	success  bool
}

func (e *userMessagingHookTestExecutor) PostToolUsePayload(invocation *Invocation, output *Output) (*PostToolUsePayload, bool) {
	return &PostToolUsePayload{
		ToolName:     &HookToolName{Name: e.hookName},
		ToolUseID:    invocation.CallID,
		ToolInput:    functionHookInput(invocation.Payload.Arguments),
		ToolResponse: output.Body,
	}, true
}

func (e *userMessagingHookTestExecutor) WithUpdatedHookInput(invocation *Invocation, updatedInput any) (*Invocation, error) {
	data, err := json.Marshal(updatedInput)
	if err != nil {
		return nil, err
	}
	updated := *invocation
	updated.Payload.Arguments = string(data)
	return &updated, nil
}

// Rust #47030 captures the successful send after input hooks, so the assistant
// context carries exactly the text the host was asked to deliver.
func TestRouterCapturesRewrittenUserMessagingTextLikeRust(t *testing.T) {
	registry := NewRegistry()
	executor := &userMessagingHookTestExecutor{
		ExecutorFunc: NewExecutorFunc(Spec{Name: NamespacedName("mcp__codex_apps", "user_messaging_send_message")}, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true, Body: "Message sent."}, nil
		}),
		hookName: "mcp__codex_apps__user_messaging_send_message",
		success:  true,
	}
	if err := registry.Register(executor); err != nil {
		t.Fatalf("register: %v", err)
	}
	hooks := &fakeToolHooks{pre: &PreToolUseHookOutcome{
		UpdatedInput: map[string]any{"text": "Rewritten question after the hook."},
	}}

	output, err := NewRouter(registry).DispatchWithHooks(context.Background(), &Invocation{
		CallID:   "message-call",
		ToolName: NamespacedName("mcp__codex_apps", "user_messaging_send_message"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"text":"Original question before the hook."}`},
	}, hooks)
	if err != nil {
		t.Fatalf("DispatchWithHooks() error = %v", err)
	}
	if got, want := output.DeliveredAssistantMessage, "Rewritten question after the hook."; got != want {
		t.Fatalf("delivered assistant message = %q, want %q", got, want)
	}
}

func TestRouterDispatchCapturesUserMessagingWithoutHooksLikeRust(t *testing.T) {
	registry := NewRegistry()
	executor := &userMessagingHookTestExecutor{
		ExecutorFunc: NewExecutorFunc(Spec{Name: NamespacedName("mcp__codex_apps", "user_messaging_send_message")}, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true, Body: "Message sent."}, nil
		}),
		hookName: "mcp__codex_apps__user_messaging_send_message",
		success:  true,
	}
	if err := registry.Register(executor); err != nil {
		t.Fatalf("register: %v", err)
	}

	output, err := NewRouter(registry).Dispatch(context.Background(), &Invocation{
		CallID:   "message-call",
		ToolName: NamespacedName("mcp__codex_apps", "user_messaging_send_message"),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"text":"Continue?"}`},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if got, want := output.DeliveredAssistantMessage, "Continue?"; got != want {
		t.Fatalf("delivered assistant message = %q, want %q", got, want)
	}
}

func TestUserMessagingDeliveryRequiresSuccessfulRecognizedSendLikeRust(t *testing.T) {
	tests := []struct {
		name     string
		hookName string
		input    any
		success  bool
		want     string
	}{
		{name: "confirmed send", hookName: "mcp__codex_apps__user_messaging__send_message", input: map[string]any{"text": "Staging only?"}, success: true, want: "Staging only?"},
		{name: "legacy flat name", hookName: "mcp__codex_apps__user_messaging_send_message", input: map[string]any{"text": "Continue?"}, success: true, want: "Continue?"},
		{name: "failed send", hookName: "mcp__codex_apps__user_messaging__send_message", input: map[string]any{"text": "Staging only?"}, success: false},
		{name: "other app", hookName: "mcp__codex_apps__slack__send_message", input: map[string]any{"text": "Staging only?"}, success: true},
		{name: "empty text", hookName: "mcp__codex_apps__user_messaging__send_message", input: map[string]any{"text": " "}, success: true},
		{name: "missing text", hookName: "mcp__codex_apps__user_messaging__send_message", input: map[string]any{}, success: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := &Output{Success: test.success}
			captureDeliveredAssistantMessage(output, &PostToolUsePayload{
				ToolName: &HookToolName{Name: test.hookName}, ToolInput: test.input,
			})
			if output.DeliveredAssistantMessage != test.want {
				t.Fatalf("delivered assistant message = %q, want %q", output.DeliveredAssistantMessage, test.want)
			}
		})
	}
}

func TestUserMessagingDeliveryUsesGuardianMessageBudgetLikeRust(t *testing.T) {
	text := strings.Repeat("question ", 800)
	output := &Output{Success: true}
	captureDeliveredAssistantMessage(output, &PostToolUsePayload{
		ToolName:  &HookToolName{Name: "mcp__codex_apps__user_messaging__send_message"},
		ToolInput: map[string]any{"text": text},
	})
	want, _ := codexctx.GuardianTruncateText(text, guardianDeliveredAssistantMessageTokens)
	if output.DeliveredAssistantMessage != want {
		t.Fatalf("delivered assistant message was not bounded by the guardian budget")
	}
	if output.DeliveredAssistantMessage == text {
		t.Fatal("oversized delivered assistant message was not truncated")
	}
}
