package appserver

import (
	"reflect"
	"testing"

	"codex_go/turn"
)

// TestStandaloneToolOutputRetentionAnnotationLikeRust mirrors Rust #61344
// `pending_turn_input`: a retained, named client tool output carries an
// explicit `retain_across_windows` decision, while an ordinary output stays
// unmarked.
func TestStandaloneToolOutputRetentionAnnotationLikeRust(t *testing.T) {
	retained := standaloneToolOutputInputItem(&turn.TurnToolOutput{
		Name:      "inspect",
		Namespace: "example",
		Output:    turn.ToolOutputText("Inspect the failing tests."),
		Retain:    true,
	}, true)
	want := map[string]any{
		"type":             "function_call_output",
		"name":             "inspect",
		"namespace":        "example",
		"output":           "Inspect the failing tests.",
		"harness_metadata": map[string]any{"retain_across_windows": true},
	}
	if !reflect.DeepEqual(retained, want) {
		t.Fatalf("retained item = %#v, want %#v", retained, want)
	}

	ordinary := standaloneToolOutputInputItem(&turn.TurnToolOutput{Name: "inspect", Output: turn.ToolOutputText("done")}, true)
	object, _ := ordinary.(map[string]any)
	if _, marked := object["harness_metadata"]; marked {
		t.Fatalf("ordinary output carries retention metadata: %#v", ordinary)
	}
}
