package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMCPStatusSetsReauthenticationFailureReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			writeJSON(t, w, map[string]any{
				"access_token":  "oauth-new",
				"refresh_token": "refresh-2",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("reject existing credentials"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configURL := server.URL + "/mcp"
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-1",
		ExpiresAtMillis: &expiresAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: configURL, OAuthClientID: "client-1", Enabled: true}},
	}, CodexHome: home})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].FailureReason == nil || *status.Data[0].FailureReason != "reauthenticationRequired" {
		t.Fatalf("status = %#v", status.Data)
	}
	if status.Data[0].Error == nil || !strings.Contains(*status.Data[0].Error, "requires OAuth reauthentication") {
		t.Fatalf("Error = %#v", status.Data[0].Error)
	}
}
