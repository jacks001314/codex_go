package mcp

import (
	"testing"

	"codex_go/apps"
)

func intPtr(value int) *int {
	return &value
}

// TestToolConfigOutputTokenLimitParseLikeRust verifies the per-tool
// output_token_limit parses from the runtime config map (Rust #41421).
func TestToolConfigOutputTokenLimitParseLikeRust(t *testing.T) {
	tests := []struct {
		name    string
		toolRaw any
		want    *int
	}{
		{name: "snake_case", toolRaw: map[string]any{"output_token_limit": 5000}, want: intPtr(5000)},
		{name: "camelCase", toolRaw: map[string]any{"outputTokenLimit": 5000}, want: intPtr(5000)},
		{name: "absent", toolRaw: map[string]any{"approval_mode": "prompt"}, want: nil},
		{name: "non-positive ignored", toolRaw: map[string]any{"output_token_limit": 0}, want: nil},
		{name: "float accepted", toolRaw: map[string]any{"output_token_limit": float64(5000)}, want: intPtr(5000)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runtimeConfigToolsMap(map[string]any{"tools": map[string]any{
				"my_tool": tc.toolRaw,
			}}, "tools")
			cfg, ok := got["my_tool"]
			if !ok {
				t.Fatalf("tool config missing")
			}
			if tc.want == nil {
				if cfg.OutputTokenLimit != nil {
					t.Fatalf("OutputTokenLimit = %#v, want nil", cfg.OutputTokenLimit)
				}
				return
			}
			if cfg.OutputTokenLimit == nil || *cfg.OutputTokenLimit != *tc.want {
				t.Fatalf("OutputTokenLimit = %#v, want %d", cfg.OutputTokenLimit, *tc.want)
			}
		})
	}
}

// TestToolConfigRestrictOutputTokenLimitLikeRust verifies the most-restrictive
// (smallest) limit wins while keeping the approval mode independent (Rust
// McpServerToolConfig::restrict_output_token_limit).
func TestToolConfigRestrictOutputTokenLimitLikeRust(t *testing.T) {
	cfg := ToolConfig{ApprovalMode: ptrAppToolApproval(apps.AppToolApprovalPrompt), OutputTokenLimit: intPtr(8000)}
	cfg.RestrictOutputTokenLimit(intPtr(5000))
	if cfg.OutputTokenLimit == nil || *cfg.OutputTokenLimit != 5000 {
		t.Fatalf("restrict to smaller = %#v, want 5000", cfg.OutputTokenLimit)
	}
	if cfg.ApprovalMode == nil || *cfg.ApprovalMode != apps.AppToolApprovalPrompt {
		t.Fatalf("approval mode changed by output limit restriction: %#v", cfg.ApprovalMode)
	}

	// A larger requested limit does not loosen an existing stricter limit.
	cfg.RestrictOutputTokenLimit(intPtr(10000))
	if cfg.OutputTokenLimit == nil || *cfg.OutputTokenLimit != 5000 {
		t.Fatalf("larger restrict loosened limit = %#v, want 5000", cfg.OutputTokenLimit)
	}

	// A nil/non-positive requested limit is ignored.
	cfg.RestrictOutputTokenLimit(nil)
	cfg.RestrictOutputTokenLimit(intPtr(0))
	if cfg.OutputTokenLimit == nil || *cfg.OutputTokenLimit != 5000 {
		t.Fatalf("nil/zero restrict changed limit = %#v, want 5000", cfg.OutputTokenLimit)
	}
}

// TestTruncateMCPResponseToOutputTokenLimitLikeRust verifies a tool response over
// the configured output budget is truncated before reaching the model (Rust #41421).
func TestTruncateMCPResponseToOutputTokenLimitLikeRust(t *testing.T) {
	response := &MCPToolCallResponse{
		Content: []MCPToolCallContent{
			{Type: "text", Text: "long text " + repeatForLength(5000)},
			{Type: "text", Text: "short"},
		},
	}
	truncateMCPResponseToOutputTokenLimit(response, 10)
	// The first (over-budget) item is now truncated to the budget.
	if len(response.Content) != 2 {
		t.Fatalf("content length = %d, want 2", len(response.Content))
	}
	if len(response.Content[0].Text) >= 5000 {
		t.Fatalf("first text not truncated to budget (len=%d)", len(response.Content[0].Text))
	}

	// A response within budget is not truncated.
	small := &MCPToolCallResponse{Content: []MCPToolCallContent{{Type: "text", Text: "hi"}}}
	truncateMCPResponseToOutputTokenLimit(small, 10)
	if small.Content[0].Text != "hi" {
		t.Fatalf("within-budget response was truncated: %q", small.Content[0].Text)
	}

	// A multi-item response that fits the budget is not truncated at all.
	multi := &MCPToolCallResponse{Content: []MCPToolCallContent{
		{Type: "text", Text: "hello"},
		{Type: "text", Text: "world"},
	}}
	truncateMCPResponseToOutputTokenLimit(multi, 100)
	if multi.Content[0].Text != "hello" || multi.Content[1].Text != "world" {
		t.Fatalf("within-budget multi response was truncated: %#v", multi.Content)
	}
}

func repeatForLength(n int) string {
	buf := make([]byte, 0, n)
	for len(buf) < n {
		buf = append(buf, 'a')
	}
	return string(buf)
}
