package appserver

import (
	"reflect"
	"testing"

	"codex_go/turn"
)

// TestStandaloneToolOutputRetentionAnnotationLikeRust mirrors Rust #52671: a
// retained client tool output is recorded as client-authored harness metadata,
// while an ordinary output stays unmarked.
func TestStandaloneToolOutputRetentionAnnotationLikeRust(t *testing.T) {
	retained := standaloneToolOutputInputItem(&turn.TurnToolOutput{
		Name:      "inspect",
		Namespace: "example",
		Output:    turn.ToolOutputText("Inspect the failing tests."),
		Retain:    true,
	})
	want := map[string]any{
		"type":             "function_call_output",
		"name":             "inspect",
		"namespace":        "example",
		"output":           "Inspect the failing tests.",
		"harness_metadata": map[string]any{"client_authored": true},
	}
	if !reflect.DeepEqual(retained, want) {
		t.Fatalf("retained item = %#v, want %#v", retained, want)
	}

	ordinary := standaloneToolOutputInputItem(&turn.TurnToolOutput{Name: "inspect", Output: turn.ToolOutputText("done")})
	object, _ := ordinary.(map[string]any)
	if _, marked := object["harness_metadata"]; marked {
		t.Fatalf("ordinary output carries retention metadata: %#v", ordinary)
	}
}
