package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func oauthRefreshAdoptionServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"issuer":                 "https://issuer.example.test",
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			// A transient provider failure must not be treated as a definitive
			// rejection.
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTokenHasExpiredUsesRealExpiry(t *testing.T) {
	now := time.Now()
	if tokenHasExpired(nil, now) {
		t.Fatal("nil token must not be expired")
	}
	if tokenHasExpired(&OAuthTokenSet{}, now) {
		t.Fatal("token without expiry must not be expired")
	}
	past := now.Add(-time.Second).UnixMilli()
	if !tokenHasExpired(&OAuthTokenSet{ExpiresAtMillis: &past}, now) {
		t.Fatal("past expiry must be expired")
	}
	future := now.Add(time.Second).UnixMilli()
	if tokenHasExpired(&OAuthTokenSet{ExpiresAtMillis: &future}, now) {
		t.Fatal("future expiry must not be expired")
	}
}

// Rust #43947: after a failed refresh whose access token has expired, adopt a
// valid login completed concurrently instead of prompting again.
func TestHTTPMCPOAuthRefreshAdoptsConcurrentLogin(t *testing.T) {
	server := oauthRefreshAdoptionServer(t)
	defer server.Close()
	home := t.TempDir()
	configURL := server.URL + "/mcp"
	future := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-adopted",
		RefreshToken:    "refresh-new",
		ExpiresAtMillis: &future,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	past := time.Now().Add(-time.Hour).UnixMilli()
	previous := &OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-old",
		ExpiresAtMillis: &past,
	}
	client := &httpClient{config: &ServerConfig{URL: configURL, OAuthClientID: "client-1"}, client: server.Client()}

	refreshed, err := client.refreshOAuthTokenForRequest(previous, "docs", home)
	if err != nil || refreshed == nil {
		t.Fatalf("refreshOAuthTokenForRequest() = %#v, %v; want adopted replacement", refreshed, err)
	}
	if refreshed.AccessToken != "oauth-adopted" {
		t.Fatalf("adopted access token = %q, want oauth-adopted", refreshed.AccessToken)
	}
}

// A failed proactive refresh keeps the original error while the access token is
// still valid.
func TestHTTPMCPOAuthRefreshKeepsErrorWhileTokenValid(t *testing.T) {
	server := oauthRefreshAdoptionServer(t)
	defer server.Close()
	home := t.TempDir()
	configURL := server.URL + "/mcp"
	future := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-adopted",
		RefreshToken:    "refresh-new",
		ExpiresAtMillis: &future,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	previous := &OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-still-valid",
		RefreshToken:    "refresh-old",
		ExpiresAtMillis: &future,
	}
	client := &httpClient{config: &ServerConfig{URL: configURL, OAuthClientID: "client-1"}, client: server.Client()}

	refreshed, err := client.refreshOAuthTokenForRequest(previous, "docs", home)
	if err == nil || refreshed != nil {
		t.Fatalf("refresh while token valid = %#v, %v; want original error", refreshed, err)
	}
}

// A replacement credential bound to a different issuer requires
// reauthentication instead of being adopted.
func TestHTTPMCPOAuthRefreshRejectsReplacementWithNewIssuer(t *testing.T) {
	server := oauthRefreshAdoptionServer(t)
	defer server.Close()
	home := t.TempDir()
	configURL := server.URL + "/mcp"
	future := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://new-issuer.example.test",
		AccessToken:     "oauth-adopted",
		RefreshToken:    "refresh-new",
		ExpiresAtMillis: &future,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	past := time.Now().Add(-time.Hour).UnixMilli()
	previous := &OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-old",
		ExpiresAtMillis: &past,
	}
	client := &httpClient{config: &ServerConfig{URL: configURL, OAuthClientID: "client-1"}, client: server.Client()}

	refreshed, err := client.refreshOAuthTokenForRequest(previous, "docs", home)
	if err == nil || refreshed != nil || !strings.Contains(err.Error(), "reauthentication required") {
		t.Fatalf("issuer-changed replacement = %#v, %v", refreshed, err)
	}
}

// An expired replacement is not adopted; the original refresh error remains.
// Rust #43947: a failed local refresh must not send an unauthenticated tool
// call; the executor converts the typed error into the reconnect signal.
func TestMCPHTTPToolCallRequiresAuthenticationWhenRefreshFails(t *testing.T) {
	var toolRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"issuer":                 "https://issuer.example.test",
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		default:
			toolRequests++
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configURL := server.URL + "/mcp"
	past := time.Now().Add(-time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-old",
		ExpiresAtMillis: &past,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	client := &httpClient{
		config: &ServerConfig{URL: configURL, OAuthServerName: "docs", OAuthClientID: "client-1", CodexHome: home},
		client: server.Client(),
	}

	if _, _, err := client.doRPC(context.Background(), "tools/call", map[string]any{"name": "demo"}, "", true); !errors.Is(err, errMCPAuthenticationRequired) {
		t.Fatalf("doRPC() error = %v, want authentication required", err)
	}
	if toolRequests != 0 {
		t.Fatalf("unauthenticated tool call was sent (%d requests)", toolRequests)
	}

	output, ok := mcpAuthenticationChallengeToolOutput(errMCPAuthenticationRequired)
	if !ok || output == nil || output.Success || output.Data["mcp/www_authenticate"] != `Bearer error="invalid_token"` {
		t.Fatalf("reconnect output = %#v ok=%v", output, ok)
	}
}

func TestHTTPMCPOAuthRefreshDoesNotAdoptExpiredReplacement(t *testing.T) {
	server := oauthRefreshAdoptionServer(t)
	defer server.Close()
	home := t.TempDir()
	configURL := server.URL + "/mcp"
	past := time.Now().Add(-time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-expired",
		RefreshToken:    "refresh-new",
		ExpiresAtMillis: &past,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	previous := &OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-old",
		ExpiresAtMillis: &past,
	}
	client := &httpClient{config: &ServerConfig{URL: configURL, OAuthClientID: "client-1"}, client: server.Client()}

	refreshed, err := client.refreshOAuthTokenForRequest(previous, "docs", home)
	if err == nil || refreshed != nil {
		t.Fatalf("expired replacement = %#v, %v; want original error", refreshed, err)
	}
}
