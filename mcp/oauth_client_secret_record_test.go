package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfiguredOAuthClientSecretIsNotPersistedInTokenRecordLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if r.Form.Get("client_secret") != "configured-secret" {
			t.Fatalf("client_secret = %q", r.Form.Get("client_secret"))
		}
		writeJSON(t, w, map[string]any{
			"access_token":  "access",
			"refresh_token": "refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	tokens, err := NewOAuthTokenClient(server.Client()).ExchangeAuthorizationCode(context.Background(), &OAuthCodeExchangeOptions{
		ServerName:                 "docs",
		ServerURL:                  "https://mcp.example.test/mcp",
		ClientID:                   "client",
		ClientSecret:               "configured-secret",
		OmitClientSecretFromRecord: true,
		AuthorizationEndpoint:      server.URL + "/authorize",
		TokenEndpoint:              server.URL + "/token",
		RedirectURL:                "http://127.0.0.1:1234/callback",
		Code:                       "code",
	})
	if err != nil {
		t.Fatalf("ExchangeAuthorizationCode() error = %v", err)
	}
	if tokens == nil || tokens.ClientSecret != "" {
		t.Fatalf("token record retained configured secret: %#v", tokens)
	}
}
