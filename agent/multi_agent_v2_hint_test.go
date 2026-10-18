package agent

import (
	"strings"
	"testing"
)

func TestMultiAgentV2UsageHintCatalogRoleInstructionsLikeRust(t *testing.T) {
	catalogText := "catalog subagent role"
	hint := MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		IsSubagent:              true,
		MaxConcurrency:          2,
		SubagentCatalogHintText: &catalogText,
	})
	if !strings.Contains(hint, "<multi_agent_role>\n"+catalogText+"\n</multi_agent_role>") {
		t.Fatalf("catalog role should be marked with <multi_agent_role>:\n%s", hint)
	}

	// Configured text takes precedence over catalog and stays unmarked.
	configured := "configured subagent role"
	hint = MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		IsSubagent:              true,
		MaxConcurrency:          2,
		SubagentUsageHintText:   &configured,
		SubagentCatalogHintText: &catalogText,
	})
	if strings.Contains(hint, "catalog subagent role") || strings.Contains(hint, "<multi_agent_role>") {
		t.Fatalf("configured role should win over catalog and stay unmarked:\n%s", hint)
	}
	if !strings.Contains(hint, "configured subagent role") {
		t.Fatalf("configured role missing:\n%s", hint)
	}

	// An empty catalog role suppresses the default identity fallback.
	empty := ""
	hint = MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		IsSubagent:              true,
		MaxConcurrency:          2,
		SubagentCatalogHintText: &empty,
	})
	if strings.Contains(hint, MultiAgentV2SubagentUsageHint) {
		t.Fatalf("empty catalog role should suppress the default identity:\n%s", hint)
	}
	if !strings.Contains(hint, "concurrency slots") {
		t.Fatalf("shared concurrency guidance should remain:\n%s", hint)
	}
}

// TestMultiAgentV2UsageHintUpdatePlanFilterLikeRust mirrors Rust #41744:
// configured role text is caller-owned and survives the filter, while bundled
// or catalog instructions are Codex-owned.
func TestMultiAgentV2UsageHintUpdatePlanFilterLikeRust(t *testing.T) {
	configured := "## Plan tool\nConfigured root guidance."
	hint := MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		MaxConcurrency:             2,
		RootUsageHintText:          &configured,
		OmitUpdatePlanInstructions: true,
	})
	if !strings.Contains(hint, configured) {
		t.Fatalf("configured hint was filtered:\n%s", hint)
	}

	catalog := "## Plan tool\nCatalog root guidance."
	hint = MultiAgentV2UsageHint(MultiAgentV2UsageHintOptions{
		MaxConcurrency:             2,
		RootCatalogHintText:        &catalog,
		OmitUpdatePlanInstructions: true,
	})
	if strings.Contains(hint, "## Plan tool") || strings.Contains(hint, "Catalog root guidance.") {
		t.Fatalf("catalog plan-tool section survived:\n%s", hint)
	}
}
