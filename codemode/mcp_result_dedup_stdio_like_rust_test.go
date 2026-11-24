package codemode

import (
	"context"
	"strings"
	"testing"

	"codex_go/tool"
)

// TestStdioHostSessionDedupesMCPFallbackLikeRust covers Rust #63540 on the host
// transport: a session opened with mcpResultDedup prints the original tool
// result without the duplicate content fallback while a script copy keeps it.
func TestStdioHostSessionDedupesMCPFallbackLikeRust(t *testing.T) {
	for _, test := range []struct {
		name        string
		dedup       bool
		wantContent int
	}{
		{name: "dedup off", dedup: false, wantContent: 2},
		{name: "dedup on", dedup: true, wantContent: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := newStdioHostSession(&stdioHostServer{}, "session-1", test.dedup)
			fallback := `{"a":1}`
			if err := session.registry.Register(tool.NewExecutorFunc(tool.Spec{
				Name:        tool.PlainName("lookup"),
				Description: "Look up a value",
			}, func(_ context.Context, invocation *tool.Invocation) (*tool.Output, error) {
				return &tool.Output{
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
			output, err := session.exec.Execute(context.Background(), &tool.Invocation{
				CallID: "cell-1",
				Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `
const original = await tools.lookup({});
text(original);
const copy = { ...original };
text(copy);
`},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if output == nil || !output.Success {
				t.Fatalf("output = %#v", output)
			}
			if got := strings.Count(output.Body, `"content"`); got != test.wantContent {
				t.Fatalf("printed content fields = %d, want %d (body %q)", got, test.wantContent, output.Body)
			}
			if got := strings.Count(output.Body, `"structuredContent"`); got != 2 {
				t.Fatalf("printed structuredContent fields = %d, want 2 (body %q)", got, output.Body)
			}
		})
	}
}
