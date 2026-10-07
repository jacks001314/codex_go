package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// oauthLoginIDIssuer serves the discovery document a streamable-HTTP OAuth
// login needs plus the token endpoint its callback exchanges against.
func oauthLoginIDIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "http://" + r.Host + "/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case "/token":
			writeJSON(t, w, map[string]any{
				"access_token":  "access-login-id",
				"refresh_token": "refresh-login-id",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(issuer.Close)
	return issuer
}

func oauthLoginIDService(t *testing.T, issuer *httptest.Server, completions chan *MCPOAuthLoginCompletion) *MCPService {
	t.Helper()
	service := NewMCPService(&RuntimeConfig{
		CodexHome: t.TempDir(),
		Servers: map[string]ServerRegistration{
			"docs": {Config: ServerConfig{URL: issuer.URL + "/mcp", OAuthClientID: "client-1", Enabled: true}},
		},
	})
	service.SetOAuthLoginCompletionHandler(MCPOAuthLoginCompletionHandlerFunc(func(_ context.Context, completion *MCPOAuthLoginCompletion) {
		completions <- completion
	}))
	return service
}

// TestMCPServiceOauthLoginReturnsAndCarriesLoginIDLikeRust mirrors Rust #49276
// (`mcp_processor.rs`): the app-server mints one UUIDv7 login id per attempt and
// that same id appears in the login response and in the completion
// notification, so a client can match the two.
func TestMCPServiceOauthLoginReturnsAndCarriesLoginIDLikeRust(t *testing.T) {
	issuer := oauthLoginIDIssuer(t)
	completions := make(chan *MCPOAuthLoginCompletion, 1)
	service := oauthLoginIDService(t, issuer, completions)
	threadID := "thread-oauth"

	login, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", ThreadID: &threadID, Scopes: []string{"read"}})
	if err != nil {
		t.Fatalf("OauthLogin() error = %v", err)
	}
	if login.LoginID == nil || strings.TrimSpace(*login.LoginID) == "" {
		t.Fatalf("login response = %#v, want a generated login id", login)
	}
	parsed, err := uuid.Parse(*login.LoginID)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("login id = %q (%v), want a UUIDv7", *login.LoginID, err)
	}
	encoded, err := json.Marshal(login)
	if err != nil {
		t.Fatalf("Marshal(login) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"loginId":"`+*login.LoginID+`"`) {
		t.Fatalf("login response JSON = %s, want the login id", encoded)
	}
	// Every attempt gets its own id.
	if next := NewMCPOAuthLoginID(); next == *login.LoginID {
		t.Fatalf("NewMCPOAuthLoginID() = %q, want a fresh id", next)
	}

	authorizationURL, err := url.Parse(login.AuthorizationURL)
	if err != nil {
		t.Fatalf("Parse authorization URL error = %v", err)
	}
	redirectURI := authorizationURL.Query().Get("redirect_uri")
	state := authorizationURL.Query().Get("state")
	if redirectURI == "" || state == "" {
		t.Fatalf("authorization URL = %s", login.AuthorizationURL)
	}
	response, err := http.Get(redirectURI + "?code=code-1&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback GET error = %v", err)
	}
	_ = response.Body.Close()

	select {
	case completion := <-completions:
		if completion.Name != "docs" || completion.ThreadID != threadID || !completion.Success {
			t.Fatalf("completion = %#v", completion)
		}
		if completion.LoginID != *login.LoginID {
			t.Fatalf("completion login id = %q, want the response's %q", completion.LoginID, *login.LoginID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for OAuth completion")
	}
}

// TestMCPServiceOauthCancelCarriesLoginIDLikeRust pins the same carrier on the
// unsuccessful path: a cancelled attempt's completion still identifies the
// attempt it belongs to.
func TestMCPServiceOauthCancelCarriesLoginIDLikeRust(t *testing.T) {
	issuer := oauthLoginIDIssuer(t)
	completions := make(chan *MCPOAuthLoginCompletion, 1)
	service := oauthLoginIDService(t, issuer, completions)
	threadID := "thread-oauth"

	login, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", ThreadID: &threadID})
	if err != nil {
		t.Fatalf("OauthLogin() error = %v", err)
	}
	if login.LoginID == nil {
		t.Fatalf("login response = %#v, want a generated login id", login)
	}
	if _, err := service.OauthCancel(&MCPServerOauthCancelParams{Name: "docs"}); err != nil {
		t.Fatalf("OauthCancel() error = %v", err)
	}
	select {
	case completion := <-completions:
		if completion.Success || !strings.Contains(completion.Error, "cancelled") {
			t.Fatalf("completion = %#v, want the cancelled attempt", completion)
		}
		if completion.LoginID != *login.LoginID {
			t.Fatalf("completion login id = %q, want the response's %q", completion.LoginID, *login.LoginID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for OAuth cancel completion")
	}
}
