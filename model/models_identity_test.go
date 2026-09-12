package model

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"codex_go/auth"
)

// chatGPTCatalogAuth mirrors Rust's `models_identity_tests::chatgpt_auth`: the
// access token carries the account identity claims and only its signature
// changes across a refresh.
func chatGPTCatalogAuth(t *testing.T, email, user, account, plan, signature string) *auth.AuthDotJSON {
	t.Helper()
	claims := map[string]any{
		"email": email,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_user_id": user,
		},
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	accessToken := "e30." + base64.RawURLEncoding.EncodeToString(payload) + "." + signature
	snapshot := auth.FromChatGPTAuthTokens(accessToken, account, &plan)
	return &snapshot
}

func catalogIdentity(t *testing.T, provider *ProviderInfo, snapshot *auth.AuthDotJSON) string {
	t.Helper()
	if snapshot == nil {
		return ModelsCatalogIdentity(provider, nil, nil, "")
	}
	headers, err := ResolveProviderAuth(snapshot, *provider)
	if err != nil {
		t.Fatalf("ResolveProviderAuth: %v", err)
	}
	return ModelsCatalogIdentity(provider, snapshot, &headers, "")
}

// Mirrors Rust
// `cache_identity_tracks_account_email_user_plan_and_auth_mode_but_not_token_refresh`.
func TestModelsCatalogIdentityTracksAccountNotTokenRefresh(t *testing.T) {
	provider := CreateOpenAIProvider("")

	initial := chatGPTCatalogAuth(t, "one@example.com", "user", "account", "team", "first")
	key := catalogIdentity(t, &provider, initial)
	if key == "" {
		t.Fatal("identity is empty for a scoped ChatGPT provider")
	}

	refreshed := chatGPTCatalogAuth(t, "one@example.com", "user", "account", "team", "second")
	if got := catalogIdentity(t, &provider, refreshed); got != key {
		t.Fatalf("identity changed across a token refresh: %q != %q", got, key)
	}

	apiKey := auth.FromAPIKey("api-key")
	different := map[string]*auth.AuthDotJSON{
		"email":   chatGPTCatalogAuth(t, "two@example.com", "user", "account", "team", "first"),
		"user":    chatGPTCatalogAuth(t, "one@example.com", "other", "account", "team", "first"),
		"account": chatGPTCatalogAuth(t, "one@example.com", "user", "other", "team", "first"),
		"plan":    chatGPTCatalogAuth(t, "one@example.com", "user", "account", "plus", "first"),
		"api-key": &apiKey,
		"no-auth": nil,
	}
	for name, snapshot := range different {
		if got := catalogIdentity(t, &provider, snapshot); got == key {
			t.Fatalf("%s: identity matched the ChatGPT account key", name)
		}
	}
}

// Mirrors Rust
// `cache_identity_tracks_provider_routing_and_effective_api_credentials`.
func TestModelsCatalogIdentityTracksProviderRoutingAndCredentials(t *testing.T) {
	provider := CreateOpenAIProvider("https://one.example/v1")
	first := auth.FromAPIKey("first-key")
	key := catalogIdentity(t, &provider, &first)

	second := auth.FromAPIKey("second-key")
	if got := catalogIdentity(t, &provider, &second); got == key {
		t.Fatal("identity matched across two API keys")
	}

	otherBase := provider
	otherBase.BaseURL = "https://two.example/v1"
	if got := catalogIdentity(t, &otherBase, &first); got == key {
		t.Fatal("identity matched across two base URLs")
	}

	bearer := provider
	bearer.ExperimentalBearerToken = "provider-key"
	if got := catalogIdentity(t, &bearer, &first); got == key {
		t.Fatal("identity matched with an explicit provider bearer token")
	}

	headers := provider
	headers.HTTPHeaders = map[string]string{"openai-project": "another-project"}
	if got := catalogIdentity(t, &headers, &first); got == key {
		t.Fatal("identity matched across provider headers")
	}
}

// Mirrors Rust
// `cache_identity_distinguishes_auth_requirements_and_unknown_plans`.
func TestModelsCatalogIdentityDistinguishesAuthRequirementsAndUnknownPlans(t *testing.T) {
	provider := CreateOpenAIProvider("https://example.com/v1")

	first := chatGPTCatalogAuth(t, "one@example.com", "user", "account", "future-plan-one", "first")
	second := chatGPTCatalogAuth(t, "one@example.com", "user", "account", "future-plan-two", "first")
	key := catalogIdentity(t, &provider, first)
	if got := catalogIdentity(t, &provider, second); got == key {
		t.Fatal("identity matched across two unknown plans")
	}

	noAuthRequired := provider
	noAuthRequired.RequiresOpenAIAuth = false
	if got := catalogIdentity(t, &noAuthRequired, first); got == key {
		t.Fatal("identity matched across requires_openai_auth values")
	}
}

// A provider whose env-key credential is configured but missing cannot compute a
// stable identity; Rust fails `api_key()?` and the endpoint disables cache reuse.
func TestModelsCatalogIdentityUnavailableWhenEnvKeyIsMissing(t *testing.T) {
	provider := CreateOpenAIProvider("https://example.com/v1")
	provider.EnvKey = "CODEX_TEST_MISSING_API_KEY"
	t.Setenv("CODEX_TEST_MISSING_API_KEY", "")

	snapshot := auth.FromAPIKey("api-key")
	// Resolving auth also fails for a missing env key, so the identity is
	// computed directly: it must not fall back to an unscoped digest.
	if got := ModelsCatalogIdentity(&provider, &snapshot, nil, ""); got != "" {
		t.Fatalf("identity = %q, want empty when the provider env key is missing", got)
	}
}

// The managed residency requirement is enforced on the provider before the
// digest, so it scopes the catalog (Rust enforce_managed_residency).
func TestModelsCatalogIdentityTracksManagedResidency(t *testing.T) {
	provider := CreateOpenAIProvider("https://example.com/v1")
	snapshot := auth.FromAPIKey("api-key")
	headers, err := ResolveProviderAuth(&snapshot, provider)
	if err != nil {
		t.Fatalf("ResolveProviderAuth: %v", err)
	}
	without := ModelsCatalogIdentity(&provider, &snapshot, &headers, "")
	withUS := ModelsCatalogIdentity(&provider, &snapshot, &headers, "us")
	if without == "" || withUS == "" {
		t.Fatalf("identities = %q / %q, want both scoped", without, withUS)
	}
	if without == withUS {
		t.Fatal("identity did not change with the managed residency requirement")
	}
	if padded := ModelsCatalogIdentity(&provider, &snapshot, &headers, "  us  "); padded != withUS {
		t.Fatalf("identity = %q, want the trimmed residency to match %q", padded, withUS)
	}
}

// ChatGPT access tokens are excluded from the digest, so the raw access token
// itself must not leak into (or churn) the identity.
func TestModelsCatalogIdentityExcludesChatGPTAccessToken(t *testing.T) {
	provider := CreateOpenAIProvider("")
	first := chatGPTCatalogAuth(t, "one@example.com", "user", "account", "team", "aaaa")
	second := chatGPTCatalogAuth(t, "one@example.com", "user", "account", "team", "bbbb")
	if first.Tokens["access_token"] == second.Tokens["access_token"] {
		t.Fatal("test setup: access tokens must differ")
	}
	if got, want := catalogIdentity(t, &provider, first), catalogIdentity(t, &provider, second); got != want {
		t.Fatalf("identity churned on token rotation: %q != %q", got, want)
	}
}
