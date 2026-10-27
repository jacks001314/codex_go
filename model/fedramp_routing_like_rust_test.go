package model

import (
	"testing"

	"codex_go/auth"
)

// Mirrors Rust #57178: FedRAMP issuance markers are routing hints only. The
// selected provider credential decides routing and the request header, and the
// complete key still authenticates unchanged.
func TestFedrampAPIKeyRoutingFollowsSelectedProviderCredentialLikeRust(t *testing.T) {
	const (
		projectKey    = "sk-proj-fedramp-synthetic-test-key"
		serviceKey    = "sk-svcacct-fedramp-synthetic-test-key"
		commercialKey = "sk-proj-synthetic-test-key"
	)
	cases := []struct {
		name        string
		ambientKey  string
		providerKey string
		wantFedramp bool
	}{
		{"ambient project marker", projectKey, "", true},
		{"ambient service-account marker", serviceKey, "", true},
		{"ambient unmarked key", commercialKey, "", false},
		{"provider commercial overrides ambient FedRAMP", projectKey, commercialKey, false},
		{"provider FedRAMP overrides ambient commercial", commercialKey, projectKey, true},
		{"provider service-account marker", commercialKey, serviceKey, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := CreateOpenAIProvider("")
			provider.ExperimentalBearerToken = testCase.providerKey
			snapshot := &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: testCase.ambientKey}

			headers, err := ResolveProviderAuth(snapshot, provider)
			if err != nil {
				t.Fatalf("ResolveProviderAuth() error = %v", err)
			}
			key := testCase.providerKey
			if key == "" {
				key = testCase.ambientKey
			}
			if got := headers.Headers.Get("Authorization"); got != "Bearer "+key {
				t.Fatalf("Authorization = %q, want complete selected key", got)
			}
			if got := headers.Headers.Get("X-OpenAI-Fedramp"); (got == "true") != testCase.wantFedramp {
				t.Fatalf("X-OpenAI-Fedramp = %q, want header %v", got, testCase.wantFedramp)
			}

			apiProvider, err := ResolveAPIProvider(provider, snapshot)
			if err != nil {
				t.Fatalf("ResolveAPIProvider() error = %v", err)
			}
			wantBaseURL := "https://api.openai.com/v1"
			if testCase.wantFedramp {
				wantBaseURL = FedRAMPAPIBaseURL
			}
			if apiProvider.BaseURL != wantBaseURL {
				t.Fatalf("base URL = %q, want %q", apiProvider.BaseURL, wantBaseURL)
			}
		})
	}
}

// Explicit endpoint overrides win, including an explicitly configured
// commercial default URL.
func TestFedrampRoutingPreservesExplicitBaseURLOverridesLikeRust(t *testing.T) {
	snapshot := &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-proj-fedramp-synthetic-test-key"}
	for _, baseURL := range []string{"https://api.openai.com/v1", "https://example.test/codex"} {
		provider := CreateOpenAIProvider(baseURL)
		apiProvider, err := ResolveAPIProvider(provider, snapshot)
		if err != nil {
			t.Fatalf("ResolveAPIProvider(%q) error = %v", baseURL, err)
		}
		if apiProvider.BaseURL != baseURL {
			t.Fatalf("base URL = %q, want explicit override %q", apiProvider.BaseURL, baseURL)
		}
	}
}

// Command-backed and non-OpenAI providers keep their configured routing.
func TestFedrampRoutingSkipsCommandAndNonOpenAIProvidersLikeRust(t *testing.T) {
	snapshot := &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-proj-fedramp-synthetic-test-key"}
	commandProvider := CreateOpenAIProvider("")
	commandProvider.Auth = &ProviderAuthInfo{Command: "token-helper"}
	nonOpenAI := ProviderInfo{Name: "Azure", BaseURL: "", RequiresOpenAIAuth: true}
	for name, provider := range map[string]ProviderInfo{
		"command auth": commandProvider,
		"non-OpenAI":   nonOpenAI,
	} {
		fedramp, err := UsesFedrampAPIKey(provider, snapshot)
		if err != nil {
			t.Fatalf("%s: UsesFedrampAPIKey() error = %v", name, err)
		}
		if fedramp {
			t.Fatalf("%s: FedRAMP routing selected", name)
		}
	}
}

// Ambient API-key snapshots carry the header without changing the stored key.
func TestFedrampAPIKeyAuthHeadersPreserveStoredKeyLikeRust(t *testing.T) {
	const key = "sk-proj-fedramp-synthetic-test-key"
	snapshot := auth.FromAPIKey(key)
	headers, err := AuthHeadersFromAuth(snapshot)
	if err != nil {
		t.Fatalf("AuthHeadersFromAuth() error = %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer "+key {
		t.Fatalf("Authorization = %q, want complete stored key", got)
	}
	if got := headers.Headers.Get("X-OpenAI-Fedramp"); got != "true" {
		t.Fatalf("X-OpenAI-Fedramp = %q, want true", got)
	}
	if snapshot.OpenAIAPIKey != key {
		t.Fatalf("stored key = %q, want unchanged %q", snapshot.OpenAIAPIKey, key)
	}
}

// Routing and authentication come from one credential snapshot, so a rotated
// credential cannot be sent to the previous credential's endpoint.
func TestAPIProviderAndAuthResolveFedrampTogetherLikeRust(t *testing.T) {
	const key = "sk-proj-fedramp-synthetic-test-key"
	runtimeProvider := CreateRuntimeProvider(CreateOpenAIProvider(""), &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: key})
	apiProvider, authHeaders, err := runtimeProvider.APIProviderAndAuth()
	if err != nil {
		t.Fatalf("APIProviderAndAuth() error = %v", err)
	}
	if apiProvider.BaseURL != FedRAMPAPIBaseURL {
		t.Fatalf("base URL = %q, want %q", apiProvider.BaseURL, FedRAMPAPIBaseURL)
	}
	if got := authHeaders.Headers.Get("Authorization"); got != "Bearer "+key {
		t.Fatalf("Authorization = %q", got)
	}
	if got := authHeaders.Headers.Get("X-OpenAI-Fedramp"); got != "true" {
		t.Fatalf("X-OpenAI-Fedramp = %q", got)
	}
}

func TestIsFedrampAPIKeyRecognizesOnlyIssuanceMarkers(t *testing.T) {
	for _, key := range []string{"sk-proj-fedramp-abc", "sk-svcacct-fedramp-abc"} {
		if !auth.IsFedrampAPIKey(key) {
			t.Fatalf("IsFedrampAPIKey(%q) = false", key)
		}
	}
	for _, key := range []string{"sk-proj-abc", "sk-fedramp-abc", "fedramp-abc", ""} {
		if auth.IsFedrampAPIKey(key) {
			t.Fatalf("IsFedrampAPIKey(%q) = true", key)
		}
	}
}
