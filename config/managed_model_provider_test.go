package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestManagedModelProviderRequirementsOverrideUserConfigLikeRust mirrors Rust
// #44650: a required model_provider overrides local configuration, and a
// required provider definition replaces the corresponding local entry.
func TestManagedModelProviderRequirementsOverrideUserConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `
model_provider = "local"

[model_providers.local]
name = "Local"
base_url = "https://local.example/v1"

[model_providers.gateway]
name = "User Gateway"
base_url = "https://user-gateway.example/v1"
http_headers = { "X-User-Header" = "user" }
`)
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(`
model_provider = "gateway"

[model_providers.gateway]
name = "Gateway"
base_url = "https://gateway.example/v1"
http_headers = { "Authorization" = "Bearer managed" }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffectiveWithOptions(home, &EffectiveOptions{})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if got := cfg.Values["model_provider"]; got != "gateway" {
		t.Fatalf("model_provider = %#v, want managed selection", got)
	}
	providers, ok := cfg.Values["model_providers"].(map[string]any)
	if !ok {
		t.Fatalf("model_providers = %#v", cfg.Values["model_providers"])
	}
	if _, ok := providers["local"]; !ok {
		t.Fatalf("local provider entry was dropped: %#v", providers)
	}
	gateway, ok := providers["gateway"].(map[string]any)
	if !ok {
		t.Fatalf("gateway provider = %#v", providers["gateway"])
	}
	if gateway["name"] != "Gateway" || gateway["base_url"] != "https://gateway.example/v1" {
		t.Fatalf("gateway definition = %#v", gateway)
	}
	headers, ok := gateway["http_headers"].(map[string]any)
	if !ok || headers["Authorization"] != "Bearer managed" || headers["X-User-Header"] != nil {
		t.Fatalf("gateway headers = %#v, want managed definition to replace local", gateway["http_headers"])
	}

	requirements, err := LoadRequirementsFile(filepath.Join(home, "requirements.toml"))
	if err != nil {
		t.Fatalf("LoadRequirementsFile() error = %v", err)
	}
	if requirements == nil || requirements.ModelProvider == nil || *requirements.ModelProvider != "gateway" {
		t.Fatalf("requirements.ModelProvider = %#v", requirements)
	}
	if len(requirements.ModelProviders) != 1 {
		t.Fatalf("requirements.ModelProviders = %#v", requirements.ModelProviders)
	}

	service := NewConfigService(home)
	service.SetRequirements(requirements)
	read := service.Requirements()
	if read == nil || read.Requirements == nil || read.Requirements.ModelProvider == nil || *read.Requirements.ModelProvider != "gateway" {
		t.Fatalf("Requirements() = %#v", read)
	}
	if len(read.Requirements.ModelProviders) != 1 {
		t.Fatalf("Requirements().ModelProviders = %#v", read.Requirements.ModelProviders)
	}
}

// TestManagedModelProviderWriteRejectedLikeRust verifies config write APIs
// reject managed provider settings, including provider IDs containing dots.
func TestManagedModelProviderWriteRejectedLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n")
	provider := "gateway"
	service := NewConfigService(home)
	service.SetRequirements(&ConfigRequirements{
		ModelProvider: &provider,
		ModelProviders: map[string]any{
			"my.gateway": map[string]any{"base_url": "https://gateway.example/v1"},
		},
	})

	for _, keyPath := range []string{
		"model_provider",
		"model_providers",
		"model_providers.my.gateway",
		"model_providers.my.gateway.base_url",
	} {
		if _, err := service.WriteValue(&ConfigValueWriteParams{KeyPath: keyPath, Value: "changed", MergeStrategy: MergeReplace}); err == nil {
			t.Fatalf("WriteValue(%s) succeeded, want managed write rejection", keyPath)
		}
	}
	if _, err := service.WriteValue(&ConfigValueWriteParams{KeyPath: "model", Value: "gpt-5.1", MergeStrategy: MergeReplace}); err != nil {
		t.Fatalf("WriteValue(model) error = %v, want unrelated keys writable", err)
	}
}
