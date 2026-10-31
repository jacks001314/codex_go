package state

import (
	"strings"
	"testing"
)

// Mirrors Rust #44569: the fixed synchronous action byte limit is gone, so an
// action that only becomes large in aggregate is reviewed whole instead of
// being rejected or partially rendered.
func TestGuardianActionReviewsCompleteAggregatePayloadsLikeRust(t *testing.T) {
	files := make([]string, 20_000)
	for index := range files {
		files[index] = "/tmp/file"
	}
	text, truncated, err := FormatGuardianActionPretty(Action{Type: "apply_patch", CWD: "/tmp", Files: files})
	if err != nil {
		t.Fatalf("FormatGuardianActionPretty() error = %v, want the complete action", err)
	}
	if truncated {
		t.Fatal("FormatGuardianActionPretty() reported truncation for a complete action")
	}
	if len(text) < 200_000 {
		t.Fatalf("rendered action = %d bytes, want the complete oversized payload", len(text))
	}
	if count := strings.Count(text, "/tmp/file"); count < len(files) {
		t.Fatalf("rendered action kept %d of %d files", count, len(files))
	}
}

// Mirrors Rust #44569: action strings are no longer shortened and the returned
// text stays lossless even above the former per-string token budget.
func TestGuardianActionPreservesOversizedStringsWholeLikeRust(t *testing.T) {
	message := strings.Repeat("review", 20_000)
	text, truncated, err := FormatGuardianActionPretty(Action{
		Type:      "mcp_tool_call",
		Server:    "server",
		ToolName:  "tool",
		Arguments: map[string]any{"message": message},
	})
	if err != nil {
		t.Fatalf("FormatGuardianActionPretty() error = %v", err)
	}
	if truncated {
		t.Fatal("FormatGuardianActionPretty() truncated a complete action")
	}
	if !strings.Contains(text, message) {
		t.Fatal("rendered action does not carry the complete oversized string")
	}
	if strings.Contains(text, "<truncated omitted_approx_tokens=") {
		t.Fatal("rendered action still carries the action truncation marker")
	}
}
