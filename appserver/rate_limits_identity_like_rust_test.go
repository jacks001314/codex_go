package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
)

// TestRuntimeRouterRateLimitsIdentityRecheckedAfterBackendResponseLikeRust
// covers Rust's rate_limits_identity_tests (#45762): the active login can change
// while the usage read is in flight, so account-bound CTA content is only
// exposed when the response still matches the identity resolved afterwards.
func TestRuntimeRouterRateLimitsIdentityRecheckedAfterBackendResponseLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		account  string
		user     string
		verified bool
	}{
		{name: "user_switch", account: "workspace-a", user: "user-b"},
		{name: "workspace_switch", account: "workspace-b", user: "user-a"},
		{name: "same_identity_token_refresh", account: "workspace-a", user: "user-a", verified: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAuthEnvAppserver(t)
			home := t.TempDir()
			writeRateLimitIdentityAuth(t, home, "old-token", "workspace-a", "user-a")

			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseResponse := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseResponse()
			handlerErr := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/codex/usage" {
					handlerErr <- "path = " + r.URL.Path
				}
				if got := r.Header.Get("Authorization"); got != "Bearer old-token" {
					handlerErr <- "Authorization = " + got
				}
				if got := r.Header.Get("ChatGPT-Account-ID"); got != "workspace-a" {
					handlerErr <- "ChatGPT-Account-ID = " + got
				}
				close(entered)
				<-release
				w.Header().Set("Content-Type", "application/json")
				payload := map[string]any{
					"account_id": "workspace-a",
					"user_id":    "user-a",
					"plan_type":  "team",
					"rate_limit": map[string]any{
						"allowed":       true,
						"limit_reached": false,
						"primary_window": map[string]any{
							"used_percent":         42,
							"limit_window_seconds": 3600,
							"reset_after_seconds":  120,
							"reset_at":             2000000000,
						},
					},
				}
				if err := json.NewEncoder(w).Encode(payload); err != nil {
					handlerErr <- "encode: " + err.Error()
				}
			}))
			defer server.Close()
			if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
				t.Fatalf("Write config error = %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{
				Account: auth.NewAccountManager(),
				Config:  config.NewConfigService(home),
			})

			request := requestWithParams(t, IntID(1), MethodGetAccountRateLimits, map[string]any{})
			done := make(chan *Response, 1)
			go func() { done <- router.Handle(request) }()

			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("backend usage read did not start")
			}
			writeRateLimitIdentityAuth(t, home, "new-token", tc.account, tc.user)
			releaseResponse()

			response := <-done
			close(handlerErr)
			for msg := range handlerErr {
				t.Errorf("usage request: %s", msg)
			}
			if response == nil || response.Error != nil {
				t.Fatalf("rate limits response = %+v", response)
			}
			limits := response.Result.(*auth.GetAccountRateLimitsResponse)
			if limits.AccountID == nil || *limits.AccountID != "workspace-a" {
				t.Fatalf("accountID = %v, want workspace-a", limits.AccountID)
			}
			if limits.RateLimits.PlanType == nil || *limits.RateLimits.PlanType != auth.PlanType("team") {
				t.Fatalf("plan type = %+v, want team", limits.RateLimits.PlanType)
			}
			if limits.RateLimits.Primary == nil || limits.RateLimits.Primary.UsedPercent != 42 {
				t.Fatalf("rate limits primary = %+v", limits.RateLimits.Primary)
			}
			if tc.verified {
				if limits.OrdinaryUsageAllowed == nil || !*limits.OrdinaryUsageAllowed {
					t.Fatalf("ordinaryUsageAllowed = %v, want true for unchanged identity", limits.OrdinaryUsageAllowed)
				}
				return
			}
			if limits.OrdinaryUsageAllowed != nil {
				t.Fatalf("ordinaryUsageAllowed = %v, want nil after identity change", *limits.OrdinaryUsageAllowed)
			}
		})
	}
}

// writeRateLimitIdentityAuth stores a ChatGPT token auth file whose account and
// user claims are explicit so the identity check does not depend on JWT parsing.
func writeRateLimitIdentityAuth(t *testing.T, home, token, account, user string) {
	t.Helper()
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgptAuthTokens",
		Tokens: map[string]any{
			"access_token":       token,
			"account_id":         account,
			"chatgpt_account_id": account,
			"chatgpt_user_id":    user,
			"plan_type":          "team",
			"chatgpt_plan_type":  "team",
		},
	}
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("auth save error: %v", err)
	}
}
