package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust oauth_callback_input_tests (#44629): the pasted redirect URL must
// match this login's redirect URI before state validation and code exchange.
func TestParseMCPOAuthCallbackURLValidatesRedirect(t *testing.T) {
	redirect := "http://127.0.0.1:1455/callback/abc123?workspace=ws-1"
	callback, err := ParseMCPOAuthCallbackURL(
		"http://127.0.0.1:1455/callback/abc123?workspace=ws-1&code=code-1&state=state-1",
		redirect, "/callback/abc123",
	)
	if err != nil || callback == nil || callback.Code != "code-1" || callback.State != "state-1" {
		t.Fatalf("valid callback = %#v, %v", callback, err)
	}

	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{name: "oversized", url: "http://127.0.0.1:1455/callback/abc123?workspace=ws-1&state=" + strings.Repeat("a", 64*1024), want: "64 KiB"},
		{name: "fragment", url: "http://127.0.0.1:1455/callback/abc123?workspace=ws-1&code=c&state=s#frag", want: "fragment"},
		{name: "credentials", url: "http://user:pass@127.0.0.1:1455/callback/abc123?workspace=ws-1&code=c&state=s", want: "credentials"},
		{name: "missing redirect param", url: "http://127.0.0.1:1455/callback/abc123?code=c&state=s", want: "does not match"},
		{name: "changed redirect param", url: "http://127.0.0.1:1455/callback/abc123?workspace=other&code=c&state=s", want: "does not match"},
		{name: "duplicate redirect param name", url: "http://127.0.0.1:1455/callback/abc123?workspace=ws-1&workspace=other&code=c&state=s", want: "changes this login"},
		{name: "wrong path", url: "http://127.0.0.1:1455/other?workspace=ws-1&code=c&state=s", want: "callback path"},
		{name: "missing response params", url: "http://127.0.0.1:1455/callback/abc123?workspace=ws-1", want: "invalid MCP OAuth callback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseMCPOAuthCallbackURL(tc.url, redirect, "/callback/abc123"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}

	// Provider errors still surface through the pasted URL.
	if _, err := ParseMCPOAuthCallbackURL(
		"http://127.0.0.1:1455/callback/abc123?workspace=ws-1&error=invalid_scope&error_description=scope%20rejected",
		redirect, "/callback/abc123",
	); err == nil || !strings.Contains(err.Error(), "scope rejected") {
		t.Fatalf("provider error = %v", err)
	}
}

// Mirrors Rust's manual callback-input login: a pasted redirect URL completes
// the login and stores the exchanged tokens.
func TestOAuthLoginServerCompletesWithCallbackURL(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"access_token":  "access-pasted",
			"refresh_token": "refresh-pasted",
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

	tokens, err := login.CompleteWithCallbackURL(context.Background(), login.RedirectURL+"?code=code-1&state=state-1")
	if err != nil || tokens == nil || tokens.AccessToken != "access-pasted" {
		t.Fatalf("CompleteWithCallbackURL() = %#v, %v", tokens, err)
	}
	select {
	case result := <-login.Done():
		if result == nil || result.Error != nil || result.Tokens == nil || result.Tokens.AccessToken != "access-pasted" {
			t.Fatalf("login result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for login result")
	}
	loaded, err := store.Load("docs", "https://mcp.example.test/mcp")
	if err != nil || loaded == nil || loaded.AccessToken != "access-pasted" {
		t.Fatalf("stored tokens = %#v, %v", loaded, err)
	}

	// A state mismatch is rejected through the same path.
	second, err := StartOAuthLoginServer(context.Background(), &OAuthLoginServerOptions{
		ServerName:            "docs2",
		ServerURL:             "https://mcp.example.test/mcp",
		ClientID:              "client-1",
		AuthorizationEndpoint: issuer.URL + "/authorize",
		TokenEndpoint:         issuer.URL + "/token",
		State:                 "state-2",
		HTTPClient:            issuer.Client(),
	})
	if err != nil {
		t.Fatalf("StartOAuthLoginServer(second) error = %v", err)
	}
	defer func() {
		_ = second.Cancel(context.Background())
	}()
	if _, err := second.CompleteWithCallbackURL(context.Background(), second.RedirectURL+"?code=code-1&state=wrong"); err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("state mismatch error = %v", err)
	}
}
