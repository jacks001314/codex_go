package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func mustMarshalAuthForTest(t *testing.T, auth AuthDotJSON) string {
	t.Helper()
	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return string(data)
}

// Rust #58343: automatic storage loads fallback credentials when the keyring is
// unreadable. A refresh of those credentials must persist to the file store and
// must not overwrite the unread keyring generation.
func TestAutoStoreRefreshPersistsFallbackWhenKeyringUnreadableLikeRust(t *testing.T) {
	home := t.TempDir()
	keyringStore := NewKeyringStore(KeyringBackendDirect)
	key := authStoreKey(home)
	if err := keyringStore.Set(authKeyringService, key, "unreadable credential document"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	t.Cleanup(func() { _, _ = keyringStore.Delete(authKeyringService, key) })
	fallback := saveGrantTestAuth(t, home, grantTestTokens("login-fallback", "refresh-fallback"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONAuth(t, w, map[string]string{
			"access_token":  "access-refreshed",
			"refresh_token": "refresh-refreshed",
		})
	}))
	defer server.Close()
	t.Setenv(RefreshTokenURLEnvOverride, server.URL)

	options := &StoreOptions{Mode: AuthCredentialsStoreAuto, KeyringStore: keyringStore}
	refreshed, err := RefreshChatGPTTokens(context.Background(), &RefreshChatGPTTokenOptions{
		CodexHome:    home,
		StoreOptions: options,
		AuthSnapshot: fallback,
	})
	if err != nil {
		t.Fatalf("RefreshChatGPTTokens() error = %v", err)
	}
	if refreshed == nil || refreshed.Tokens["access_token"] != "access-refreshed" {
		t.Fatalf("refreshed auth = %#v", refreshed)
	}
	persisted, err := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile}).Load()
	if err != nil || persisted == nil || persisted.Tokens["access_token"] != "access-refreshed" {
		t.Fatalf("fallback auth = %#v, error = %v", persisted, err)
	}
	value, err := keyringStore.Get(authKeyringService, key)
	if err != nil || value != "unreadable credential document" {
		t.Fatalf("unread keyring entry changed: %q, %v", value, err)
	}
}

// Rust #58343: a keyring that recovers with a newer generation before the
// commit makes the fallback refresh stale, so it is rejected untouched.
func TestAutoStoreRefreshRejectsStaleFallbackAfterKeyringRecoveryLikeRust(t *testing.T) {
	home := t.TempDir()
	keyringStore := NewKeyringStore(KeyringBackendDirect)
	key := authStoreKey(home)
	t.Cleanup(func() { _, _ = keyringStore.Delete(authKeyringService, key) })
	fallback := saveGrantTestAuth(t, home, grantTestTokens("login-fallback", "refresh-fallback"))
	newer := AuthDotJSON{AuthMode: "chatgpt", Tokens: grantTestTokens("login-fallback", "refresh-newer")}
	if err := keyringStore.Set(authKeyringService, key, mustMarshalAuthForTest(t, newer)); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	options := &StoreOptions{Mode: AuthCredentialsStoreAuto, KeyringStore: keyringStore}
	err := persistRefreshedChatGPTTokens(home, fallback, &ExchangedTokens{
		AccessToken:  "access-refreshed",
		RefreshToken: "refresh-refreshed",
	}, options)
	if !errors.Is(err, errAccountCredentialsChangedDuringRefresh) {
		t.Fatalf("persist error = %v, want stale credentials", err)
	}
	value, err := keyringStore.Get(authKeyringService, key)
	if err != nil || value != mustMarshalAuthForTest(t, newer) {
		t.Fatalf("keyring entry changed: %q, %v", value, err)
	}
}

// Rust #58343: an unresolvable fallback symlink must not block keyring commits.
func TestKeyringCommitProceedsWhenFallbackSymlinkUnresolvableLikeRust(t *testing.T) {
	home := t.TempDir()
	authPath := NewStore(home).Path()
	if err := os.Symlink(authPath, authPath); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(authPath) })
	keyringStore := NewKeyringStore(KeyringBackendDirect)
	key := authStoreKey(home)
	t.Cleanup(func() { _, _ = keyringStore.Delete(authKeyringService, key) })

	options := &StoreOptions{Mode: AuthCredentialsStoreKeyring, KeyringStore: keyringStore}
	if err := NewStoreWithOptions(home, options).Save(FromAPIKey("sk-keyring")); err != nil {
		t.Fatalf("Save() with an unresolvable fallback symlink = %v", err)
	}
	if _, err := NewStoreWithOptions(home, options).Delete(); err != nil {
		t.Fatalf("Delete() with an unresolvable fallback symlink = %v", err)
	}
}
