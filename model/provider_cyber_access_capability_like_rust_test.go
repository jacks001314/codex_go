package model

import "testing"

// TestProviderCyberAccessProgramsCapabilityLikeRust covers Rust #58190: a
// non-OpenAI provider opts into Cyber access programs through
// `capabilities.cyber_access_programs`, both from parsed config and from the
// derived API-key policy.
func TestProviderCyberAccessProgramsCapabilityLikeRust(t *testing.T) {
	provider, err := ProviderInfoFromConfig(map[string]any{
		"name":     "custom",
		"base_url": "https://custom.example/v1",
		"capabilities": map[string]any{
			"cyber_access_programs": true,
		},
	})
	if err != nil {
		t.Fatalf("ProviderInfoFromConfig() error = %v", err)
	}
	if provider.Capabilities == nil || provider.Capabilities.CyberAccessPrograms == nil || !*provider.Capabilities.CyberAccessPrograms {
		t.Fatalf("capabilities = %#v", provider.Capabilities)
	}
	if !provider.SupportsCyberAccessPrograms("custom") {
		t.Fatal("provider with the capability must qualify")
	}
	if (&ProviderInfo{}).SupportsCyberAccessPrograms("custom") {
		t.Fatal("provider without the capability must not qualify")
	}
	if !(&ProviderInfo{}).SupportsCyberAccessPrograms(OpenAIProviderID) {
		t.Fatal("the OpenAI provider always qualifies")
	}

	settings := map[string]bool{"api_key_cyber_access_programs": true}
	if got := ApiKeyCyberAccessProgramsFromConfig(settings, "custom", *provider); got != ApiKeyCyberAccessProgramsEnabled {
		t.Fatalf("policy = %v, want Enabled", got)
	}
	if got := ApiKeyCyberAccessProgramsFromConfig(settings, "custom", ProviderInfo{}); got != ApiKeyCyberAccessProgramsUnsupportedProvider {
		t.Fatalf("policy = %v, want UnsupportedProvider", got)
	}
	if got := ApiKeyCyberAccessProgramsFromConfig(map[string]bool{"api_key_cyber_access_programs": false}, "custom", *provider); got != ApiKeyCyberAccessProgramsDisabled {
		t.Fatalf("policy = %v, want Disabled", got)
	}

	disabled, err := ProviderInfoFromConfig(map[string]any{
		"capabilities": map[string]any{"cyber_access_programs": false},
	})
	if err != nil {
		t.Fatalf("ProviderInfoFromConfig() error = %v", err)
	}
	if disabled.SupportsCyberAccessPrograms("custom") {
		t.Fatal("an explicit false capability must not qualify")
	}
	if _, err := ProviderInfoFromConfig(map[string]any{
		"capabilities": map[string]any{"cyber_access_programs": "yes"},
	}); err == nil {
		t.Fatal("a non-boolean capability must be rejected")
	}
}
