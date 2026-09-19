package chatwidget

import "testing"

// TestWindowsSandboxMxcLevelIsEnabledLikeRust mirrors Rust #46271's TUI half:
// selecting the native MXC backend counts as having the sandbox enabled, so it
// never opens the legacy enable/setup prompt and never requires elevated setup.
func TestWindowsSandboxMxcLevelIsEnabledLikeRust(t *testing.T) {
	if ElevatedWindowsSandboxSetupRequired(WindowsSandboxLevelMxc, true, false) {
		t.Fatal("mxc must not require elevated legacy setup")
	}
	decision := MaybePromptWindowsSandboxEnable(true, WindowsSandboxLevelMxc, false, true)
	if decision.SetupRequired || decision.OpenEnablePrompt {
		t.Fatalf("mxc prompt decision = %#v, want no setup prompt", decision)
	}
	// The legacy levels keep their existing prompts.
	if !MaybePromptWindowsSandboxEnable(true, WindowsSandboxLevelDisabled, false, true).SetupRequired {
		t.Fatal("a disabled sandbox still requires setup")
	}
}
