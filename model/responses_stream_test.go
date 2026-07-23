package model

import "testing"

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
