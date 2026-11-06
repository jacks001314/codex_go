package runtimeauth

import "testing"

// Rust #59823: runtime auth must follow the provider's selected credential
// source, so a provider with its own key or command credential never reuses the
// saved-login runtime.
func TestProviderForConfigBindsToCredentialSourceLikeRust(t *testing.T) {
	settings := map[string]bool{"runtime_agent_instances": true}
	base := ProviderOptions{
		FeatureSettings: settings,
		CodexHome:       t.TempDir(),
		InstallationID:  "install-1",
		Harness:         "codex-go",
	}
	withValues := func(values map[string]any) *Provider {
		options := base
		options.Values = values
		return ProviderForConfig(options)
	}

	if provider := withValues(map[string]any{"model_provider": "openai"}); provider == nil {
		t.Fatal("saved-login provider lost runtime auth")
	}
	cases := map[string]map[string]any{
		"env_key": {
			"model_provider": "openai",
			"model_providers": map[string]any{
				"openai": map[string]any{"env_key": "OPENAI_API_KEY"},
			},
		},
		"bearer token": {
			"model_provider":            "openai",
			"experimental_bearer_token": "sk-test",
		},
		"command auth": {
			"model_provider": "openai",
			"model_providers": map[string]any{
				"openai": map[string]any{"auth": map[string]any{"command": "print-token"}},
			},
		},
	}
	for name, values := range cases {
		if provider := withValues(values); provider != nil {
			t.Fatalf("%s selected an explicit credential but kept the login runtime", name)
		}
	}
	// A provider-specific key on another provider does not affect the selection.
	other := map[string]any{
		"model_provider": "openai",
		"model_providers": map[string]any{
			"custom": map[string]any{"env_key": "CUSTOM_KEY"},
		},
	}
	if provider := withValues(other); provider == nil {
		t.Fatal("unselected provider credentials removed runtime auth")
	}
}
