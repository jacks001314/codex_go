package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
)

func appserverFakeJWT(payload map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	body, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func appserverManagedChatGPTAuth(accessToken string, lastRefresh time.Time) *auth.AuthDotJSON {
	return &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token":  accessToken,
			"refresh_token": "old-refresh-token",
		},
		LastRefresh: lastRefresh.Format(time.RFC3339),
	}
}

// Rust #53991: AuthManager::auth() refreshes a managed ChatGPT auth whose
// access token is inside the five-minute window (`proactive_expiry`) or whose
// last_refresh is older than eight days (`scheduled`) before the turn's
// request, and the app-server publishes the refreshed credential.
func TestResponsesAgentProactivelyRefreshesManagedChatGPTLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		snapshot   func(now time.Time) *auth.AuthDotJSON
		wantReason string
	}{
		{
			name: "proactive expiry",
			snapshot: func(now time.Time) *auth.AuthDotJSON {
				return appserverManagedChatGPTAuth(appserverFakeJWT(map[string]any{"exp": now.Add(time.Minute).Unix()}), now)
			},
			wantReason: "proactive_expiry",
		},
		{
			name: "scheduled",
			snapshot: func(now time.Time) *auth.AuthDotJSON {
				return appserverManagedChatGPTAuth("opaque-access-token", now.Add(-9*24*time.Hour))
			},
			wantReason: "scheduled",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			clearAuthEnvAppserver(t)
			now := time.Now().UTC()
			home := t.TempDir()
			snapshot := testCase.snapshot(now)
			if err := auth.NewStore(home).Save(*snapshot); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			var requestBody map[string]string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"access_token":  "new-access-token",
					"refresh_token": "new-refresh-token",
				})
			}))
			defer server.Close()
			t.Setenv(auth.RefreshTokenURLEnvOverride, server.URL)

			router := NewRuntimeRouter(RuntimeServices{
				Account:    auth.NewAccountManager(),
				Config:     config.NewConfigService(home),
				HTTPClient: &http.Client{},
			})
			cfg := &config.Config{Values: map[string]any{}}
			refreshed := router.proactiveRefreshManagedChatGPT(context.Background(), home, cfg, snapshot)
			if refreshed == nil || refreshed.Tokens["access_token"] != "new-access-token" {
				t.Fatalf("refreshed snapshot = %#v", refreshed)
			}
			if got := requestBody["refresh_reason"]; got != testCase.wantReason {
				t.Fatalf("refresh_reason = %q, want %q (body %#v)", got, testCase.wantReason, requestBody)
			}
			published := router.requireAccount().AuthSnapshot()
			if published == nil || published.Tokens["access_token"] != "new-access-token" {
				t.Fatalf("published account snapshot = %#v", published)
			}
		})
	}
}

func TestResponsesAgentKeepsFreshManagedChatGPTAuth(t *testing.T) {
	clearAuthEnvAppserver(t)
	now := time.Now().UTC()
	home := t.TempDir()
	snapshot := appserverManagedChatGPTAuth("opaque-access-token", now)
	if err := auth.NewStore(home).Save(*snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "new-access-token"})
	}))
	defer server.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, server.URL)

	router := NewRuntimeRouter(RuntimeServices{
		Account:    auth.NewAccountManager(),
		Config:     config.NewConfigService(home),
		HTTPClient: &http.Client{},
	})
	refreshed := router.proactiveRefreshManagedChatGPT(context.Background(), home, &config.Config{Values: map[string]any{}}, snapshot)
	if refreshed != snapshot {
		t.Fatalf("fresh auth was replaced: %#v", refreshed)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("refresh HTTP calls = %d, want 0", got)
	}
}
