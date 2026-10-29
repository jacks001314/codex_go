package turn

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Rust #41002 accepts a FunctionCallOutputBody for toolOutput.output: plain text
// or structured content items, with the text-size limit and remote-image rules
// applied to both forms.
func TestTurnToolOutputBodyDecodesBothWireFormsLikeRust(t *testing.T) {
	var text TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","toolOutput":{"name":"notifications","output":"Alice mentioned you."}}`), &text); err != nil {
		t.Fatalf("unmarshal text output: %v", err)
	}
	if text.ToolOutput == nil || text.ToolOutput.Output.Items != nil || text.ToolOutput.Output.Text != "Alice mentioned you." {
		t.Fatalf("text tool output = %#v", text.ToolOutput)
	}
	if err := text.Validate(); err != nil {
		t.Fatalf("text tool output error = %v", err)
	}
	encoded, err := json.Marshal(text.ToolOutput)
	if err != nil {
		t.Fatalf("marshal text tool output: %v", err)
	}
	if string(encoded) != `{"name":"notifications","output":"Alice mentioned you."}` {
		t.Fatalf("text tool output wire form = %s", encoded)
	}

	var structured TurnStartParams
	payload := `{"threadId":"thread-1","toolOutput":{"name":"screenshot","output":[{"type":"input_text","text":"Captured."},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}}`
	if err := json.Unmarshal([]byte(payload), &structured); err != nil {
		t.Fatalf("unmarshal structured output: %v", err)
	}
	if structured.ToolOutput == nil || len(structured.ToolOutput.Output.Items) != 2 {
		t.Fatalf("structured tool output = %#v", structured.ToolOutput)
	}
	if err := structured.Validate(); err != nil {
		t.Fatalf("structured tool output error = %v", err)
	}
	if got := structured.ToolOutput.Output.TextCharCount(); got != len("Captured.") {
		t.Fatalf("TextCharCount() = %d, want %d", got, len("Captured."))
	}
	encoded, err = json.Marshal(structured.ToolOutput)
	if err != nil {
		t.Fatalf("marshal structured tool output: %v", err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if items, ok := roundTrip["output"].([]any); !ok || len(items) != 2 {
		t.Fatalf("structured wire form = %s", encoded)
	}
}

func TestTurnToolOutputStructuredValidationLikeRust(t *testing.T) {
	remote := TurnStartParams{ThreadID: "thread-1", ToolOutput: &TurnToolOutput{
		Name: "screenshot",
		Output: ToolOutputContentItems(FunctionCallOutputContentItem{
			Type:     "input_image",
			ImageURL: "https://example.test/shot.png",
		}),
	}}
	err := remote.Validate()
	if !errors.Is(err, ErrInvalidTurnRequest) || !strings.Contains(err.Error(), "remote image URLs are not supported") {
		t.Fatalf("remote image tool output error = %v, want a remote image rejection", err)
	}

	fileReference := TurnStartParams{ThreadID: "thread-1", ToolOutput: &TurnToolOutput{
		Name: "screenshot",
		Output: ToolOutputContentItems(FunctionCallOutputContentItem{
			Type:   "input_image",
			FileID: "file-123",
		}),
	}}
	if err := fileReference.Validate(); err != nil {
		t.Fatalf("file reference tool output error = %v", err)
	}

	tooLarge := TurnStartParams{ThreadID: "thread-1", ToolOutput: &TurnToolOutput{
		Name: "logs",
		Output: ToolOutputContentItems(FunctionCallOutputContentItem{
			Type: "input_text",
			Text: strings.Repeat("x", MaxUserInputTextChars+1),
		}),
	}}
	var inputTooLarge *InputTooLargeError
	if err := tooLarge.Validate(); !errors.As(err, &inputTooLarge) {
		t.Fatalf("oversized structured tool output error = %v, want InputTooLargeError", err)
	}

	mixed := TurnStartParams{ThreadID: "thread-1", ToolOutput: &TurnToolOutput{
		Name:   "logs",
		Output: TurnToolOutputBody{Text: "plain", Items: []FunctionCallOutputContentItem{{Type: "input_text", Text: "item"}}},
	}}
	if err := mixed.Validate(); !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("mixed tool output error = %v, want ErrInvalidTurnRequest", err)
	}
}
