package features

import "testing"

// TestCodeModeMcpResultDedupRegisteredLikeRust pins Rust #63540: the
// deduplication of MCP text fallbacks in Code Mode output is an
// under-development flag that stays off by default.
func TestCodeModeMcpResultDedupRegisteredLikeRust(t *testing.T) {
	const key = "code_mode_mcp_result_dedup"
	if !Known(key) {
		t.Fatalf("%s should be known", key)
	}
	if got := StageFor(key); got != StageUnderDevelopment {
		t.Fatalf("StageFor(%s) = %q, want %q", key, got, StageUnderDevelopment)
	}
	if defaults := Defaults(); defaults[key] {
		t.Fatalf("%s default = true, want false", key)
	}
	if !Known("code_mode_tool_search") {
		t.Fatal("the new flag replaced code_mode_tool_search instead of joining it")
	}
}
