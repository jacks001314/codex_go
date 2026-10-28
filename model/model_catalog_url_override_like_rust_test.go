package model

import (
	"reflect"
	"strings"
	"testing"
)

const openAICatalogURLForTest = "https://chatgpt.com/backend-api/codex/model-catalog"

const openAICatalogHostErrorMessage = "model_providers.openai.model_catalog_url must use an allowed ChatGPT host. If you need to use a 3rd party host, configure a custom model_provider"

func TestMergeConfiguredProvidersAppliesOpenAICatalogURLOverride(t *testing.T) {
	inferenceURL := "https://api.openai.example/v1"
	builtIn := BuiltInProviders(inferenceURL)[OpenAIProviderID]

	merged, err := MergeConfiguredProviders(BuiltInProviders(inferenceURL), map[string]ProviderInfo{
		OpenAIProviderID: {ModelCatalogURL: openAICatalogURLForTest},
	})
	if err != nil {
		t.Fatalf("MergeConfiguredProviders() error = %v", err)
	}
	got := merged[OpenAIProviderID]
	if got.ModelCatalogURL != openAICatalogURLForTest {
		t.Fatalf("model_catalog_url = %q, want %q", got.ModelCatalogURL, openAICatalogURLForTest)
	}
	if got.Name != builtIn.Name || got.BaseURL != builtIn.BaseURL || got.EnvKey != builtIn.EnvKey {
		t.Fatalf("built-in identity changed: %+v", got)
	}
	if got.BaseURL != inferenceURL {
		t.Fatalf("base_url = %q, want retained inference URL %q", got.BaseURL, inferenceURL)
	}
	if !reflect.DeepEqual(got.Auth, builtIn.Auth) || got.RequiresOpenAIAuth != builtIn.RequiresOpenAIAuth {
		t.Fatalf("built-in authentication changed: %+v", got)
	}
}

func TestMergeConfiguredProvidersRejectsInvalidOpenAICatalogURL(t *testing.T) {
	cases := map[string]string{
		"plain http":           "http://chatgpt.com/backend-api/codex/model-catalog",
		"third-party host":     "https://example.com/model-catalog.json",
		"embedded credentials": "https://user:pass@chatgpt.com/backend-api/codex/model-catalog",
		"relative url":         "/backend-api/codex/model-catalog",
		"empty host":           "https:///backend-api/codex/model-catalog",
	}
	for name, catalogURL := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := MergeConfiguredProviders(BuiltInProviders(""), map[string]ProviderInfo{
				OpenAIProviderID: {ModelCatalogURL: catalogURL},
			})
			if err == nil || err.Error() != openAICatalogHostErrorMessage {
				t.Fatalf("error = %v, want %q", err, openAICatalogHostErrorMessage)
			}
		})
	}
}

func TestConfiguredProviderMapAcceptsOpenAICatalogOnlyOverride(t *testing.T) {
	configured, err := ConfiguredProviderMap(map[string]any{
		OpenAIProviderID: map[string]any{"model_catalog_url": openAICatalogURLForTest},
	})
	if err != nil {
		t.Fatalf("ConfiguredProviderMap() error = %v", err)
	}
	if configured[OpenAIProviderID].ModelCatalogURL != openAICatalogURLForTest {
		t.Fatalf("configured openai = %+v", configured[OpenAIProviderID])
	}
}

func TestConfiguredProviderMapRejectsNonCatalogOpenAIOverrides(t *testing.T) {
	cases := map[string]map[string]any{
		"name": {
			"name":              "Custom OpenAI",
			"model_catalog_url": openAICatalogURLForTest,
		},
		"base_url": {
			"base_url":          "https://example.com/v1",
			"model_catalog_url": openAICatalogURLForTest,
		},
		"env_key": {
			"env_key":           "OPENAI_API_KEY",
			"model_catalog_url": openAICatalogURLForTest,
		},
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ConfiguredProviderMap(map[string]any{OpenAIProviderID: block})
			if err == nil || !strings.Contains(err.Error(), "reserved built-in provider IDs") {
				t.Fatalf("error = %v, want reserved provider rejection", err)
			}
		})
	}
}

func TestRequiredModelProviderDefinitionWithBaseURLKeepsThreadInference(t *testing.T) {
	inferenceURL := "https://thread.openai.example/v1"
	provider, err := RequiredModelProviderDefinitionWithBaseURL(OpenAIProviderID, map[string]any{
		"model_catalog_url": openAICatalogURLForTest,
	}, inferenceURL)
	if err != nil {
		t.Fatalf("RequiredModelProviderDefinitionWithBaseURL() error = %v", err)
	}
	if provider == nil {
		t.Fatal("RequiredModelProviderDefinitionWithBaseURL() = nil")
	}
	if provider.BaseURL != inferenceURL {
		t.Fatalf("base_url = %q, want %q", provider.BaseURL, inferenceURL)
	}
	if provider.ModelCatalogURL != openAICatalogURLForTest {
		t.Fatalf("model_catalog_url = %q", provider.ModelCatalogURL)
	}
}

func TestRequiredModelProviderDefinitionRejectsInvalidOpenAICatalogURL(t *testing.T) {
	_, err := RequiredModelProviderDefinition(OpenAIProviderID, map[string]any{
		"model_catalog_url": "https://example.com/model-catalog.json",
	})
	if err == nil || err.Error() != openAICatalogHostErrorMessage {
		t.Fatalf("error = %v, want %q", err, openAICatalogHostErrorMessage)
	}
}
