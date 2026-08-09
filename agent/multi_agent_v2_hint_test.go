package agent

import (
	"strings"
	"testing"
)

func TestMultiAgentV2UsageHintMatchesRootAndSubagentContract(t *testing.T) {
	root := MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		MaxConcurrency:                 4,
		WaitAgentEnabled:               true,
		ExposeSpawnAgentModelOverrides: true,
	})
	for _, fragment := range []string{
		"You are `/root`",
		"Message Type: MESSAGE | FINAL_ANSWER",
		"functions.collaboration.spawn_agent",
		"When calling `wait_agent`, prefer longer waits (minutes) to avoid busy polling.",
		"There are 4 available concurrency slots, meaning that up to 4 agents can be active at once, including you.",
		"Only set `model` or `reasoning_effort` when explicitly requested",
	} {
		if !strings.Contains(root, fragment) {
			t.Fatalf("root usage hint missing %q:\n%s", fragment, root)
		}
	}

	subagent := MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		IsSubagent:                     true,
		MaxConcurrency:                 2,
		WaitAgentEnabled:               false,
		ExposeSpawnAgentModelOverrides: false,
	})
	for _, fragment := range []string{
		"You are an agent in a team of agents collaborating to complete a task.",
		"Message Type: NEW_TASK | MESSAGE | FINAL_ANSWER",
		"There are 2 available concurrency slots, meaning that up to 2 agents can be active at once, including you.",
	} {
		if !strings.Contains(subagent, fragment) {
			t.Fatalf("subagent usage hint missing %q:\n%s", fragment, subagent)
		}
	}
	if strings.Contains(subagent, "When calling `wait_agent`") {
		t.Fatalf("wait_agent guidance present while disabled (Rust 92b83e226d):\n%s", subagent)
	}
	if strings.Contains(subagent, "Only set `model` or `reasoning_effort`") {
		t.Fatalf("model override guidance present while hidden:\n%s", subagent)
	}
}

func TestMultiAgentV2UsageHintConfiguredOverrideReplacesIdentity(t *testing.T) {
	custom := "Delegate carefully."
	hint := MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		MaxConcurrency:    4,
		RootUsageHintText: &custom,
	})
	if !strings.Contains(hint, custom) {
		t.Fatalf("configured root hint missing: %q", hint)
	}
	if strings.Contains(hint, "You are `/root`") {
		t.Fatalf("configured root hint should replace the built-in identity:\n%s", hint)
	}
}
