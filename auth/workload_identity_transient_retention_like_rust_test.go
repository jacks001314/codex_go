package auth

import (
	"net/http"
	"testing"
)

// Mirrors Rust #59491: a transient external auth failure keeps the cached
// identity, while permanent failures and credentials the active restrictions
// forbid still clear it.
func TestRetainAuthAfterTransientExternalFailureLikeRust(t *testing.T) {
	cached := &AuthDotJSON{
		AuthMode: "chatgptAuthTokens",
		Tokens:   map[string]any{"access_token": "cached-token", "chatgpt_account_id": "workspace-allowed"},
	}
	transient := &workloadExchangeRejected{Status: http.StatusServiceUnavailable}
	if !WorkloadIdentityTransientError(transient) {
		t.Fatal("service unavailable exchange was not classified as transient")
	}
	retained, ok := RetainAuthAfterTransientExternalFailure(transient, cached, nil)
	if !ok || retained == nil {
		t.Fatalf("retained = %#v, ok = %v; want the cached credential", retained, ok)
	}
	if retained.Tokens["access_token"] != "cached-token" {
		t.Fatalf("retained token = %v, want the cached token", retained.Tokens["access_token"])
	}
	retained.Tokens["access_token"] = "mutated"
	if cached.Tokens["access_token"] != "cached-token" {
		t.Fatal("retained credential aliased the cached tokens")
	}

	if _, ok := RetainAuthAfterTransientExternalFailure(transient, cached, func(*AuthDotJSON) bool { return false }); ok {
		t.Fatal("retained a credential the active restrictions reject")
	}
	if _, ok := RetainAuthAfterTransientExternalFailure(workloadErrInvalidExchangeResponse, cached, nil); ok {
		t.Fatal("retained a credential after a permanent external auth failure")
	}
	if _, ok := RetainAuthAfterTransientExternalFailure(transient, nil, nil); ok {
		t.Fatal("retained when nothing was cached")
	}

	rejected := &workloadExchangeRejected{Status: http.StatusUnauthorized}
	if WorkloadIdentityTransientError(rejected) {
		t.Fatal("a rejected exchange was classified as transient")
	}
}
