package exec

import (
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

func customProviderCapabilityConfig(externalWebAccess *bool, remoteCompaction string) *config.Config {
	capabilities := map[string]any{}
	if externalWebAccess != nil {
		capabilities["external_web_access"] = *externalWebAccess
	}
	if remoteCompaction != "" {
		capabilities["remote_compaction"] = remoteCompaction
	}
	provider := map[string]any{
		"name":     "Custom",
		"base_url": "https://example.com/v1",
		"wire_api": "responses",
	}
	if len(capabilities) > 0 {
		provider["capabilities"] = capabilities
	}
	return &config.Config{Values: map[string]any{
		"model_provider": "custom",
		"web_search":     "live",
		"model_providers": map[string]any{
			"custom": provider,
		},
	}}
}

func hostedWebSearchToolExternalAccess(t *testing.T, tools []any) bool {
	t.Helper()
	for _, tool := range tools {
		entry, _ := tool.(map[string]any)
		if entry["type"] != turn.HostedWebSearchToolType {
			continue
		}
		external, ok := entry["external_web_access"].(bool)
		if !ok {
			t.Fatalf("hosted web search tool = %#v", entry)
		}
		return external
	}
	t.Fatalf("hosted web search tool missing from %#v", tools)
	return false
}

// TestCustomProviderCapabilityOverridesDriveRequestsLikeRust mirrors Rust
// #50459's request snapshot: a custom provider with
// `external_web_access = false` pins hosted search to cached even when the
// session asks for live search, and `remote_compaction = "v2"` enables the
// Responses compaction protocol. Omitted capabilities keep provider defaults.
func TestCustomProviderCapabilityOverridesDriveRequestsLikeRust(t *testing.T) {
	disabled := false
	overridden := customProviderCapabilityConfig(&disabled, "v2")
	runner := &Runner{UseResponsesAPI: true}
	tools, err := runner.hostedToolsForRun(overridden, nil, "custom", "gpt-5", false, nil, nil)
	if err != nil {
		t.Fatalf("hostedToolsForRun: %v", err)
	}
	if hostedWebSearchToolExternalAccess(t, tools) {
		t.Fatal("capabilities.external_web_access=false must pin hosted search to cached")
	}
	if !execProviderSupportsRemoteCompaction(overridden, "custom") {
		t.Fatal("capabilities.remote_compaction=v2 must enable remote compaction")
	}

	defaults := customProviderCapabilityConfig(nil, "")
	tools, err = runner.hostedToolsForRun(defaults, nil, "custom", "gpt-5", false, nil, nil)
	if err != nil {
		t.Fatalf("hostedToolsForRun(defaults): %v", err)
	}
	if !hostedWebSearchToolExternalAccess(t, tools) {
		t.Fatal("unspecified capabilities must keep live external web access")
	}
	if execProviderSupportsRemoteCompaction(defaults, "custom") {
		t.Fatal("unspecified capabilities must keep custom-provider local compaction")
	}
}
