package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestCodeModeTextOmitsDuplicateMCPFallbackInProcessLikeRust covers Rust #63540
// in the in-process engine: text() drops content for the value the tool
// returned, while a script copy keeps both fields and the JavaScript value
// still exposes content.
func TestCodeModeTextOmitsDuplicateMCPFallbackInProcessLikeRust(t *testing.T) {
	registry := NewRegistry()
	fallback := `{"a":1}`
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("lookup"), Description: "Look up a value"}, func(_ context.Context, invocation *Invocation) (*Output, error) {
		return &Output{
			CallID:   invocation.CallID,
			ToolName: invocation.ToolName,
			Success:  true,
			Body:     fallback,
			Data: map[string]any{
				"content":           []any{map[string]any{"type": "text", "text": fallback}},
				"structuredContent": map[string]any{"a": 1},
			},
		}, nil
	})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	for _, test := range []struct {
		name  string
		dedup bool
	}{
		{name: "dedup off", dedup: false},
		{name: "dedup on", dedup: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := NewCodeModeRuntimeWithOptions(nil, false, CodeModeSessionOptions{MCPResultDedup: test.dedup})
			exec, _ := runtime.Executors(registry)
			output, err := exec.Execute(context.Background(), &Invocation{
				CallID: "dedup-cell",
				Payload: Payload{Kind: PayloadCustom, Input: `
const original = await tools.lookup({});
text(original);
text(original.content.length);
const copy = { ...original };
text(copy);
`},
				Context: map[string]any{},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if output == nil || !output.Success {
				t.Fatalf("output = %#v", output)
			}
			lines := strings.Split(codeModeBodyAfterHeader(t, output.Body, "Script completed"), "\n")
			if len(lines) != 3 {
				t.Fatalf("text lines = %#v (body %q)", lines, output.Body)
			}
			var printed map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &printed); err != nil {
				t.Fatalf("printed original is not JSON: %v (%q)", err, lines[0])
			}
			_, originalHasContent := printed["content"]
			if originalHasContent == test.dedup {
				t.Fatalf("original content present = %v, want %v (%q)", originalHasContent, !test.dedup, lines[0])
			}
			if strings.TrimSpace(lines[1]) != "1" {
				t.Fatalf("JavaScript content length = %q, want 1", lines[1])
			}
			var copied map[string]any
			if err := json.Unmarshal([]byte(lines[2]), &copied); err != nil {
				t.Fatalf("printed copy is not JSON: %v (%q)", err, lines[2])
			}
			if _, copyHasContent := copied["content"]; !copyHasContent {
				t.Fatalf("copy lost content: %q", lines[2])
			}
			if copied["structuredContent"] == nil {
				t.Fatalf("copy lost structuredContent: %q", lines[2])
			}
		})
	}
}
