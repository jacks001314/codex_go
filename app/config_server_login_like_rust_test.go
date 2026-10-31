package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"codex_go/config"
)

// Mirrors Rust managed_config_auth_manager: only a server that declares OAuth
// endpoints owns credentials.
func TestConfigServerAuthManagerRequiresOAuthLikeRust(t *testing.T) {
	home := t.TempDir()
	store := newConfigServerTestStore()
	if manager := newConfigServerAuthManager(home, nil, nil, store); manager != nil {
		t.Fatal("a nil server must not own credentials")
	}
	anonymous := &config.ConfigServer{BaseURL: "https://config.example.com/settings"}
	if manager := newConfigServerAuthManager(home, anonymous, nil, store); manager != nil {
		t.Fatal("a server without OAuth must not own credentials")
	}
	withOAuth := &config.ConfigServer{BaseURL: "https://config.example.com/settings", OAuth: &config.ConfigServerOAuth{
		AuthorizationURL: "https://config.example.com/authorize",
		TokenURL:         "https://config.example.com/token",
		ClientID:         "company-client",
	}}
	if manager := newConfigServerAuthManager(home, withOAuth, nil, store); manager == nil {
		t.Fatal("an OAuth server must own credentials")
	}
}

// Mirrors Rust ensure_config_server_login plus logout_config_server: the grant
// is reused while it exists, logout removes it, and the next sign-in happens
// only after it is gone.
func TestConfigServerSignInIsReusedUntilLogoutLikeRust(t *testing.T) {
	var authorizations atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			authorizations.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "account-0", "refresh_token": "refresh-0", "expires_in": 3600, "token_type": "Bearer",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	store := newConfigServerTestStore()
	requirements := &config.ConfigRequirements{ConfigServer: &config.ConfigServer{
		BaseURL: server.URL + "/settings",
		OAuth: &config.ConfigServerOAuth{
			AuthorizationURL: server.URL + "/authorize",
			TokenURL:         server.URL + "/token",
			ClientID:         "company-client",
		},
	}}
	ctx := context.Background()
	signIns := 0
	onURL := func(authorizationURL string) {
		signIns++
		parsed, err := url.Parse(authorizationURL)
		if err != nil {
			t.Errorf("parse authorization url: %v", err)
			return
		}
		go func() {
			response, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=browser-authorization-code&state=" + url.QueryEscape(parsed.Query().Get("state")))
			if err == nil {
				_, _ = io.ReadAll(response.Body)
				_ = response.Body.Close()
			}
		}()
	}
	if err := ensureConfigServerLoginWith(ctx, home, requirements, server.Client(), store, onURL); err != nil {
		t.Fatalf("ensureConfigServerLoginWith() error = %v", err)
	}
	if signIns != 1 || authorizations.Load() != 1 {
		t.Fatalf("sign-ins = %d, token requests = %d, want one of each", signIns, authorizations.Load())
	}
	if err := ensureConfigServerLoginWith(ctx, home, requirements, server.Client(), store, onURL); err != nil {
		t.Fatalf("second ensureConfigServerLoginWith() error = %v", err)
	}
	if signIns != 1 || authorizations.Load() != 1 {
		t.Fatalf("an existing grant must be reused, sign-ins = %d, token requests = %d", signIns, authorizations.Load())
	}
	removed, err := logoutConfigServerWith(ctx, home, requirements, server.Client(), store)
	if err != nil || !removed {
		t.Fatalf("logoutConfigServerWith() = %v, %v, want removed", removed, err)
	}
	removedAgain, err := logoutConfigServerWith(ctx, home, requirements, server.Client(), store)
	if err != nil || removedAgain {
		t.Fatalf("second logout = %v, %v, want no stored grant", removedAgain, err)
	}
	if err := ensureConfigServerLoginWith(ctx, home, requirements, server.Client(), store, onURL); err != nil {
		t.Fatalf("ensureConfigServerLoginWith() after logout error = %v", err)
	}
	if signIns != 2 {
		t.Fatalf("sign-ins after logout = %d, want 2", signIns)
	}
}

// A configuration without a config server keeps the anonymous behaviour.
func TestConfigServerLoginWithoutServerIsNoopLikeRust(t *testing.T) {
	if err := ensureConfigServerLoginWith(context.Background(), t.TempDir(), nil, nil, newConfigServerTestStore(), func(string) {
		t.Fatal("no sign-in is expected without a config server")
	}); err != nil {
		t.Fatalf("ensureConfigServerLoginWith() error = %v", err)
	}
	if removed, err := logoutConfigServerWith(context.Background(), t.TempDir(), nil, nil, newConfigServerTestStore()); err != nil || removed {
		t.Fatalf("logoutConfigServerWith() = %v, %v", removed, err)
	}
}
