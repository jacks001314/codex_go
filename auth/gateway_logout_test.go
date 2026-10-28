package auth

import (
	"context"
	"testing"
	"time"
)

// Mirrors Rust OAuthSessionManager::logout: the persisted grant and the
// in-memory copy are both removed, the manager publishes NotReady, and a
// second logout reports that nothing was stored.
func TestGatewayLogoutRemovesStoredAndCachedCredentialsLikeRust(t *testing.T) {
	home := t.TempDir()
	manager := NewGatewayAuthManager(GatewayAuthConfig{
		AuthorizationURL: "https://issuer.example.com/authorize",
		TokenURL:         "https://issuer.example.com/token",
		ClientID:         "client-1",
	}, home, nil, newGatewayTestStore())

	expires := time.Now().Add(time.Hour).Unix()
	token := &gatewayStoredToken{
		AccessToken:     "access-1",
		RefreshToken:    "refresh-1",
		AuthorizationID: "grant-1",
		ExpiresAt:       &expires,
	}
	if err := manager.saveToken(token); err != nil {
		t.Fatalf("saveToken() error = %v", err)
	}
	manager.mu.Lock()
	manager.cache.token = token
	manager.cache.pending = &gatewayStoredToken{AccessToken: "pending-1"}
	manager.mu.Unlock()

	changes, unsubscribe := manager.SubscribeStatus()
	defer unsubscribe()

	removed, err := manager.Logout(context.Background())
	if err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if !removed {
		t.Fatal("Logout() = false, want true for a stored grant")
	}
	if stored, err := manager.loadToken(); err != nil || stored != nil {
		t.Fatalf("loadToken() after logout = %#v, %v", stored, err)
	}
	manager.mu.Lock()
	cached, pending := manager.cache.token, manager.cache.pending
	manager.mu.Unlock()
	if cached != nil || pending != nil {
		t.Fatalf("cache after logout = %#v / %#v, want cleared", cached, pending)
	}
	select {
	case change := <-changes:
		if change.Status.Kind != GatewayAuthStatusNotReady {
			t.Fatalf("logout published %#v, want notReady", change.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("logout did not publish a status change")
	}

	removed, err = manager.Logout(context.Background())
	if err != nil {
		t.Fatalf("second Logout() error = %v", err)
	}
	if removed {
		t.Fatal("second Logout() = true, want false")
	}
}

func TestGatewayLogoutCancelledContextKeepsCredentials(t *testing.T) {
	home := t.TempDir()
	manager := NewGatewayAuthManager(GatewayAuthConfig{TokenURL: "https://issuer.example.com/token"}, home, nil, newGatewayTestStore())
	if err := manager.saveToken(&gatewayStoredToken{AccessToken: "access-1", AuthorizationID: "grant-1"}); err != nil {
		t.Fatalf("saveToken() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Logout(ctx); err == nil {
		t.Fatal("Logout() with a cancelled context must fail")
	}
	if stored, err := manager.loadToken(); err != nil || stored == nil {
		t.Fatalf("loadToken() after cancelled logout = %#v, %v, want retained grant", stored, err)
	}
}
