package appserver

import (
	"testing"

	"codex_go/turn"
)

// Rust #41002 emits a structured FunctionCallOutputBody unchanged so inline
// images survive into the model request, while a text body stays a plain string.
func TestStandaloneToolOutputInputItemStructuredLikeRust(t *testing.T) {
	structured := standaloneToolOutputInputItem(&turn.TurnToolOutput{
		Name: "screenshot",
		Output: turn.ToolOutputContentItems(
			turn.FunctionCallOutputContentItem{Type: "input_text", Text: "Captured."},
			turn.FunctionCallOutputContentItem{Type: "input_image", ImageURL: "data:image/png;base64,AAAA"},
		),
	})
	item, ok := structured.(map[string]any)
	if !ok {
		t.Fatalf("standalone tool output = %#v, want a map", structured)
	}
	body, ok := item["output"].([]turn.FunctionCallOutputContentItem)
	if !ok || len(body) != 2 {
		t.Fatalf("structured output body = %#v, want two content items", item["output"])
	}
	if body[1].ImageURL != "data:image/png;base64,AAAA" {
		t.Fatalf("structured output image = %#v", body[1])
	}

	text := standaloneToolOutputInputItem(&turn.TurnToolOutput{Name: "inspect", Output: turn.ToolOutputText("done")})
	if got := text.(map[string]any)["output"]; got != "done" {
		t.Fatalf("text output body = %#v, want the plain string", got)
	}
}
