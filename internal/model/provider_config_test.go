package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderForConfigIDReadsConfiguredProvider(t *testing.T) {
	values := map[string]any{
		"model_providers": map[string]any{
			"custom": map[string]any{
				"name":     "Custom",
				"base_url": "https://example.test/v1",
				"env_key":  "CUSTOM_API_KEY",
				"wire_api": "responses",
				"http_headers": map[string]any{
					"X-Test": "ok",
				},
				"query_params": map[string]any{
					"api-version": "2026-06-30",
				},
				"request_max_retries": float64(9),
			},
		},
	}
	provider, err := ProviderForConfigID(values, "custom", "")
	if err != nil {
		t.Fatalf("ProviderForConfigID error = %v", err)
	}
	if provider.Name != "Custom" || provider.BaseURL != "https://example.test/v1" || provider.EnvKey != "CUSTOM_API_KEY" {
		t.Fatalf("provider = %#v", provider)
	}
	if provider.HTTPHeaders["X-Test"] != "ok" || provider.QueryParams["api-version"] != "2026-06-30" {
		t.Fatalf("provider maps = %#v %#v", provider.HTTPHeaders, provider.QueryParams)
	}
	if provider.RequestMaxRetries == nil || *provider.RequestMaxRetries != 9 {
		t.Fatalf("request retries = %#v", provider.RequestMaxRetries)
	}
}

func TestProviderAuthConfigDefaultsMatchRust(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	provider, err := ProviderInfoFromConfig(map[string]any{
		"name": "Corp",
		"auth": map[string]any{
			"command": "print-token",
			"args":    []any{"--format=text"},
		},
	})
	if err != nil {
		t.Fatalf("ProviderInfoFromConfig returned error: %v", err)
	}
	if provider.Auth == nil {
		t.Fatal("provider.Auth is nil")
	}
	if provider.Auth.TimeoutMS != DefaultProviderAuthTimeoutMS {
		t.Fatalf("timeout_ms = %d", provider.Auth.TimeoutMS)
	}
	if provider.Auth.RefreshIntervalMS != DefaultProviderAuthRefreshMS {
		t.Fatalf("refresh_interval_ms = %d", provider.Auth.RefreshIntervalMS)
	}
	if provider.Auth.CWD != filepath.Clean(cwd) {
		t.Fatalf("cwd = %q, want %q", provider.Auth.CWD, filepath.Clean(cwd))
	}
	if provider.WireAPI != WireAPIResponses {
		t.Fatalf("wire_api = %q", provider.WireAPI)
	}
}

func TestProviderAuthConfigRejectsZeroTimeout(t *testing.T) {
	_, err := ProviderInfoFromConfig(map[string]any{
		"name": "Corp",
		"auth": map[string]any{
			"command":    "print-token",
			"timeout_ms": float64(0),
		},
	})
	if err == nil {
		t.Fatal("ProviderInfoFromConfig returned nil error, want timeout failure")
	}
	if !strings.Contains(err.Error(), "provider auth.timeout_ms must be non-zero") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderAuthConfigAllowsDisabledRefresh(t *testing.T) {
	provider, err := ProviderInfoFromConfig(map[string]any{
		"name": "Corp",
		"auth": map[string]any{
			"command":             "print-token",
			"refresh_interval_ms": float64(0),
		},
	})
	if err != nil {
		t.Fatalf("ProviderInfoFromConfig returned error: %v", err)
	}
	if provider.Auth == nil || provider.Auth.RefreshIntervalMS != 0 {
		t.Fatalf("auth = %#v", provider.Auth)
	}
}

func TestProviderForConfigIDReturnsBuiltin(t *testing.T) {
	provider, err := ProviderForConfigID(nil, OpenAIProviderID, "https://example.test/v1")
	if err != nil {
		t.Fatalf("ProviderForConfigID error = %v", err)
	}
	if provider.Name != OpenAIProviderName || provider.BaseURL != "https://example.test/v1" {
		t.Fatalf("provider = %#v", provider)
	}
}

func TestProviderForConfigIDRejectsLegacyOllamaChat(t *testing.T) {
	_, err := ProviderForConfigID(nil, LegacyOllamaChatProviderID, "")
	if err == nil {
		t.Fatal("ProviderForConfigID returned nil error, want ollama-chat removed failure")
	}
	if !strings.Contains(err.Error(), OllamaChatProviderRemovedMessage) {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderForConfigIDUnknownProviderMessageMatchesRust(t *testing.T) {
	_, err := ProviderForConfigID(nil, "missing-provider", "")
	if err == nil {
		t.Fatal("ProviderForConfigID returned nil error, want missing provider failure")
	}
	if err.Error() != "Model provider `missing-provider` not found" {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderConfigRejectsAWSForCustomProvider(t *testing.T) {
	values := map[string]any{
		"model_providers": map[string]any{
			"custom": map[string]any{
				"name": "Custom",
				"aws": map[string]any{
					"profile": "codex-bedrock",
				},
			},
		},
	}
	_, err := ProvidersFromConfig(values, "")
	if err == nil {
		t.Fatal("ProvidersFromConfig returned nil error, want aws provider failure")
	}
	if !strings.Contains(err.Error(), "model_providers.custom: provider aws is only supported for `amazon-bedrock`") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderConfigRejectsAuthWithEnvKey(t *testing.T) {
	values := map[string]any{
		"model_providers": map[string]any{
			"corp": map[string]any{
				"name":    "Corp",
				"env_key": "CORP_TOKEN",
				"auth": map[string]any{
					"command": "print-token",
				},
			},
		},
	}
	_, err := ProvidersFromConfig(values, "")
	if err == nil {
		t.Fatal("ProvidersFromConfig returned nil error, want auth/env_key failure")
	}
	if !strings.Contains(err.Error(), "model_providers.corp: provider auth cannot be combined with env_key") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderConfigAllowsOnlyAmazonBedrockAWSOverride(t *testing.T) {
	values := map[string]any{
		"model_providers": map[string]any{
			AmazonBedrockProviderID: map[string]any{
				"aws": map[string]any{
					"profile": "codex-bedrock",
					"region":  "us-west-2",
				},
			},
		},
	}
	provider, err := ProviderForConfigID(values, AmazonBedrockProviderID, "")
	if err != nil {
		t.Fatalf("ProviderForConfigID error = %v", err)
	}
	if provider.AWS == nil || provider.AWS.Profile != "codex-bedrock" || provider.AWS.Region != "us-west-2" {
		t.Fatalf("aws = %#v", provider.AWS)
	}

	values["model_providers"].(map[string]any)[AmazonBedrockProviderID] = map[string]any{
		"name": "Custom Bedrock",
		"aws":  map[string]any{"profile": "codex-bedrock"},
	}
	_, err = ProviderForConfigID(values, AmazonBedrockProviderID, "")
	if err == nil {
		t.Fatal("ProviderForConfigID returned nil error, want unsupported override failure")
	}
	if !strings.Contains(err.Error(), "only supports changing aws.profile and aws.region") {
		t.Fatalf("error = %v", err)
	}

	values["model_providers"].(map[string]any)[AmazonBedrockProviderID] = map[string]any{
		"wire_api": "responses",
		"aws":      map[string]any{"profile": "codex-bedrock"},
	}
	provider, err = ProviderForConfigID(values, AmazonBedrockProviderID, "")
	if err != nil {
		t.Fatalf("ProviderForConfigID with default wire_api error = %v", err)
	}
	if provider.AWS == nil || provider.AWS.Profile != "codex-bedrock" {
		t.Fatalf("aws = %#v", provider.AWS)
	}
}
