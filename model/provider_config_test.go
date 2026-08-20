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
	if !strings.Contains(err.Error(), "model_providers.custom: provider aws is only supported for `amazon-bedrock` or `amazon-bedrock-runtime`") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderAWSConfigParsesAuthRefreshLikeRust(t *testing.T) {
	// Rust #39410: aws.auth_refresh carries a command, args, and timeout for
	// refreshing expired SDK credentials on Bedrock.
	values := map[string]any{
		"model_providers": map[string]any{
			"amazon-bedrock": map[string]any{
				"aws":    map[string]any{
					"region": "us-east-1",
					"auth_refresh": map[string]any{
						"command":    "aws",
						"args":       []string{"sso", "login", "--no-browser"},
						"timeout_ms": int64(30000),
					},
				},
			},
		},
	}
	providers, err := ProvidersFromConfig(values, "")
	if err != nil {
		t.Fatalf("ProvidersFromConfig error = %v", err)
	}
	bedrock, ok := providers[AmazonBedrockProviderID]
	if !ok {
		t.Fatalf("providers missing amazon-bedrock: %#v", providers)
	}
	if bedrock.AWS == nil || bedrock.AWS.AuthRefresh == nil {
		t.Fatalf("bedrock aws auth_refresh = %#v", bedrock.AWS)
	}
	if bedrock.AWS.AuthRefresh.Command != "aws" || len(bedrock.AWS.AuthRefresh.Args) != 3 || bedrock.AWS.AuthRefresh.TimeoutMS != 30000 {
		t.Fatalf("auth_refresh = %#v", bedrock.AWS.AuthRefresh)
	}
}

func TestProviderConfigRejectsReservedBuiltinIDsLikeRust(t *testing.T) {
	// Mirrors Rust validate_reserved_model_provider_ids
	// (config/src/config_toml.rs): openai/ollama/lmstudio cannot be overridden;
	// the two Bedrock ids are exempt because their blocks extend the built-ins.
	for _, reserved := range []string{OpenAIProviderID, OllamaOSSProviderID, LMStudioOSSProviderID} {
		values := map[string]any{
			"model_providers": map[string]any{
				reserved: map[string]any{"name": "Custom"},
			},
		}
		_, err := ProvidersFromConfig(values, "")
		if err == nil {
			t.Fatalf("ProvidersFromConfig accepted reserved id %q", reserved)
		}
		for _, want := range []string{
			"model_providers contains reserved built-in provider IDs",
			"Built-in providers cannot be overridden",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("reserved %q error %q missing %q", reserved, err, want)
			}
		}
	}

	// Bedrock ids stay extendable.
	for _, bedrock := range []string{AmazonBedrockProviderID, AmazonBedrockRuntimeProviderID} {
		values := map[string]any{
			"model_providers": map[string]any{
				bedrock: map[string]any{"aws": map[string]any{"profile": "dev"}},
			},
		}
		if _, err := ProvidersFromConfig(values, ""); err != nil {
			t.Fatalf("ProvidersFromConfig rejected bedrock extension %q: %v", bedrock, err)
		}
	}
}

func TestProviderConfigRejectsEmptyCustomProviderNameLikeRust(t *testing.T) {
	// Mirrors Rust validate_model_providers (config/src/config_toml.rs):
	// a non-Bedrock provider name must not be empty.
	values := map[string]any{
		"model_providers": map[string]any{
			"custom": map[string]any{"name": "   "},
		},
	}
	_, err := ProvidersFromConfig(values, "")
	if err == nil || !strings.Contains(err.Error(), "model_providers.custom: provider name must not be empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderConfigAcceptsBedrockRuntimeProvider(t *testing.T) {
	// Mirrors Rust merge_configured_model_providers: the runtime variant
	// accepts base_url/auth/aws/http_headers extensions like the standard one.
	values := map[string]any{
		"model_providers": map[string]any{
			AmazonBedrockRuntimeProviderID: map[string]any{
				"aws": map[string]any{"profile": "codex-bedrock", "region": "us-west-2"},
			},
		},
	}
	providers, err := ProvidersFromConfig(values, "")
	if err != nil {
		t.Fatalf("ProvidersFromConfig returned error: %v", err)
	}
	provider, ok := providers[AmazonBedrockRuntimeProviderID]
	if !ok {
		t.Fatal("amazon-bedrock-runtime provider missing after merge")
	}
	if provider.Name != AmazonBedrockRuntimeProviderName {
		t.Fatalf("name = %q, want %q", provider.Name, AmazonBedrockRuntimeProviderName)
	}
	if provider.AWS == nil || provider.AWS.Profile != "codex-bedrock" || provider.AWS.Region != "us-west-2" {
		t.Fatalf("aws override = %#v", provider.AWS)
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
	if !strings.Contains(err.Error(), "only supports changing `base_url`, `auth`, `http_headers`, `aws.profile`, `aws.region`, and `aws.auth_refresh`") {
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

func TestProviderConfigAppliesAmazonBedrockTransportOverrides(t *testing.T) {
	values := map[string]any{"model_providers": map[string]any{
		AmazonBedrockProviderID: map[string]any{
			"base_url":     "https://proxy.example.com/v1",
			"auth":         map[string]any{"command": "print-token", "args": []any{"--json"}},
			"http_headers": map[string]any{"x-example-header": "value"},
			"aws":          map[string]any{"profile": "codex-bedrock", "region": "us-west-2"},
		},
	}}
	provider, err := ProviderForConfigID(values, AmazonBedrockProviderID, "")
	if err != nil {
		t.Fatal(err)
	}
	if provider.BaseURL != "https://proxy.example.com/v1" || provider.Auth == nil || provider.Auth.Command != "print-token" || provider.AWS == nil || provider.AWS.Profile != "codex-bedrock" || provider.HTTPHeaders["x-example-header"] != "value" || provider.HTTPHeaders[AmazonBedrockMantleClientHeader] != AmazonBedrockMantleClientValue {
		t.Fatalf("provider = %+v", provider)
	}
}

func TestProviderConfigRejectsEmptyAmazonBedrockAuthCommand(t *testing.T) {
	values := map[string]any{"model_providers": map[string]any{
		AmazonBedrockProviderID: map[string]any{"auth": map[string]any{"command": "  "}},
	}}
	_, err := ProvidersFromConfig(values, "")
	if err == nil || !strings.Contains(err.Error(), "model_providers.amazon-bedrock: provider auth.command must not be empty") {
		t.Fatalf("error = %v", err)
	}
}
