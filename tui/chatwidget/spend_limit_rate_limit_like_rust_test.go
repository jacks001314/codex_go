package chatwidget

import "testing"

// TestAppServerSpendLimitsMapToUsageLimitLikeRust mirrors Rust #62329's
// app_server_rate_limit_error_kind: every scoped spend limit drives the same
// usage-limit handling as UsageLimitExceeded.
func TestAppServerSpendLimitsMapToUsageLimitLikeRust(t *testing.T) {
	for _, kind := range []TurnRuntimeCodexErrorKind{
		TurnRuntimeCodexErrorUsageLimitExceeded,
		TurnRuntimeCodexErrorAPIKeySpendLimitExceeded,
		TurnRuntimeCodexErrorProjectSpendLimitExceeded,
		TurnRuntimeCodexErrorOrganizationSpendLimitExceeded,
	} {
		got, ok := AppServerRateLimitErrorKind(&TurnRuntimeCodexErrorInfo{Kind: kind})
		if !ok || got != RateLimitErrorUsageLimit {
			t.Fatalf("AppServerRateLimitErrorKind(%q) = %q, %t; want usage_limit", kind, got, ok)
		}
	}
	if _, ok := AppServerRateLimitErrorKind(&TurnRuntimeCodexErrorInfo{Kind: TurnRuntimeCodexErrorCyberPolicy}); ok {
		t.Fatal("policy refusals are not rate limits")
	}
}
