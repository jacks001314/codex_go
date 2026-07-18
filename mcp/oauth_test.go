package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOAuthStoreSaveLoadStatusAndDelete(t *testing.T) {
	home := t.TempDir()
	store := NewOAuthStore(home)
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	tokens := &OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       "https://mcp.example.test/mcp",
		ClientID:        "client-1",
		ClientSecret:    " secret-1 ",
		AccessToken:     "access-token",
		RefreshToken:    "refresh-token",
		Scopes:          []string{"read", "write"},
		ExpiresAtMillis: &expiresAt,
	}
	if err := store.Save(tokens); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".credentials.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("stored entries = %d, want 1", len(raw))
	}
	for key, entry := range raw {
		if key == "docs" || entry["access_token"] != "access-token" || entry["client_id"] != "client-1" || entry["client_secret"] != "secret-1" {
			t.Fatalf("stored key=%q entry=%#v", key, entry)
		}
	}

	loaded, err := store.Load("docs", "https://mcp.example.test/mcp")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != "access-token" || loaded.RefreshToken != "refresh-token" || loaded.ClientSecret != "secret-1" || len(loaded.Scopes) != 2 {
		t.Fatalf("loaded = %#v", loaded)
	}
	status, err := store.AuthStatus("docs", &ServerConfig{URL: "https://mcp.example.test/mcp", OAuthClientID: "client-1"})
	if err != nil {
		t.Fatalf("AuthStatus() error = %v", err)
	}
	if status != MCPAuthOAuth {
		t.Fatalf("AuthStatus() = %s, want %s", status, MCPAuthOAuth)
	}

	removed, err := store.Delete("docs", "https://mcp.example.test/mcp")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !removed {
		t.Fatalf("Delete() removed = false")
	}
	if _, err := os.Stat(filepath.Join(home, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf(".credentials.json should be removed, err = %v", err)
	}
}

func TestResolveOAuthScopesAndRetryMatchRust(t *testing.T) {
	tests := []struct {
		name               string
		explicit           []string
		explicitConfigured bool
		configured         []string
		configuredPresent  bool
		discovered         []string
		want               ResolvedOAuthScopes
	}{
		{name: "explicit", explicit: []string{"explicit"}, explicitConfigured: true, configured: []string{"configured"}, configuredPresent: true, discovered: []string{"discovered"}, want: ResolvedOAuthScopes{Scopes: []string{"explicit"}, Source: OAuthScopesExplicit}},
		{name: "configured", configured: []string{"configured"}, configuredPresent: true, discovered: []string{"discovered"}, want: ResolvedOAuthScopes{Scopes: []string{"configured"}, Source: OAuthScopesConfigured}},
		{name: "configured empty", configured: []string{}, configuredPresent: true, discovered: []string{"discovered"}, want: ResolvedOAuthScopes{Scopes: []string{}, Source: OAuthScopesConfigured}},
		{name: "discovered", discovered: []string{"read", " read ", "write"}, want: ResolvedOAuthScopes{Scopes: []string{"read", "write"}, Source: OAuthScopesDiscovered}},
		{name: "empty", want: ResolvedOAuthScopes{Scopes: []string{}, Source: OAuthScopesEmpty}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveOAuthScopes(tc.explicit, tc.explicitConfigured, tc.configured, tc.configuredPresent, tc.discovered)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ResolveOAuthScopes() = %#v, want %#v", got, tc.want)
			}
		})
	}
	providerError := &OAuthProviderError{Code: "invalid_scope", Description: "scope rejected"}
	if !ShouldRetryOAuthWithoutScopes(ResolvedOAuthScopes{Scopes: []string{"read"}, Source: OAuthScopesDiscovered}, providerError) {
		t.Fatal("discovered provider error should retry without scopes")
	}
	if ShouldRetryOAuthWithoutScopes(ResolvedOAuthScopes{Scopes: []string{"read"}, Source: OAuthScopesConfigured}, providerError) {
		t.Fatal("configured provider error should not retry without scopes")
	}
	if ShouldRetryOAuthWithoutScopes(ResolvedOAuthScopes{Scopes: []string{"read"}, Source: OAuthScopesDiscovered}, errors.New("callback timeout")) {
		t.Fatal("non-provider error should not retry without scopes")
	}
}

func TestOAuthStoreAuthStatusVariants(t *testing.T) {
	store := NewOAuthStore(t.TempDir())
	if status, err := store.AuthStatus("docs", &ServerConfig{URL: "https://mcp.example.test/mcp", BearerTokenEnvVar: "TOKEN"}); err != nil || status != MCPAuthBearerToken {
		t.Fatalf("bearer status = %s, err = %v", status, err)
	}
	if status, err := store.AuthStatus("docs", &ServerConfig{URL: "https://mcp.example.test/mcp", OAuthClientID: "client-1"}); err != nil || status != MCPAuthNotLoggedIn {
		t.Fatalf("not logged in status = %s, err = %v", status, err)
	}
	expired := time.Now().Add(-time.Hour).UnixMilli()
	if err := store.Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       "https://mcp.example.test/mcp",
		ClientID:        "client-1",
		AccessToken:     "expired-access",
		ExpiresAtMillis: &expired,
	}); err != nil {
		t.Fatalf("Save(expired) error = %v", err)
	}
	if status, err := store.AuthStatus("docs", &ServerConfig{URL: "https://mcp.example.test/mcp", OAuthClientID: "client-1"}); err != nil || status != MCPAuthNotLoggedIn {
		t.Fatalf("expired status = %s, err = %v", status, err)
	}
	if err := store.Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       "https://mcp.example.test/mcp",
		ClientID:        "client-1",
		AccessToken:     "expired-access",
		RefreshToken:    "refresh-token",
		ExpiresAtMillis: &expired,
	}); err != nil {
		t.Fatalf("Save(refresh) error = %v", err)
	}
	if status, err := store.AuthStatus("docs", &ServerConfig{URL: "https://mcp.example.test/mcp", OAuthClientID: "client-1"}); err != nil || status != MCPAuthOAuth {
		t.Fatalf("refresh status = %s, err = %v", status, err)
	}
}

func TestDiscoverStreamableHTTPOAuthReturnsNormalizedScopes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server/mcp" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"authorization_endpoint": "https://example.com/authorize",
			"token_endpoint":         "https://example.com/token",
			"registration_endpoint":  "https://example.com/register",
			"scopes_supported":       []string{"profile", " email ", "profile", "", "   "},
		})
	}))
	defer server.Close()

	discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), server.URL+"/mcp", server.Client())
	if err != nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() error = %v", err)
	}
	if discovery == nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() returned nil discovery")
	}
	if got := strings.Join(discovery.ScopesSupported, ","); got != "profile,email" {
		t.Fatalf("ScopesSupported = %q, want profile,email", got)
	}
	if discovery.RegistrationEndpoint != "https://example.com/register" {
		t.Fatalf("RegistrationEndpoint = %q", discovery.RegistrationEndpoint)
	}
}

func TestDiscoverStreamableHTTPOAuthFollowsProtectedResourceMetadata(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server/mcp" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"authorization_endpoint": "https://example.com/authorize",
			"token_endpoint":         "https://example.com/token",
			"scopes_supported":       []string{"read", " write ", "read"},
		})
	}))
	defer authServer.Close()

	var resourceServer *httptest.Server
	resourceServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+resourceServer.URL+`/oauth-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/oauth-resource":
			writeJSON(t, w, map[string]any{
				"resource":              resourceServer.URL + "/mcp",
				"authorization_servers": []string{authServer.URL + "/mcp"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer resourceServer.Close()

	discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), resourceServer.URL+"/mcp", resourceServer.Client())
	if err != nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() error = %v", err)
	}
	if discovery == nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() returned nil discovery")
	}
	if got := strings.Join(discovery.ScopesSupported, ","); got != "read,write" {
		t.Fatalf("ScopesSupported = %q, want read,write", got)
	}
	if discovery.Resource != resourceServer.URL+"/mcp" {
		t.Fatalf("Resource = %q, want %q", discovery.Resource, resourceServer.URL+"/mcp")
	}
}

func TestDiscoverStreamableHTTPOAuthIgnoresEmptyScopes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server/mcp" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"authorization_endpoint": "https://example.com/authorize",
			"token_endpoint":         "https://example.com/token",
			"scopes_supported":       []string{"", "   "},
		})
	}))
	defer server.Close()

	discovery, err := DiscoverStreamableHTTPOAuth(context.Background(), server.URL+"/mcp", server.Client())
	if err != nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() error = %v", err)
	}
	if discovery == nil {
		t.Fatalf("DiscoverStreamableHTTPOAuth() returned nil discovery")
	}
	if discovery.ScopesSupported != nil {
		t.Fatalf("ScopesSupported = %#v, want nil", discovery.ScopesSupported)
	}
}

func TestSupportsStreamableHTTPOAuthLoginDoesNotRequireScopesSupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server/mcp" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"authorization_endpoint": "https://example.com/authorize",
			"token_endpoint":         "https://example.com/token",
		})
	}))
	defer server.Close()

	supported, err := SupportsStreamableHTTPOAuthLogin(context.Background(), server.URL+"/mcp", server.Client())
	if err != nil {
		t.Fatalf("SupportsStreamableHTTPOAuthLogin() error = %v", err)
	}
	if !supported {
		t.Fatalf("SupportsStreamableHTTPOAuthLogin() = false, want true")
	}
}

func TestMCPOAuthLoginUsesDiscoveredAuthorizationEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server/mcp" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"authorization_endpoint": "https://issuer.example.test/authorize",
			"token_endpoint":         "https://issuer.example.test/token",
		})
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{
			URL:           server.URL + "/mcp",
			OAuthClientID: "client-1",
			Enabled:       true,
		}},
	}})
	timeout := uint64(1)
	response, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", Scopes: []string{"read", "read", " write "}, TimeoutSecs: &timeout})
	if err != nil {
		t.Fatalf("OauthLogin() error = %v", err)
	}
	for _, want := range []string{"https://issuer.example.test/authorize", "client_id=client-1", "scope=read+write"} {
		if !strings.Contains(response.AuthorizationURL, want) {
			t.Fatalf("AuthorizationURL missing %q: %s", want, response.AuthorizationURL)
		}
	}
}

func TestOAuthTokenClientExchangeAuthorizationCode(t *testing.T) {
	var sawTokenRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		sawTokenRequest = true
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		wants := map[string]string{
			"grant_type":    "authorization_code",
			"code":          "auth-code",
			"redirect_uri":  "http://127.0.0.1:1234/callback",
			"client_id":     "client-1",
			"client_secret": "secret-1",
			"code_verifier": "verifier-1",
		}
		for key, want := range wants {
			if got := r.Form.Get(key); got != want {
				t.Fatalf("form[%s] = %q, want %q", key, got, want)
			}
		}
		writeJSON(t, w, map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         "read write",
		})
	}))
	defer server.Close()

	tokens, err := NewOAuthTokenClient(server.Client()).ExchangeAuthorizationCode(context.Background(), &OAuthCodeExchangeOptions{
		ServerName:            "docs",
		ServerURL:             "https://mcp.example.test/mcp",
		ClientID:              "client-1",
		ClientSecret:          " secret-1 ",
		AuthorizationEndpoint: server.URL + "/authorize",
		TokenEndpoint:         server.URL + "/token",
		RedirectURL:           "http://127.0.0.1:1234/callback",
		Code:                  "auth-code",
		CodeVerifier:          "verifier-1",
		Scopes:                []string{"read"},
	})
	if err != nil {
		t.Fatalf("ExchangeAuthorizationCode() error = %v", err)
	}
	if !sawTokenRequest {
		t.Fatalf("token endpoint was not called")
	}
	if tokens == nil || tokens.ServerName != "docs" || tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" || tokens.ClientSecret != "secret-1" {
		t.Fatalf("tokens = %#v", tokens)
	}
	if got := strings.Join(tokens.Scopes, ","); got != "read,write" {
		t.Fatalf("scopes = %q, want read,write", got)
	}
	if tokens.ExpiresAtMillis == nil || *tokens.ExpiresAtMillis <= time.Now().UnixMilli() {
		t.Fatalf("expires_at = %#v, want future timestamp", tokens.ExpiresAtMillis)
	}
}

func TestOAuthTokenClientRefreshToken(t *testing.T) {
	var sawRefresh bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		sawRefresh = true
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		wants := map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "refresh-old",
			"client_id":     "client-1",
			"client_secret": "secret-1",
		}
		for key, want := range wants {
			if got := r.Form.Get(key); got != want {
				t.Fatalf("form[%s] = %q, want %q", key, got, want)
			}
		}
		writeJSON(t, w, map[string]any{
			"access_token": "access-new",
			"token_type":   "Bearer",
			"expires_in":   7200,
		})
	}))
	defer server.Close()

	expired := time.Now().Add(-time.Hour).UnixMilli()
	tokens, err := NewOAuthTokenClient(server.Client()).RefreshToken(context.Background(), &OAuthRefreshOptions{
		ServerName:      "docs",
		ServerURL:       "https://mcp.example.test/mcp",
		ClientID:        "client-1",
		ClientSecret:    " secret-1 ",
		TokenEndpoint:   server.URL + "/token",
		AccessToken:     "access-old",
		RefreshToken:    "refresh-old",
		Scopes:          []string{"read", " read ", "write"},
		ExpiresAtMillis: &expired,
	})
	if err != nil {
		t.Fatalf("RefreshToken() error = %v", err)
	}
	if !sawRefresh {
		t.Fatalf("token endpoint was not called")
	}
	if tokens == nil || tokens.AccessToken != "access-new" || tokens.RefreshToken != "refresh-old" || tokens.ClientSecret != "secret-1" {
		t.Fatalf("tokens = %#v", tokens)
	}
	if got := strings.Join(tokens.Scopes, ","); got != "read,write" {
		t.Fatalf("scopes = %q, want read,write", got)
	}
}

func TestOAuthLoginSessionBuildsPKCEAuthorizationURL(t *testing.T) {
	session, err := NewOAuthLoginSession(&OAuthLoginSessionOptions{
		ServerURL:             "https://mcp.example.test/mcp",
		ClientID:              "client-1",
		AuthorizationEndpoint: "https://issuer.example.test/authorize",
		TokenEndpoint:         "https://issuer.example.test/token",
		RedirectURL:           "http://127.0.0.1:1234/callback/abc123",
		Resource:              "https://mcp.example.test/mcp",
		Scopes:                []string{"read", " write ", "read"},
		State:                 "state-1",
	})
	if err != nil {
		t.Fatalf("NewOAuthLoginSession() error = %v", err)
	}
	parsed, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatalf("Parse authorization URL error = %v", err)
	}
	query := parsed.Query()
	if parsed.String() == "" || parsed.Scheme != "https" || parsed.Host != "issuer.example.test" || parsed.Path != "/authorize" {
		t.Fatalf("authorization URL = %s", session.AuthorizationURL)
	}
	wants := map[string]string{
		"response_type":         "code",
		"client_id":             "client-1",
		"redirect_uri":          "http://127.0.0.1:1234/callback/abc123",
		"resource":              "https://mcp.example.test/mcp",
		"scope":                 "read write",
		"state":                 "state-1",
		"code_challenge_method": "S256",
	}
	for key, want := range wants {
		if got := query.Get(key); got != want {
			t.Fatalf("query[%s] = %q, want %q in %s", key, got, want, session.AuthorizationURL)
		}
	}
	if query.Get("code_challenge") == "" || session.CodeVerifier == "" || session.CallbackPath != "/callback/abc123" {
		t.Fatalf("session = %#v query=%#v", session, query)
	}
}

func TestOAuthClientRegistrarRegistersPublicClient(t *testing.T) {
	var registrationPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/register" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("registration request method=%s content-type=%s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&registrationPayload); err != nil {
			t.Fatalf("Decode registration payload error = %v", err)
		}
		writeJSON(t, w, map[string]any{
			"client_id":                  "dynamic-client",
			"client_secret":              "dynamic-secret",
			"client_id_issued_at":        123,
			"token_endpoint_auth_method": "none",
		})
	}))
	defer server.Close()

	registered, err := NewOAuthClientRegistrar(server.Client()).Register(context.Background(), &OAuthClientRegistrationOptions{
		RegistrationEndpoint: server.URL + "/register",
		ClientName:           "Codex Go",
		RedirectURIs:         []string{"http://127.0.0.1:1234/callback"},
		Scopes:               []string{"read", " write ", "read"},
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if registered.ClientID != "dynamic-client" || registered.ClientSecret != "dynamic-secret" || registered.ClientIDIssuedAt == nil || *registered.ClientIDIssuedAt != 123 {
		t.Fatalf("registered = %#v", registered)
	}
	redirects, ok := registrationPayload["redirect_uris"].([]any)
	if !ok || len(redirects) != 1 || redirects[0] != "http://127.0.0.1:1234/callback" {
		t.Fatalf("redirect_uris = %#v", registrationPayload["redirect_uris"])
	}
	if registrationPayload["client_name"] != "Codex Go" || registrationPayload["scope"] != "read write" || registrationPayload["token_endpoint_auth_method"] != "none" {
		t.Fatalf("registration payload = %#v", registrationPayload)
	}
}

func TestOAuthLoginSessionWithClientRegistration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/register" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"client_id":     "dynamic-client",
			"client_secret": "dynamic-secret",
		})
	}))
	defer server.Close()

	session, err := NewOAuthLoginSessionWithClientRegistration(context.Background(), &OAuthLoginSessionOptions{
		ServerURL:             "https://mcp.example.test/mcp",
		RegistrationEndpoint:  server.URL + "/register",
		AuthorizationEndpoint: "https://issuer.example.test/authorize",
		TokenEndpoint:         "https://issuer.example.test/token",
		RedirectURL:           "http://127.0.0.1:1234/callback/abc123",
		Scopes:                []string{"read"},
		State:                 "state-1",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOAuthLoginSessionWithClientRegistration() error = %v", err)
	}
	if session.ClientID != "dynamic-client" || session.ClientSecret != "dynamic-secret" {
		t.Fatalf("session client = %#v", session)
	}
	parsed, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatalf("Parse authorization URL error = %v", err)
	}
	if parsed.Query().Get("client_id") != "dynamic-client" {
		t.Fatalf("authorization URL = %s", session.AuthorizationURL)
	}
}

func TestOAuthLoginSessionCompleteCallbackExchangesCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "code-1" || r.Form.Get("code_verifier") == "" {
			t.Fatalf("exchange form = %#v", r.Form)
		}
		writeJSON(t, w, map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	session, err := NewOAuthLoginSession(&OAuthLoginSessionOptions{
		ServerURL:             "https://mcp.example.test/mcp",
		ClientID:              "client-1",
		AuthorizationEndpoint: server.URL + "/authorize",
		TokenEndpoint:         server.URL + "/token",
		RedirectURL:           "http://127.0.0.1:1234/callback",
		State:                 "state-1",
	})
	if err != nil {
		t.Fatalf("NewOAuthLoginSession() error = %v", err)
	}
	tokens, err := session.CompleteCallback(context.Background(), "/callback?code=code-1&state=state-1", NewOAuthTokenClient(server.Client()), "docs")
	if err != nil {
		t.Fatalf("CompleteCallback() error = %v", err)
	}
	if tokens == nil || tokens.ServerName != "docs" || tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" {
		t.Fatalf("tokens = %#v", tokens)
	}
	if _, err := session.CompleteCallback(context.Background(), "/callback?code=code-1&state=wrong", NewOAuthTokenClient(server.Client()), "docs"); err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("CompleteCallback() state mismatch error = %v", err)
	}
}

func TestStartOAuthLoginServerCompletesAndStoresTokens(t *testing.T) {
	var redirectURIWant string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "code-1" || r.Form.Get("redirect_uri") != redirectURIWant {
			t.Fatalf("exchange form = %#v want redirect %q", r.Form, redirectURIWant)
		}
		writeJSON(t, w, map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"expires_in":    3600,
		})
	}))
	defer issuer.Close()

	store := NewOAuthStore(t.TempDir())
	login, err := StartOAuthLoginServer(context.Background(), &OAuthLoginServerOptions{
		ServerName:            "docs",
		ServerURL:             "https://mcp.example.test/mcp",
		ClientID:              "client-1",
		AuthorizationEndpoint: issuer.URL + "/authorize",
		TokenEndpoint:         issuer.URL + "/token",
		Scopes:                []string{"read"},
		State:                 "state-1",
		Store:                 store,
		HTTPClient:            issuer.Client(),
	})
	if err != nil {
		t.Fatalf("StartOAuthLoginServer() error = %v", err)
	}
	defer func() {
		_ = login.Cancel(context.Background())
	}()
	redirectURIWant = login.RedirectURL
	if !strings.Contains(login.AuthorizationURL, "redirect_uri="+url.QueryEscape(login.RedirectURL)) {
		t.Fatalf("AuthorizationURL = %s redirect=%s", login.AuthorizationURL, login.RedirectURL)
	}

	response, err := http.Get(login.CallbackURL + "?code=code-1&state=state-1")
	if err != nil {
		t.Fatalf("callback GET error = %v", err)
	}
	_ = response.Body.Close()

	select {
	case result := <-login.Done():
		if result == nil || result.Error != nil || result.Tokens == nil || result.Tokens.AccessToken != "access-1" {
			t.Fatalf("login result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for login result")
	}
	loaded, err := store.Load("docs", "https://mcp.example.test/mcp")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != "access-1" || loaded.RefreshToken != "refresh-1" {
		t.Fatalf("stored tokens = %#v", loaded)
	}
}

func TestMCPServiceOauthLoginStartsCallbackServer(t *testing.T) {
	var redirectURISeen string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			redirectURISeen = r.Form.Get("redirect_uri")
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "code-1" || r.Form.Get("client_id") != "client-1" {
				t.Fatalf("exchange form = %#v", r.Form)
			}
			writeJSON(t, w, map[string]any{
				"access_token":  "access-service",
				"refresh_token": "refresh-service",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()

	home := t.TempDir()
	completions := make(chan *MCPOAuthLoginCompletion, 1)
	service := NewMCPService(&RuntimeConfig{
		CodexHome: home,
		Servers: map[string]ServerRegistration{
			"docs": {Config: ServerConfig{URL: issuer.URL + "/mcp", OAuthClientID: "client-1", Enabled: true}},
		},
	})
	service.SetOAuthLoginCompletionHandler(MCPOAuthLoginCompletionHandlerFunc(func(ctx context.Context, completion *MCPOAuthLoginCompletion) {
		completions <- completion
	}))
	threadID := "thread-oauth"
	login, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", ThreadID: &threadID, Scopes: []string{"read"}})
	if err != nil {
		t.Fatalf("OauthLogin() error = %v", err)
	}
	authorizationURL, err := url.Parse(login.AuthorizationURL)
	if err != nil {
		t.Fatalf("Parse authorization URL error = %v", err)
	}
	query := authorizationURL.Query()
	redirectURI := query.Get("redirect_uri")
	state := query.Get("state")
	if redirectURI == "" || state == "" || query.Get("client_id") != "client-1" {
		t.Fatalf("authorization URL = %s", login.AuthorizationURL)
	}
	response, err := http.Get(redirectURI + "?code=code-1&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback GET error = %v", err)
	}
	_ = response.Body.Close()
	if redirectURISeen != redirectURI {
		t.Fatalf("redirect URI seen = %q want %q", redirectURISeen, redirectURI)
	}
	loaded, err := NewOAuthStore(home).Load("docs", issuer.URL+"/mcp")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != "access-service" || loaded.RefreshToken != "refresh-service" {
		t.Fatalf("stored tokens = %#v", loaded)
	}
	select {
	case completion := <-completions:
		if completion.Name != "docs" || completion.ThreadID != "thread-oauth" || !completion.Success || completion.Error != "" {
			t.Fatalf("completion = %#v", completion)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for OAuth completion")
	}
}

func TestMCPServiceOauthLoginUsesDynamicClientRegistration(t *testing.T) {
	var registeredRedirectURI string
	var exchangeClientID string
	var exchangeClientSecret string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
				"registration_endpoint":  "http://" + r.Host + "/register",
			})
		case "/register":
			var raw map[string]any
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				t.Fatalf("Decode(register) error = %v", err)
			}
			redirectURIs, _ := raw["redirect_uris"].([]any)
			if len(redirectURIs) != 1 {
				t.Fatalf("registration redirect_uris = %#v", raw["redirect_uris"])
			}
			registeredRedirectURI, _ = redirectURIs[0].(string)
			if raw["client_name"] != "Codex" || raw["token_endpoint_auth_method"] != "none" {
				t.Fatalf("registration payload = %#v", raw)
			}
			writeJSON(t, w, map[string]any{
				"client_id":     "dynamic-client",
				"client_secret": "dynamic-secret",
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			exchangeClientID = r.Form.Get("client_id")
			exchangeClientSecret = r.Form.Get("client_secret")
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "code-1" {
				t.Fatalf("exchange form = %#v", r.Form)
			}
			writeJSON(t, w, map[string]any{
				"access_token":  "access-dynamic",
				"refresh_token": "refresh-dynamic",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()

	home := t.TempDir()
	service := NewMCPService(&RuntimeConfig{
		CodexHome: home,
		Servers: map[string]ServerRegistration{
			"docs": {Config: ServerConfig{URL: issuer.URL + "/mcp", Enabled: true}},
		},
	})
	login, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", Scopes: []string{"read"}})
	if err != nil {
		t.Fatalf("OauthLogin() error = %v", err)
	}
	authorizationURL, err := url.Parse(login.AuthorizationURL)
	if err != nil {
		t.Fatalf("Parse authorization URL error = %v", err)
	}
	query := authorizationURL.Query()
	redirectURI := query.Get("redirect_uri")
	state := query.Get("state")
	if redirectURI == "" || state == "" || query.Get("client_id") != "dynamic-client" {
		t.Fatalf("authorization URL = %s", login.AuthorizationURL)
	}
	if registeredRedirectURI != redirectURI {
		t.Fatalf("registered redirect URI = %q want %q", registeredRedirectURI, redirectURI)
	}
	response, err := http.Get(redirectURI + "?code=code-1&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback GET error = %v", err)
	}
	_ = response.Body.Close()
	if exchangeClientID != "dynamic-client" || exchangeClientSecret != "dynamic-secret" {
		t.Fatalf("exchange client = %q secret = %q", exchangeClientID, exchangeClientSecret)
	}
	loaded, err := NewOAuthStore(home).Load("docs", issuer.URL+"/mcp")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.ClientID != "dynamic-client" || loaded.ClientSecret != "dynamic-secret" || loaded.AccessToken != "access-dynamic" || loaded.RefreshToken != "refresh-dynamic" {
		t.Fatalf("stored tokens = %#v", loaded)
	}
}

func TestMCPServiceOauthCancelCompletesActiveLogin(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()

	completions := make(chan *MCPOAuthLoginCompletion, 1)
	service := NewMCPService(&RuntimeConfig{
		CodexHome: t.TempDir(),
		Servers: map[string]ServerRegistration{
			"docs": {Config: ServerConfig{URL: issuer.URL + "/mcp", OAuthClientID: "client-1", Enabled: true}},
		},
	})
	service.SetOAuthLoginCompletionHandler(MCPOAuthLoginCompletionHandlerFunc(func(ctx context.Context, completion *MCPOAuthLoginCompletion) {
		completions <- completion
	}))
	threadID := "thread-oauth"
	if _, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", ThreadID: &threadID}); err != nil {
		t.Fatalf("OauthLogin() error = %v", err)
	}
	if _, err := service.OauthCancel(&MCPServerOauthCancelParams{Name: "docs"}); err != nil {
		t.Fatalf("OauthCancel() error = %v", err)
	}
	select {
	case completion := <-completions:
		if completion.Name != "docs" || completion.ThreadID != "thread-oauth" || completion.Success || !strings.Contains(completion.Error, "cancelled") {
			t.Fatalf("completion = %#v", completion)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for OAuth cancel completion")
	}
}

func TestParseMCPOAuthCallbackAndCallbackHelpers(t *testing.T) {
	callback, err := ParseMCPOAuthCallback("/callback/abc123?code=abc&state=xyz", "/callback/abc123")
	if err != nil {
		t.Fatalf("ParseMCPOAuthCallback() error = %v", err)
	}
	if callback.Code != "abc" || callback.State != "xyz" {
		t.Fatalf("callback = %#v", callback)
	}
	if _, err := ParseMCPOAuthCallback("/callback?code=abc&state=xyz", "/callback/abc123"); err == nil || !strings.Contains(err.Error(), "callback path") {
		t.Fatalf("wrong path error = %v", err)
	}
	var providerError *OAuthProviderError
	if _, err := ParseMCPOAuthCallback("/callback?error=invalid_scope&error_description=scope%20rejected", "/callback"); err == nil || !strings.Contains(err.Error(), "scope rejected") {
		t.Fatalf("provider error = %v", err)
	} else if !errors.As(err, &providerError) || providerError.Code != "invalid_scope" {
		t.Fatalf("provider error type = %#v err=%v", providerError, err)
	}
	callbackID, err := MCPOAuthCallbackID("https://mcp.example.com/mcp?tenant=one")
	if err != nil {
		t.Fatalf("MCPOAuthCallbackID() error = %v", err)
	}
	same, err := MCPOAuthCallbackID("https://mcp.example.com/mcp?tenant=one#unused")
	if err != nil {
		t.Fatalf("MCPOAuthCallbackID(same) error = %v", err)
	}
	different, err := MCPOAuthCallbackID("https://mcp.example.com/sse?tenant=one")
	if err != nil {
		t.Fatalf("MCPOAuthCallbackID(different) error = %v", err)
	}
	if callbackID != same || callbackID == different || len(callbackID) != 12 {
		t.Fatalf("callback IDs = %q same=%q different=%q", callbackID, same, different)
	}
	redirect, err := AppendMCPOAuthCallbackID("https://callbacks.example.com/oauth/callback?provider=github", "abc123")
	if err != nil {
		t.Fatalf("AppendMCPOAuthCallbackID() error = %v", err)
	}
	if redirect != "https://callbacks.example.com/oauth/callback/abc123?provider=github" {
		t.Fatalf("redirect = %q", redirect)
	}
	path, err := MCPOAuthCallbackPath(redirect)
	if err != nil || path != "/oauth/callback/abc123" {
		t.Fatalf("callback path = %q err=%v", path, err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}
