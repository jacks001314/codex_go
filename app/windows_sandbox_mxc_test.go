package app

import (
	"testing"

	chatwidget "codex_go/tui/chatwidget"
)

// TestInteractiveWindowsSandboxLevelSelectsMxcLikeRust mirrors Rust
// tui/src/windows_sandbox.rs: a configured `windows.sandbox = "mxc"` selects the
// native backend, which the TUI treats as enabled instead of falling through to
// the legacy feature flags.
func TestInteractiveWindowsSandboxLevelSelectsMxcLikeRust(t *testing.T) {
	mxc := map[string]any{"windows": map[string]any{"sandbox": "mxc"}}
	if got := interactiveWindowsSandboxLevel(mxc); got != chatwidget.WindowsSandboxLevelMxc {
		t.Fatalf("interactiveWindowsSandboxLevel(mxc) = %q, want mxc", got)
	}
	elevated := map[string]any{"windows": map[string]any{"sandbox": "elevated"}}
	if got := interactiveWindowsSandboxLevel(elevated); got != chatwidget.WindowsSandboxLevelElevated {
		t.Fatalf("interactiveWindowsSandboxLevel(elevated) = %q, want elevated", got)
	}
	if got := interactiveWindowsSandboxLevel(map[string]any{}); got != chatwidget.WindowsSandboxLevelDisabled {
		t.Fatalf("interactiveWindowsSandboxLevel(unset) = %q, want disabled", got)
	}
}
