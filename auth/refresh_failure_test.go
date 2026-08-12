package auth

import "testing"

func TestAuthsEqualForRefreshChatGPTAuthTokensComparesTokenDataLikeRust(t *testing.T) {
	// Rust b28aa476f4 (#38054): external ChatGPT auth equivalence compares
	// token data (not the full auth JSON) so a refreshed access token is
	// applied to a retried request even when non-token metadata differs.
	base := FromChatGPTAuthTokens("access-token", "workspace-one", stringPtr("enterprise"))
	sameTokensDifferentRefresh := base
	sameTokensDifferentRefresh.LastRefresh = "2026-08-12T00:00:00Z"
	if !AuthsEqualForRefresh(&base, &sameTokensDifferentRefresh) {
		t.Fatalf("AuthsEqualForRefresh() = false, want true when only LastRefresh differs")
	}

	differentAccessToken := FromChatGPTAuthTokens("access-token-2", "workspace-one", stringPtr("enterprise"))
	if AuthsEqualForRefresh(&base, &differentAccessToken) {
		t.Fatalf("AuthsEqualForRefresh() = true, want false when the access token differs")
	}

	differentAccount := FromChatGPTAuthTokens("access-token", "workspace-two", stringPtr("enterprise"))
	if AuthsEqualForRefresh(&base, &differentAccount) {
		t.Fatalf("AuthsEqualForRefresh() = true, want false when the account id differs")
	}

	differentPlan := FromChatGPTAuthTokens("access-token", "workspace-one", stringPtr("pro"))
	if AuthsEqualForRefresh(&base, &differentPlan) {
		t.Fatalf("AuthsEqualForRefresh() = true, want false when the plan type differs")
	}
}

func TestAuthsEqualForRefreshChatGPTChatgptModeKeepsFullAuthEquivalence(t *testing.T) {
	// Rust keeps the full auth JSON comparison for regular ChatGPT mode; only
	// chatgptAuthTokens switches to token-data equivalence.
	base := FromChatGPTTokens("id-token", "access-token", "refresh-token")
	withRefresh := base
	withRefresh.LastRefresh = "2026-08-12T00:00:00Z"
	if AuthsEqualForRefresh(&base, &withRefresh) {
		t.Fatalf("AuthsEqualForRefresh() = true, want false when LastRefresh differs in chatgpt mode")
	}
}

func TestAuthsEqualForRefreshChatGPTAuthTokensNilHandling(t *testing.T) {
	left := FromChatGPTAuthTokens("access-token", "workspace-one", nil)
	if !AuthsEqualForRefresh(&left, &left) {
		t.Fatalf("AuthsEqualForRefresh() = false for identical auth")
	}
	if AuthsEqualForRefresh(&left, nil) {
		t.Fatalf("AuthsEqualForRefresh() = true for nil right")
	}
	if AuthsEqualForRefresh(nil, &left) {
		t.Fatalf("AuthsEqualForRefresh() = true for nil left")
	}
	if !AuthsEqualForRefresh(nil, nil) {
		t.Fatalf("AuthsEqualForRefresh() = false for nil/nil")
	}
}

func stringPtr(value string) *string {
	return &value
}
