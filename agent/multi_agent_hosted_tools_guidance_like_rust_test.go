package agent

import (
	"strings"
	"testing"
)

// Mirrors Rust #60011: when native tools are disabled the multi-agent guidance
// does not vanish. It names the hosted tools instead, because the collaboration
// calls the bundled identity describes cannot be made natively here.
func TestMultiAgentGuidanceNamesHostedToolsLikeRust(t *testing.T) {
	disabled := MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		MaxConcurrency:   3,
		WaitAgentEnabled: true,
		OmitBundledRole:  true,
	})
	if strings.TrimSpace(disabled) == "" {
		t.Fatal("guidance is empty with native tools disabled")
	}
	if !strings.Contains(strings.ToLower(disabled), "hosted") {
		t.Fatalf("guidance does not name hosted tools:\n%s", disabled)
	}
	if strings.Contains(disabled, "You are `/root`") || strings.Contains(disabled, "You are an agent in a team") {
		t.Fatalf("bundled native-tool identity survived:\n%s", disabled)
	}
	for _, want := range []string{"spawn_agent", "send_message", "wait_agent"} {
		if !strings.Contains(disabled, want) {
			t.Fatalf("hosted guidance lost %q:\n%s", want, disabled)
		}
	}

	// Native tools enabled: the bundled identity stays and nothing mentions
	// hosted tools.
	enabled := MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{MaxConcurrency: 3})
	if !strings.Contains(enabled, "You are `/root`") {
		t.Fatalf("bundled identity missing when native tools are enabled:\n%s", enabled)
	}
	if strings.Contains(strings.ToLower(enabled), "hosted") {
		t.Fatalf("hosted guidance leaked into the native path:\n%s", enabled)
	}

	// Configured role text still wins over both bundled variants.
	configured := "Custom root role."
	hint := MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		RootUsageHintText: &configured,
		OmitBundledRole:   true,
		MaxConcurrency:    1,
	})
	if !strings.HasPrefix(hint, configured) {
		t.Fatalf("configured role text = %q, want it to lead the hint", hint)
	}
	if strings.Contains(hint, MultiAgentV2HostedToolsUsageHint) {
		t.Fatalf("hosted guidance replaced configured role text:\n%s", hint)
	}
}
