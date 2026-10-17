package auth

import (
	"errors"
	"testing"
)

// Rust #54070: the reusable OAuth session API is the same credential manager
// exposed under gateway-specific compatibility names.
func TestOAuthSessionAliasesKeepGatewayBehaviorLikeRust(t *testing.T) {
	config := OAuthSessionConfig{
		AuthorizationURL: "http://127.0.0.1:18080/authorize",
		TokenURL:         "http://127.0.0.1:18080/token",
		ClientID:         "codex-test",
	}
	manager := NewOAuthSessionManager(config, t.TempDir(), nil, newGatewayTestStore())
	var compatibility *GatewayAuthManager = manager
	if compatibility != manager {
		t.Fatal("OAuthSessionManager must alias GatewayAuthManager")
	}
	status, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Kind != OAuthSessionStatusNotReady {
		t.Fatalf("initial status = %#v", status)
	}
	changes, unsubscribe := SubscribeOAuthSessionStatus(manager)
	defer unsubscribe()
	if changes == nil {
		t.Fatal("status subscription is nil")
	}
	wrapped := &OAuthSessionError{Err: ErrGatewayLoginInProgress}
	if !errors.Is(wrapped, ErrGatewayLoginInProgress) {
		t.Fatalf("OAuthSessionError unwrap = %v", wrapped)
	}
}
