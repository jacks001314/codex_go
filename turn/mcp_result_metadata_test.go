package turn

import (
	"testing"
)

// Mirrors Rust #46010's `result_metadata_capture_allowed`: the raw MCP result
// metadata is exposed only when the session's analytics client is enabled and
// the call belongs to the host-owned apps server. Recording the exposed
// snapshot stays a second check at the dispatch sites (Rust's recorder state).
func TestMCPResultMetadataCaptureAllowedLikeRust(t *testing.T) {
	enabled := true
	disabled := false
	cases := []struct {
		name       string
		analytics  *bool
		serverName string
		want       bool
	}{
		{name: "apps with analytics", analytics: &enabled, serverName: "codex_apps", want: true},
		{name: "analytics disabled", analytics: &disabled, serverName: "codex_apps"},
		{name: "analytics unknown", analytics: nil, serverName: "codex_apps"},
		{name: "other server", analytics: &enabled, serverName: "memory"},
		{name: "empty server", analytics: &enabled, serverName: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := &ToolRegistryOptions{AnalyticsEnabled: tc.analytics}
			if got := mcpResultMetadataCaptureAllowed(options, tc.serverName); got != tc.want {
				t.Fatalf("capture allowed = %v, want %v", got, tc.want)
			}
		})
	}
	if mcpResultMetadataCaptureAllowed(nil, "codex_apps") {
		t.Fatal("nil options must not allow capture")
	}
}
