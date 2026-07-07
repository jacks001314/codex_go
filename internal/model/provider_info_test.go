package model

import (
	"strings"
	"testing"
	"time"
)

func TestParseWireAPIRejectsChat(t *testing.T) {
	_, err := ParseWireAPI("chat")
	if err == nil {
		t.Fatal("ParseWireAPI returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "`wire_api = \"chat\"` is no longer supported.") ||
		!strings.Contains(err.Error(), "How to fix: set `wire_api = \"responses\"` in your provider config.") ||
		!strings.Contains(err.Error(), "https://github.com/openai/codex/discussions/7782") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAIProviderUsesChatGPTBaseURLForPersonalAccessToken(t *testing.T) {
	provider := CreateOpenAIProvider("")
	apiProvider, err := (&provider).ToAPIProvider("personal-access-token")
	if err != nil {
		t.Fatalf("ToAPIProvider returned error: %v", err)
	}
	if apiProvider.BaseURL != ChatGPTCodexBaseURL {
		t.Fatalf("BaseURL = %q", apiProvider.BaseURL)
	}
}

func TestBuildHeaderMapIncludesEnvHeaders(t *testing.T) {
	t.Setenv("EXAMPLE_ENV_VAR", "env-value")
	provider := ProviderInfo{
		HTTPHeaders:    map[string]string{"X-Static": "static-value"},
		EnvHTTPHeaders: map[string]string{"X-Env": "EXAMPLE_ENV_VAR", "X-Empty": "MISSING_ENV_VAR"},
	}
	headers := provider.BuildHeaderMap()
	if headers.Get("X-Static") != "static-value" {
		t.Fatalf("X-Static = %q", headers.Get("X-Static"))
	}
	if headers.Get("X-Env") != "env-value" {
		t.Fatalf("X-Env = %q", headers.Get("X-Env"))
	}
	if headers.Get("X-Empty") != "" {
		t.Fatalf("X-Empty = %q", headers.Get("X-Empty"))
	}
}

func TestBuildHeaderMapSkipsInvalidHeaders(t *testing.T) {
	t.Setenv("GOOD_ENV_VAR", "env-value")
	t.Setenv("BAD_ENV_VAR", "bad\r\nvalue")
	provider := ProviderInfo{
		HTTPHeaders: map[string]string{
			"X-Static":       "static-value",
			"Bad Header":     "bad-name",
			"X-Bad-Value":    "bad\nvalue",
			"X-Tab-Is-Valid": "ok\tvalue",
		},
		EnvHTTPHeaders: map[string]string{
			"X-Env":           "GOOD_ENV_VAR",
			"Bad Env Header":  "GOOD_ENV_VAR",
			"X-Bad-Env-Value": "BAD_ENV_VAR",
		},
	}
	headers := provider.BuildHeaderMap()
	if headers.Get("X-Static") != "static-value" || headers.Get("X-Env") != "env-value" {
		t.Fatalf("valid headers missing: %#v", headers)
	}
	if headers.Get("X-Tab-Is-Valid") != "ok\tvalue" {
		t.Fatalf("tab header value = %q", headers.Get("X-Tab-Is-Valid"))
	}
	if headers.Get("Bad Header") != "" || headers.Get("X-Bad-Value") != "" || headers.Get("X-Bad-Env-Value") != "" {
		t.Fatalf("invalid headers were retained: %#v", headers)
	}
}

func TestProviderAPIKeyRequiresEnvKey(t *testing.T) {
	provider := ProviderInfo{EnvKey: "CUSTOM_API_KEY", EnvKeyInstructions: "set it first"}
	_, err := provider.APIKey()
	if err == nil {
		t.Fatal("APIKey returned nil error, want missing env failure")
	}
	if !strings.Contains(err.Error(), "set it first") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderRetryAndTimeoutDefaultsAndCaps(t *testing.T) {
	requestRetries := uint64(999)
	streamRetries := uint64(999)
	idle := uint64(12)
	websocket := uint64(34)
	provider := ProviderInfo{
		RequestMaxRetries:         &requestRetries,
		StreamMaxRetries:          &streamRetries,
		StreamIdleTimeoutMS:       &idle,
		WebsocketConnectTimeoutMS: &websocket,
	}
	if provider.EffectiveRequestMaxRetries() != MaxRequestMaxRetries {
		t.Fatalf("request retries = %d", provider.EffectiveRequestMaxRetries())
	}
	if provider.EffectiveStreamMaxRetries() != MaxStreamMaxRetries {
		t.Fatalf("stream retries = %d", provider.EffectiveStreamMaxRetries())
	}
	if provider.EffectiveStreamIdleTimeout() != 12*time.Millisecond {
		t.Fatalf("stream idle timeout = %s", provider.EffectiveStreamIdleTimeout())
	}
	if provider.EffectiveWebsocketConnectTimeout() != 34*time.Millisecond {
		t.Fatalf("websocket timeout = %s", provider.EffectiveWebsocketConnectTimeout())
	}
}

func TestUsesOpenAIActorAuthorization(t *testing.T) {
	provider := ProviderInfo{
		HTTPHeaders: map[string]string{"X-OpenAI-Actor-Authorization": "actor-token"},
	}
	if !provider.UsesOpenAIActorAuthorization() {
		t.Fatal("UsesOpenAIActorAuthorization = false, want true")
	}
	provider.RequiresOpenAIAuth = true
	if provider.UsesOpenAIActorAuthorization() {
		t.Fatal("UsesOpenAIActorAuthorization = true with requires_openai_auth")
	}
}

func TestSupportsRemoteCompaction(t *testing.T) {
	openAIProvider := CreateOpenAIProvider("")
	if !(&openAIProvider).SupportsRemoteCompaction() {
		t.Fatal("OpenAI provider should support remote compaction")
	}
	azure := ProviderInfo{Name: "Azure", BaseURL: "https://example.openai.azure.com/openai"}
	if !(&azure).SupportsRemoteCompaction() {
		t.Fatal("Azure provider should support remote compaction")
	}
	custom := ProviderInfo{Name: "Example", BaseURL: "https://example.com/v1"}
	if (&custom).SupportsRemoteCompaction() {
		t.Fatal("custom provider should not support remote compaction")
	}
}

func TestIsAzureResponsesProviderMatchesRustMarkers(t *testing.T) {
	cases := []string{
		"https://foo.openai.azure.com/openai",
		"https://foo.openai.azure.us/openai/deployments/bar",
		"https://foo.cognitiveservices.azure.cn/openai",
		"https://foo.aoai.azure.com/openai",
		"https://foo.openai.azure-api.net/openai",
		"https://foo.azurefd.net/openai",
		"https://foo.windows.net/openai",
	}
	for _, baseURL := range cases {
		if !IsAzureResponsesProvider("", baseURL) {
			t.Fatalf("IsAzureResponsesProvider(%q) = false, want true", baseURL)
		}
	}
	if !IsAzureResponsesProvider("Azure", "https://example.com/openai") {
		t.Fatal("Azure provider name should be treated as Azure responses provider")
	}
	if IsAzureResponsesProvider("", "https://example.com/v1") {
		t.Fatal("example.com should not be treated as Azure responses provider")
	}
}

func TestCreateAmazonBedrockProvider(t *testing.T) {
	provider := CreateAmazonBedrockProvider(nil)
	if !(&provider).IsAmazonBedrock() {
		t.Fatalf("provider name = %q", provider.Name)
	}
	if provider.HTTPHeaders[AmazonBedrockMantleClientHeader] != AmazonBedrockMantleClientValue {
		t.Fatalf("headers = %#v", provider.HTTPHeaders)
	}
	if provider.AWS == nil {
		t.Fatal("AWS auth config is nil")
	}
}

func TestMergeConfiguredProvidersAddsCustomProvider(t *testing.T) {
	custom := ProviderInfo{Name: "Custom", BaseURL: "https://example.com/v1"}
	merged, err := MergeConfiguredProviders(BuiltInProviders(""), map[string]ProviderInfo{"custom": custom})
	if err != nil {
		t.Fatalf("MergeConfiguredProviders returned error: %v", err)
	}
	if merged["custom"].Name != "Custom" {
		t.Fatalf("custom provider = %#v", merged["custom"])
	}
}

func TestMergeConfiguredProvidersAppliesAmazonBedrockAWSOverride(t *testing.T) {
	merged, err := MergeConfiguredProviders(BuiltInProviders(""), map[string]ProviderInfo{
		AmazonBedrockProviderID: {
			AWS: &ProviderAWSAuthInfo{Profile: "codex-bedrock", Region: "us-west-2"},
		},
	})
	if err != nil {
		t.Fatalf("MergeConfiguredProviders returned error: %v", err)
	}
	aws := merged[AmazonBedrockProviderID].AWS
	if aws == nil || aws.Profile != "codex-bedrock" || aws.Region != "us-west-2" {
		t.Fatalf("aws override = %#v", aws)
	}
}

func TestMergeConfiguredProvidersRejectsAmazonBedrockNonAWSOverride(t *testing.T) {
	_, err := MergeConfiguredProviders(BuiltInProviders(""), map[string]ProviderInfo{
		AmazonBedrockProviderID: {
			BaseURL: "https://example.com/v1",
		},
	})
	if err == nil {
		t.Fatal("MergeConfiguredProviders returned nil error, want failure")
	}
}

func TestValidateRejectsConflictingAWSAuth(t *testing.T) {
	provider := ProviderInfo{
		AWS:                &ProviderAWSAuthInfo{},
		EnvKey:             "API_KEY",
		SupportsWebsockets: false,
	}
	err := provider.Validate()
	if err == nil {
		t.Fatal("Validate returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "env_key") {
		t.Fatalf("error = %v", err)
	}
}
