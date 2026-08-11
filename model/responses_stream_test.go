package model

import (
	"context"
	"strings"
	"testing"
)

func TestParseResponsesStreamRecoversDeclaredCustomToolFromFunctionCallEnvelope(t *testing.T) {
	javascript := `const result = await tools.shell_command({command: "Write-Output OK"}); text(result.output);`
	response, err := parseResponsesStream(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_item.added","item":{"id":"ctc_1","type":"function_call","call_id":"call-1","name":"exec","arguments":""}}`,
			`{"type":"response.custom_tool_call_input.delta","item_id":"ctc_1","call_id":"call-1","delta":"const result = await tools.shell_command({command: \"Write-Output OK\"}); "}`,
			`{"type":"response.custom_tool_call_input.delta","item_id":"ctc_1","call_id":"call-1","delta":"text(result.output);"}`,
			`{"type":"response.output_item.done","item":{"id":"ctc_1","type":"function_call","call_id":"call-1","name":"exec","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)),
		&AgentRequest{
			Prompt: "run command",
			Model:  "gpt-test",
			Tools:  []any{map[string]any{"type": "custom", "name": "exec"}},
		},
		"openai",
		nil,
	)
	if err != nil {
		t.Fatalf("parseResponsesStream() error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.Type != "custom_tool_call" || item.Name != "exec" || item.CallID != "call-1" || item.Input != javascript || item.Arguments != "" {
		t.Fatalf("custom tool item = %#v", item)
	}
}

func TestUsageFromStreamEventDataCapturesRolloutBudgetUnits(t *testing.T) {
	usage, ok := usageFromStreamEventData([]byte(`{"response":{"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6,"codex_rollout_budget_units":2.5}}}`))
	if !ok {
		t.Fatal("usageFromStreamEventData() ok = false, want true")
	}
	if usage.InputTokens != 4 || usage.OutputTokens != 2 || usage.TotalTokens != 6 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.CodexRolloutBudgetUnits != "2.5" {
		t.Fatalf("codex rollout budget units = %q, want 2.5", usage.CodexRolloutBudgetUnits)
	}
}

func TestUsageFromStreamEventDataMissingRolloutBudgetUnits(t *testing.T) {
	usage, ok := usageFromStreamEventData([]byte(`{"response":{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
	if !ok {
		t.Fatal("usageFromStreamEventData() ok = false, want true")
	}
	if usage.CodexRolloutBudgetUnits != "" {
		t.Fatalf("codex rollout budget units = %q, want empty", usage.CodexRolloutBudgetUnits)
	}
}

func TestParseResponsesStreamKeepsDeclaredFunctionToolAsFunctionCall(t *testing.T) {
	response, err := parseResponsesStream(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc-1","call_id":"call-1","delta":"{\"cmd\":\"pwd\"}"}`,
			`{"type":"response.output_item.done","item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"exec_command","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)),
		&AgentRequest{
			Prompt: "run command",
			Model:  "gpt-test",
			Tools:  []any{map[string]any{"type": "function", "name": "exec_command"}},
		},
		"openai",
		nil,
	)
	if err != nil {
		t.Fatalf("parseResponsesStream() error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.Type != "function_call" || item.Name != "exec_command" || item.Arguments != `{"cmd":"pwd"}` || item.Input != "" {
		t.Fatalf("function tool item = %#v", item)
	}
}

func TestParseResponsesStreamPreservesPlaintextCollaborationMarker(t *testing.T) {
	response, err := parseResponsesStream(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_item.added","item":{"id":"fc-1","type":"function_call","namespace":"collaboration","name":"spawn_agent","call_id":"call-1","arguments":"","encrypted_function_args":[]}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc-1","call_id":"call-1","delta":"{\"task_name\":\"worker\",\"message\":\"hello\"}"}`,
			`{"type":"response.output_item.done","item":{"id":"fc-1","type":"function_call","namespace":"collaboration","name":"spawn_agent","call_id":"call-1","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)),
		&AgentRequest{Model: "gpt-test"},
		"openai",
		nil,
	)
	if err != nil {
		t.Fatalf("parseResponsesStream() error = %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].EncryptedFunctionArgs == nil || len(*response.Items[0].EncryptedFunctionArgs) != 0 {
		t.Fatalf("function call marker = %#v", response.Items)
	}
}

func TestResponsesStreamAccumulatorAppliesCustomToolInputDeltasOverInitialInput(t *testing.T) {
	acc := &responsesStreamAccumulator{
		customToolInputDeltas: map[string]string{"call-1": "*** Begin Patch\n*** Add File: calculator.py\n+def add(a, b):\n+    return a + b\n*** End Patch"},
	}
	item := &AgentItem{Type: "custom_tool_call", ID: "item-1", CallID: "call-1", Input: ""}
	acc.applyToolInputDeltas(item)
	if item.Input == "" || item.Input != acc.customToolInputDeltas["call-1"] {
		t.Fatalf("custom tool input = %q, want accumulated delta", item.Input)
	}
}

func TestResponsesStreamAccumulatorPrefersAccumulatedFunctionArguments(t *testing.T) {
	acc := &responsesStreamAccumulator{
		functionCallArgDeltas: map[string]string{"call-1": `{"cmd":"echo ok"}`},
	}
	item := &AgentItem{Type: "function_call", ID: "item-1", CallID: "call-1", Arguments: `{"cmd":"partial"}`}
	acc.applyToolInputDeltas(item)
	if item.Arguments != `{"cmd":"echo ok"}` {
		t.Fatalf("function arguments = %q, want accumulated delta", item.Arguments)
	}
}

func TestResponsesStreamAccumulatorBridgesApplyPatchCustomDeltaToFunctionCall(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	acc := &responsesStreamAccumulator{customToolInputDeltas: map[string]string{"call-1": patch}}
	item := &AgentItem{Type: "function_call", Name: "apply_patch", CallID: "call-1"}
	acc.applyToolInputDeltas(item)
	if item.Arguments != patch {
		t.Fatalf("apply_patch arguments = %q, want custom input delta", item.Arguments)
	}
}

func TestResponsesStreamAccumulatorBridgesApplyPatchFunctionDeltaToCustomCall(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	acc := &responsesStreamAccumulator{functionCallArgDeltas: map[string]string{"call-1": patch}}
	item := &AgentItem{Type: "custom_tool_call", Name: "apply_patch", CallID: "call-1"}
	acc.applyToolInputDeltas(item)
	if item.Input != patch {
		t.Fatalf("apply_patch input = %q, want function arguments delta", item.Input)
	}
}

func TestResponsesStreamAccumulatorBridgesApplyPatchMismatchedDeltaKey(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	acc := &responsesStreamAccumulator{customToolInputDeltas: map[string]string{"stream-item-1": patch}}
	item := &AgentItem{Type: "function_call", Name: "apply_patch", CallID: "final-call-1"}
	acc.applyToolInputDeltas(item)
	if item.Arguments != patch {
		t.Fatalf("apply_patch arguments = %q, want sole custom delta", item.Arguments)
	}
}

func TestSoleAccumulatedToolInputDeltaRejectsAmbiguousInputs(t *testing.T) {
	if got := soleAccumulatedToolInputDelta(map[string]string{"a": "patch-a", "b": "patch-b"}); got != "" {
		t.Fatalf("sole delta = %q, want empty for ambiguous inputs", got)
	}
}

func TestSafetyBufferingFallsBackToTypedResponseMetadata(t *testing.T) {
	event := []byte(`{"type":"response.metadata","metadata":{"type":"safety_buffering","use_cases":["cyber"],"reasons":["user_risk"]}}`)
	buffering := safetyBufferingFromStreamMetadata(event)
	if buffering == nil || len(buffering.UseCases) != 1 || buffering.UseCases[0] != "cyber" || len(buffering.Reasons) != 1 || buffering.Reasons[0] != "user_risk" {
		t.Fatalf("safety buffering = %#v", buffering)
	}
}

func TestSafetyBufferingTopLevelPresenceWinsOverMetadata(t *testing.T) {
	event := []byte(`{"type":"response.metadata","safety_buffering":{"use_cases":["top_level"],"reasons":["top"]},"metadata":{"type":"safety_buffering","use_cases":["nested"],"reasons":["nested"]}}`)
	buffering := safetyBufferingFromStreamMetadata(event)
	if buffering == nil || len(buffering.UseCases) != 1 || buffering.UseCases[0] != "top_level" {
		t.Fatalf("top-level safety buffering should win: %#v", buffering)
	}
}

func TestSafetyBufferingTopLevelMalformedIsAuthoritative(t *testing.T) {
	// A present top-level `safety_buffering` wins even when it is null or not
	// an object (Rust 9558d830f6).
	for _, topLevel := range []string{"null", "false"} {
		event := []byte(`{"type":"response.metadata","safety_buffering":` + topLevel + `,"metadata":{"type":"safety_buffering","use_cases":["nested"],"reasons":["nested"]}}`)
		if buffering := safetyBufferingFromStreamMetadata(event); buffering != nil {
			t.Fatalf("malformed top-level %s should win over metadata: %#v", topLevel, buffering)
		}
	}
}

func TestSafetyBufferingIgnoresUnrelatedMetadata(t *testing.T) {
	event := []byte(`{"type":"response.metadata","metadata":{"type":"other_metadata","use_cases":["cyber"]}}`)
	if buffering := safetyBufferingFromStreamMetadata(event); buffering != nil {
		t.Fatalf("unrelated metadata should not produce safety buffering: %#v", buffering)
	}
}
