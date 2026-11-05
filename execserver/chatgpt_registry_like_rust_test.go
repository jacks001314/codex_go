package execserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatGPTRegistryBearerIsRedactedFromDiagnosticsLikeRust(t *testing.T) {
	const token = "synthetic-chatgpt-registry-secret"
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "endor" {
			t.Errorf("ChatGPT-Account-ID = %q", got)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"denied for %s"}}`, token)
	}))
	defer registry.Close()

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("ChatGPT-Account-ID", "endor")
	cfg := (RemoteEnvironmentConfig{
		BaseURL:       registry.URL,
		EnvironmentID: "env-1",
		AuthHeaders:   headers,
	}).WithRegistryBearerRedaction()
	_, err := registerRemoteEnvironment(context.Background(), cfg, RemotePublicKey{})
	if err == nil {
		t.Fatal("registerRemoteEnvironment() = nil, want a registry error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("registry error leaked the ChatGPT bearer: %v", err)
	}
	if !strings.Contains(err.Error(), "environment registry executor registration failed") {
		t.Fatalf("registry error = %v, want the fixed redacted diagnostic", err)
	}
}

func TestChatGPTRegistryModeKeepsAccountHeaderLikeRust(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer oversized-"+strings.Repeat("a", executorCredentialTokenMaxBytes+1))
	headers.Set("ChatGPT-Account-ID", "endor")
	cfg := (RemoteEnvironmentConfig{
		BaseURL:       "https://registry.example.test",
		EnvironmentID: "env-1",
		AuthHeaders:   headers,
	}).WithRegistryBearerRedaction()
	resolved, credential, err := resolveRemoteEnvironmentAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("resolveRemoteEnvironmentAuth() error = %v", err)
	}
	if resolved.Get("ChatGPT-Account-ID") != "endor" || credential == "" {
		t.Fatalf("resolved headers = %v, credential bytes = %d", resolved, len(credential))
	}
}
