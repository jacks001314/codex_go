package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Mirrors Rust refresh_lock_tests::acquisition_times_out_without_stealing.
func TestMCPOAuthCredentialLockAcquisitionTimesOutWithoutStealing(t *testing.T) {
	home := t.TempDir()
	storeKey := "test-store-key"
	held, err := acquireMCPOAuthCredentialLockIn(home, storeKey, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire held lock: %v", err)
	}
	_, err = acquireMCPOAuthCredentialLockIn(home, storeKey, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out after 50ms waiting for OAuth refresh lock") {
		t.Fatalf("contending acquisition = %v, want timeout", err)
	}
	held.Release()
	reacquired, err := acquireMCPOAuthCredentialLockIn(home, storeKey, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	reacquired.Release()
}

func TestMCPOAuthCredentialLockScopesByStoreKey(t *testing.T) {
	home := t.TempDir()
	first, err := acquireMCPOAuthCredentialLockIn(home, "key-one", time.Second)
	if err != nil {
		t.Fatalf("acquire key-one: %v", err)
	}
	defer first.Release()
	second, err := acquireMCPOAuthCredentialLockIn(home, "key-two", time.Second)
	if err != nil {
		t.Fatalf("acquire key-two should not contend: %v", err)
	}
	second.Release()
}

func TestOAuthStoreSaveAndDeleteUseCredentialLock(t *testing.T) {
	home := t.TempDir()
	store := NewOAuthStore(home)
	tokens := &OAuthTokenSet{
		ServerName:  "docs",
		ServerURL:   "https://mcp.example/mcp",
		ClientID:    "client-1",
		AccessToken: "token-1",
	}
	if err := store.Save(tokens); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(tokens.ServerName, tokens.ServerURL)
	if err != nil || loaded == nil || loaded.AccessToken != "token-1" {
		t.Fatalf("Load() = %#v, %v", loaded, err)
	}
	removed, err := store.Delete(tokens.ServerName, tokens.ServerURL)
	if err != nil || !removed {
		t.Fatalf("Delete() = %t, %v", removed, err)
	}
}

func TestOAuthStoreSaveWhileLockHeldAvoidsNestedAcquisition(t *testing.T) {
	home := t.TempDir()
	store := NewOAuthStore(home)
	tokens := &OAuthTokenSet{
		ServerName:  "docs",
		ServerURL:   "https://mcp.example/mcp",
		ClientID:    "client-1",
		AccessToken: "token-1",
	}
	lock, err := acquireMCPOAuthCredentialLockForServer(home, tokens.ServerName, tokens.ServerURL)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.Release()
	// The refresh transaction holds the lock and uses the locked writers.
	if err := store.saveWithLockHeld(tokens); err != nil {
		t.Fatalf("saveWithLockHeld() error = %v", err)
	}
	removed, err := store.deleteWithLockHeld(tokens.ServerName, tokens.ServerURL)
	if err != nil || !removed {
		t.Fatalf("deleteWithLockHeld() = %t, %v", removed, err)
	}
}

// The locked authoritative reread adopts a credential another process
// refreshed without contacting the provider.
func TestMCPOAuthRefreshAdoptsUnderLockWithoutContactingProvider(t *testing.T) {
	var tokenRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			writeJSON(t, w, map[string]any{
				"issuer":                 "https://issuer.example.test",
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			tokenRequests.Add(1)
			http.Error(w, "should not be contacted", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configURL := server.URL + "/mcp"
	future := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-winner",
		RefreshToken:    "refresh-winner",
		ExpiresAtMillis: &future,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	past := time.Now().Add(-time.Hour).UnixMilli()
	previous := &OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		Issuer:          "https://issuer.example.test",
		AccessToken:     "oauth-stale",
		RefreshToken:    "refresh-stale",
		ExpiresAtMillis: &past,
	}
	client := &httpClient{config: &ServerConfig{URL: configURL, OAuthClientID: "client-1"}, client: server.Client()}
	refreshed, err := client.refreshOAuthTokenForRequest(previous, "docs", home, false)
	if err != nil || refreshed == nil || refreshed.AccessToken != "oauth-winner" {
		t.Fatalf("refresh = %#v, %v; want adopted winner", refreshed, err)
	}
	if got := tokenRequests.Load(); got != 0 {
		t.Fatalf("provider token requests = %d, want 0", got)
	}
}
