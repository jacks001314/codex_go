package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func managedChatGPTSnapshot(accessToken, lastRefresh string) *AuthDotJSON {
	return &AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token":  accessToken,
			"refresh_token": "old-refresh-token",
		},
		LastRefresh: lastRefresh,
	}
}

// Rust AuthManager::proactive_refresh_reason (#53991): an access token inside
// the five-minute window reports proactive_expiry; without a readable exp, an
// auth older than eight days reports scheduled; everything else waits.
func TestProactiveRefreshReasonMatchesRust(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	expiring := fakeJWT(map[string]any{"exp": now.Add(2 * time.Minute).Unix()})
	atBoundary := fakeJWT(map[string]any{"exp": now.Add(5 * time.Minute).Unix()})
	fresh := fakeJWT(map[string]any{"exp": now.Add(30 * time.Minute).Unix()})
	noExp := fakeJWT(map[string]any{"chatgpt_account_id": "account-123"})
	oldRefresh := now.Add(-9 * 24 * time.Hour).Format(time.RFC3339)
	recentRefresh := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)

	cases := []struct {
		name     string
		snapshot *AuthDotJSON
		want     RefreshReason
		wantOK   bool
	}{
		{name: "expiring access token refreshes proactively", snapshot: managedChatGPTSnapshot(expiring, recentRefresh), want: RefreshReasonProactiveExpiry, wantOK: true},
		{name: "expiry exactly at the window edge refreshes proactively", snapshot: managedChatGPTSnapshot(atBoundary, recentRefresh), want: RefreshReasonProactiveExpiry, wantOK: true},
		{name: "readable exp outside the window never falls back to scheduled", snapshot: managedChatGPTSnapshot(fresh, oldRefresh)},
		{name: "unreadable exp with stale last_refresh refreshes on schedule", snapshot: managedChatGPTSnapshot(noExp, oldRefresh), want: RefreshReasonScheduled, wantOK: true},
		{name: "unreadable exp with recent last_refresh waits", snapshot: managedChatGPTSnapshot(noExp, recentRefresh)},
		{name: "missing tokens fall back to last_refresh", snapshot: &AuthDotJSON{AuthMode: "chatgpt", LastRefresh: oldRefresh}, want: RefreshReasonScheduled, wantOK: true},
		{name: "broken JWT falls back to last_refresh", snapshot: managedChatGPTSnapshot("not-a-jwt", oldRefresh), want: RefreshReasonScheduled, wantOK: true},
		{name: "api key auth is never refreshed", snapshot: &AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test", LastRefresh: oldRefresh}},
		{name: "external chatgpt tokens are not managed auth", snapshot: &AuthDotJSON{AuthMode: "chatgptAuthTokens", Tokens: map[string]any{"access_token": expiring}, LastRefresh: oldRefresh}},
		{name: "nil snapshot never refreshes", snapshot: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			reason, ok := ProactiveRefreshReason(testCase.snapshot, now)
			if ok != testCase.wantOK || reason != testCase.want {
				t.Fatalf("ProactiveRefreshReason() = (%q, %v), want (%q, %v)", reason, ok, testCase.want, testCase.wantOK)
			}
		})
	}
}

func TestProactiveRefreshManagedChatGPTReportsReasonsOnTheWire(t *testing.T) {
	cases := []struct {
		name       string
		snapshot   func(now time.Time) *AuthDotJSON
		wantReason string
	}{
		{
			name: "proactive expiry",
			snapshot: func(now time.Time) *AuthDotJSON {
				return managedChatGPTSnapshot(fakeJWT(map[string]any{"exp": now.Add(time.Minute).Unix()}), now.Format(time.RFC3339))
			},
			wantReason: "proactive_expiry",
		},
		{
			name: "scheduled",
			snapshot: func(now time.Time) *AuthDotJSON {
				return managedChatGPTSnapshot(fakeJWT(map[string]any{"chatgpt_account_id": "account-123"}), now.Add(-9*24*time.Hour).Format(time.RFC3339))
			},
			wantReason: "scheduled",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			clearAuthEnvForRefreshTests(t)
			now := time.Now().UTC()
			home := t.TempDir()
			snapshot := testCase.snapshot(now)
			if err := NewStore(home).Save(*snapshot); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			var requestBody map[string]string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				writeJSONAuth(t, w, map[string]string{
					"access_token":  "new-access-token",
					"refresh_token": "new-refresh-token",
				})
			}))
			defer server.Close()
			t.Setenv(RefreshTokenURLEnvOverride, server.URL)

			refreshed, reason, attempted, err := ProactiveRefreshManagedChatGPT(context.Background(), &RefreshChatGPTTokenOptions{
				CodexHome:    home,
				ClientID:     "client-test",
				AuthSnapshot: snapshot,
			}, now)
			if err != nil || !attempted {
				t.Fatalf("ProactiveRefreshManagedChatGPT() attempted = %v, err = %v", attempted, err)
			}
			if string(reason) != testCase.wantReason {
				t.Fatalf("reason = %q, want %q", reason, testCase.wantReason)
			}
			if got := requestBody["refresh_reason"]; got != testCase.wantReason {
				t.Fatalf("refresh_reason = %q, want %q (body %#v)", got, testCase.wantReason, requestBody)
			}
			if refreshed == nil || refreshed.Tokens["access_token"] != "new-access-token" {
				t.Fatalf("refreshed snapshot = %#v", refreshed)
			}
			persisted, err := NewStore(home).Load()
			if err != nil || persisted == nil || persisted.Tokens["access_token"] != "new-access-token" {
				t.Fatalf("persisted auth = %#v, err = %v", persisted, err)
			}
		})
	}
}

func TestProactiveRefreshManagedChatGPTSkipsFreshAuth(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	now := time.Now().UTC()
	home := t.TempDir()
	snapshot := managedChatGPTSnapshot("opaque-access-token", now.Format(time.RFC3339))
	if err := NewStore(home).Save(*snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSONAuth(t, w, map[string]string{"access_token": "new-access-token"})
	}))
	defer server.Close()
	t.Setenv(RefreshTokenURLEnvOverride, server.URL)

	refreshed, _, attempted, err := ProactiveRefreshManagedChatGPT(context.Background(), &RefreshChatGPTTokenOptions{
		CodexHome:    home,
		AuthSnapshot: snapshot,
	}, now)
	if err != nil || attempted || refreshed != nil {
		t.Fatalf("ProactiveRefreshManagedChatGPT() = (%#v, attempted=%v, err=%v), want no refresh", refreshed, attempted, err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("refresh HTTP calls = %d, want 0", got)
	}
}

func TestProactiveRefreshManagedChatGPTKeepsCachedAuthOnFailure(t *testing.T) {
	clearAuthEnvForRefreshTests(t)
	now := time.Now().UTC()
	home := t.TempDir()
	snapshot := managedChatGPTSnapshot(fakeJWT(map[string]any{"exp": now.Add(time.Minute).Unix()}), now.Format(time.RFC3339))
	if err := NewStore(home).Save(*snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"server_error"}}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv(RefreshTokenURLEnvOverride, server.URL)

	refreshed, reason, attempted, err := ProactiveRefreshManagedChatGPT(context.Background(), &RefreshChatGPTTokenOptions{
		CodexHome:    home,
		AuthSnapshot: snapshot,
	}, now)
	if !attempted || reason != RefreshReasonProactiveExpiry {
		t.Fatalf("attempted = %v, reason = %q", attempted, reason)
	}
	if err == nil {
		t.Fatal("ProactiveRefreshManagedChatGPT() error = nil, want the refresh failure")
	}
	if refreshed == nil || refreshed.Tokens["access_token"] != snapshot.Tokens["access_token"] {
		t.Fatalf("cached auth not preserved: %#v", refreshed)
	}
}
