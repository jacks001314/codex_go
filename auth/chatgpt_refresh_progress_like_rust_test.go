package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Rust #58355: an exchange cancelled after it started leaves its generation
// unsettled, and a later completed failure cannot settle that earlier attempt.
// Replacement credentials are never withheld.
func TestChatGPTRefreshProgressRetainsCancelledExchangeLikeRust(t *testing.T) {
	home := t.TempDir()
	snapshot := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-cancel"))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			select {
			case <-r.Context().Done():
			case <-time.After(500 * time.Millisecond):
			}
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()
	t.Setenv(RefreshTokenURLEnvOverride, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := RefreshChatGPTTokens(ctx, &RefreshChatGPTTokenOptions{CodexHome: home, AuthSnapshot: snapshot}); err == nil {
		t.Fatal("RefreshChatGPTTokens() = nil error for a cancelled exchange")
	}
	if !ChatGPTRefreshUnsettled(home, snapshot.Tokens) {
		t.Fatal("cancelled exchange was not recorded as unsettled")
	}

	if _, err := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{CodexHome: home, AuthSnapshot: snapshot}); err == nil {
		t.Fatal("RefreshChatGPTTokens() = nil error for invalid_grant")
	}
	if !ChatGPTRefreshUnsettled(home, snapshot.Tokens) {
		t.Fatal("a completed failure settled an earlier cancelled exchange")
	}

	replacement := map[string]any{
		"account_id":    "workspace-a",
		"refresh_token": "refresh-replacement",
		"access_token":  "access-replacement",
	}
	saveGrantTestAuth(t, home, replacement)
	if ChatGPTRefreshUnsettled(home, replacement) {
		t.Fatal("replacement credentials were withheld")
	}
}

// Rust #58355: successful persistence settles the generation.
func TestChatGPTRefreshProgressSettlesCompletedRefreshLikeRust(t *testing.T) {
	home := t.TempDir()
	snapshot := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-success"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONAuth(t, w, map[string]string{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
		})
	}))
	defer server.Close()
	t.Setenv(RefreshTokenURLEnvOverride, server.URL)

	refreshed, err := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{CodexHome: home, AuthSnapshot: snapshot})
	if err != nil {
		t.Fatalf("RefreshChatGPTTokens() error = %v", err)
	}
	if ChatGPTRefreshUnsettled(home, snapshot.Tokens) || ChatGPTRefreshUnsettled(home, refreshed.Tokens) {
		t.Fatal("a completed refresh stayed unsettled")
	}
}

// Rust #58355: a known failure that never started a successful exchange must
// not quarantine the credential.
func TestChatGPTRefreshProgressDoesNotQuarantineKnownFailureLikeRust(t *testing.T) {
	home := t.TempDir()
	snapshot := saveGrantTestAuth(t, home, grantTestTokens("login-1", "refresh-failure"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()
	t.Setenv(RefreshTokenURLEnvOverride, server.URL)

	if _, err := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{CodexHome: home, AuthSnapshot: snapshot}); err == nil {
		t.Fatal("RefreshChatGPTTokens() = nil error for invalid_grant")
	}
	if ChatGPTRefreshUnsettled(home, snapshot.Tokens) {
		t.Fatal("known refresh failure was recorded as unsettled")
	}
}
