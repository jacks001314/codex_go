package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type nativeReviewExecutor struct {
	spec     Spec
	required bool
}

func (e nativeReviewExecutor) Spec() Spec { return e.spec }

func (e nativeReviewExecutor) Execute(context.Context, *Invocation) (*Output, error) {
	return &Output{}, nil
}

func (e nativeReviewExecutor) NativeReviewRequired() bool { return e.required }

// Mirrors Rust #58040: the registry captures the native-review policy with the
// tool plan and dispatch retains the original call only for opted-in tools.
func TestRouterRetainsNativeCallForReviewToolsLikeRust(t *testing.T) {
	registry := NewRegistry()
	reviewName := NamespacedName("collaboration", "send_message")
	if err := registry.Register(nativeReviewExecutor{spec: Spec{Name: reviewName}, required: true}); err != nil {
		t.Fatalf("register review tool: %v", err)
	}
	plainName := PlainName("shell")
	if err := registry.Register(nativeReviewExecutor{spec: Spec{Name: plainName}}); err != nil {
		t.Fatalf("register plain tool: %v", err)
	}
	if !registry.NativeReviewRequired(reviewName) {
		t.Fatal("review tool policy was not captured")
	}
	if registry.NativeReviewRequired(plainName) {
		t.Fatal("plain tool unexpectedly requires native review")
	}
	router := NewRouter(registry)
	raw := json.RawMessage(`{"type":"function_call","call_id":"call-1","namespace":"collaboration","name":"send_message","arguments":"{}","encrypted_function_args":["message"]}`)

	invocation, ok, err := router.BuildToolCall(ResponseItem{
		Type: "function_call", Namespace: "collaboration", Name: "send_message",
		CallID: "call-1", Arguments: "{}", Raw: raw,
	})
	if err != nil || !ok {
		t.Fatalf("BuildToolCall(review) ok=%v err=%v", ok, err)
	}
	if string(invocation.NativeCall) != string(raw) {
		t.Fatalf("retained native call = %s, want the original item", invocation.NativeCall)
	}
	if !router.RequiresGuardianReview(invocation) {
		t.Fatal("registry policy did not opt the invocation into review")
	}

	invocation, ok, err = router.BuildToolCall(ResponseItem{
		Type: "function_call", Name: "shell", CallID: "call-2", Arguments: `{"cmd":"ls"}`, Raw: raw,
	})
	if err != nil || !ok {
		t.Fatalf("BuildToolCall(plain) ok=%v err=%v", ok, err)
	}
	if len(invocation.NativeCall) != 0 {
		t.Fatalf("plain tool retained a native call: %s", invocation.NativeCall)
	}

	// Without a raw item, dispatch synthesizes the call from its own view.
	invocation, ok, err = router.BuildToolCall(ResponseItem{
		Type: "function_call", Namespace: "collaboration", Name: "send_message",
		CallID: "call-3", Arguments: `{"message":"hi"}`,
	})
	if err != nil || !ok {
		t.Fatalf("BuildToolCall(synthesized) ok=%v err=%v", ok, err)
	}
	var synthesized map[string]any
	if err := json.Unmarshal(invocation.NativeCall, &synthesized); err != nil {
		t.Fatalf("decode synthesized call: %v", err)
	}
	if synthesized["call_id"] != "call-3" || synthesized["name"] != "send_message" || synthesized["namespace"] != "collaboration" {
		t.Fatalf("synthesized call = %s", invocation.NativeCall)
	}
}
