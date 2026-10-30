package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"codex_go/auth"
	"codex_go/config"
)

// TestRuntimeRouterRateLimitsUsageCapabilitiesLikeRust covers Rust #42358: the
// optional account/rateLimits/read capabilities are preserved across omitted,
// null, and object params; Luna Reserve is forwarded only for eligible ChatGPT
// sessions; lightweight polling still returns the usage response credit count;
// and account-bound decisions stay filtered by identity.
func TestRuntimeRouterRateLimitsUsageCapabilitiesLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		fedramp     bool
		params      any
		omitParams  bool
		wantLuna    bool
		wantAllowed *bool
	}{
		{name: "chatgpt_opt_in", mode: "chatgpt", params: map[string]any{"supportsLunaReserve": true}, wantLuna: true, wantAllowed: boolPtr(true)},
		{name: "chatgpt_omitted_params", mode: "chatgpt", omitParams: true, wantAllowed: boolPtr(true)},
		{name: "chatgpt_null_params", mode: "chatgpt", params: nil, wantAllowed: boolPtr(true)},
		{name: "chatgpt_opt_out", mode: "chatgpt", params: map[string]any{"supportsLunaReserve": false}, wantAllowed: boolPtr(true)},
		{name: "chatgpt_lightweight_poll", mode: "chatgpt", params: map[string]any{"excludeResetCreditDetails": true}, wantAllowed: boolPtr(true)},
		{name: "auth_tokens_not_eligible", mode: "chatgptAuthTokens", params: map[string]any{"supportsLunaReserve": true}, wantAllowed: boolPtr(true)},
		{name: "fedramp_not_eligible", mode: "chatgpt", fedramp: true, params: map[string]any{"supportsLunaReserve": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAuthEnvAppserver(t)
			home := t.TempDir()
			tokens := map[string]any{
				"access_token":       "token",
				"account_id":         "workspace-a",
				"chatgpt_account_id": "workspace-a",
				"chatgpt_user_id":    "user-a",
				"plan_type":          "team",
				"chatgpt_plan_type":  "team",
			}
			if tc.fedramp {
				tokens["chatgpt_account_is_fedramp"] = true
			}
			if err := auth.NewStore(home).Save(auth.AuthDotJSON{AuthMode: tc.mode, Tokens: tokens}); err != nil {
				t.Fatalf("auth save error = %v", err)
			}

			var mu sync.Mutex
			lunaHeader := ""
			requestCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/codex/usage" {
					t.Errorf("usage path = %q", r.URL.Path)
				}
				mu.Lock()
				lunaHeader = r.Header.Get("x-openai-codex-luna-reserve")
				requestCount++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"account_id": "workspace-a",
					"user_id":    "user-a",
					"plan_type":  "team",
					"rate_limit": map[string]any{
						"allowed":       true,
						"limit_reached": false,
						"primary_window": map[string]any{
							"used_percent":         12,
							"limit_window_seconds": 3600,
							"reset_after_seconds":  60,
							"reset_at":             2000000000,
						},
					},
					"rate_limit_reset_credits": map[string]any{"available_count": 3},
				})
			}))
			defer server.Close()
			if err := os.WriteFile(config.ConfigPath(home), []byte(`chatgpt_base_url = "`+server.URL+`"`), 0o600); err != nil {
				t.Fatalf("Write config error = %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{
				Account: auth.NewAccountManager(),
				Config:  config.NewConfigService(home),
			})

			var request *Request
			if tc.omitParams {
				request = &Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodGetAccountRateLimits}
			} else {
				request = requestWithParams(t, IntID(1), MethodGetAccountRateLimits, tc.params)
			}
			response := router.Handle(request)
			if response == nil || response.Error != nil {
				t.Fatalf("rate limits response = %+v", response)
			}
			limits := response.Result.(*auth.GetAccountRateLimitsResponse)
			mu.Lock()
			header := lunaHeader
			requests := requestCount
			mu.Unlock()
			wantHeader := ""
			if tc.wantLuna {
				wantHeader = "1"
			}
			if header != wantHeader {
				t.Fatalf("luna reserve header = %q, want %q", header, wantHeader)
			}
			if requests != 1 {
				t.Fatalf("usage requests = %d, want 1 without a separate reset-credit detail request", requests)
			}
			if tc.wantAllowed == nil {
				if limits.OrdinaryUsageAllowed != nil {
					t.Fatalf("ordinaryUsageAllowed = %v, want nil", *limits.OrdinaryUsageAllowed)
				}
			} else if limits.OrdinaryUsageAllowed == nil || *limits.OrdinaryUsageAllowed != *tc.wantAllowed {
				t.Fatalf("ordinaryUsageAllowed = %v, want %v", limits.OrdinaryUsageAllowed, *tc.wantAllowed)
			}
			if limits.RateLimitResetCredits == nil || limits.RateLimitResetCredits.AvailableCount != 3 {
				t.Fatalf("rate limit reset credits = %+v, want available count 3 from the usage response", limits.RateLimitResetCredits)
			}
		})
	}
}

func TestRuntimeRouterRateLimitsRejectsNonBooleanCapabilitiesLikeRust(t *testing.T) {
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: map[string]any{
		"access_token":       "token",
		"account_id":         "workspace-a",
		"chatgpt_account_id": "workspace-a",
		"chatgpt_user_id":    "user-a",
	}}); err != nil {
		t.Fatalf("auth save error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccountRateLimits, map[string]any{
		"supportsLunaReserve": "yes",
	}))
	if response == nil || response.Error == nil {
		t.Fatalf("rate limits response = %+v, want a parameter error", response)
	}
	if response.Error.Code != -32600 {
		t.Fatalf("error code = %d, want -32600", response.Error.Code)
	}
}
