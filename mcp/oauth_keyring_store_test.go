package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codex_go/keyring"
)

// fakeKeyring is an in-process keyring.Store for the MCP keyring-path tests.
type fakeKeyring struct {
	mu       sync.Mutex
	values   map[string]string
	failSave error
	failLoad error
}

// TestComputeMCPOAuthStoreKeyLikeRust pins the store-key shape: enterprise
// (ema-idp:) credentials are isolated per Codex home, ordinary servers ignore
// the home, and executor-owned servers use the ":" separator.
func TestComputeMCPOAuthStoreKeyLikeRust(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	keyA, err := computeMCPOAuthStoreKey(homeA, "ema-idp:acme", "https://idp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := computeMCPOAuthStoreKey(homeB, "ema-idp:acme", "https://idp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if keyA == keyB {
		t.Fatal("enterprise store keys must be isolated per codex home")
	}
	if !strings.HasPrefix(keyA, "ema-idp:acme|") {
		t.Fatalf("enterprise key = %q", keyA)
	}
	ordinaryA, err := computeMCPOAuthStoreKey(homeA, "docs", "https://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	ordinaryB, err := computeMCPOAuthStoreKey(homeB, "docs", "https://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if ordinaryA != ordinaryB {
		t.Fatalf("ordinary store keys must not depend on the codex home: %q vs %q", ordinaryA, ordinaryB)
	}
	executor, err := computeMCPOAuthStoreKey("", "executor:acme", "https://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(executor, "executor:acme:") {
		t.Fatalf("executor key = %q", executor)
	}
	if _, err := computeMCPOAuthStoreKey("", "ema-idp:acme", "https://idp.example.com"); err == nil {
		t.Fatal("enterprise key without a codex home must fail")
	}
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{values: map[string]string{}}
}

func fakeKeyringKey(service string, account string) string {
	return service + "\x00" + account
}

func (f *fakeKeyring) Load(service string, account string) (string, error) {
	if f.failLoad != nil {
		return "", f.failLoad
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[fakeKeyringKey(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (f *fakeKeyring) Save(service string, account string, secret string) error {
	if f.failSave != nil {
		return f.failSave
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[fakeKeyringKey(service, account)] = secret
	return nil
}

func (f *fakeKeyring) Delete(service string, account string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fakeKeyringKey(service, account)
	if _, ok := f.values[key]; !ok {
		return false, nil
	}
	delete(f.values, key)
	return true, nil
}

func enableOAuthKeyring(t *testing.T) {
	t.Helper()
	previous := MCPOAuthKeyringAvailable
	MCPOAuthKeyringAvailable = true
	t.Cleanup(func() { MCPOAuthKeyringAvailable = previous })
}

func keyringTestTokens(serverName string, serverURL string) *OAuthTokenSet {
	return &OAuthTokenSet{
		ServerName:   serverName,
		ServerURL:    serverURL,
		ClientID:     "client",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
	}
}

// TestOAuthStoreAutoUsesTheKeyringAndCleansTheFile pins Rust's auto behavior:
// the keyring is authoritative, a successful save removes the legacy file entry,
// and a fresh store reads the keyring without a file.
func TestOAuthStoreAutoUsesTheKeyringAndCleansTheFile(t *testing.T) {
	enableOAuthKeyring(t)
	home := t.TempDir()
	fake := newFakeKeyring()
	store := NewOAuthStore(home)
	store.Keyring = fake
	tokens := keyringTestTokens("docs", "https://example.com/mcp")

	// Seed the legacy file entry, then save through the keyring.
	fileStore := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile)
	if err := fileStore.Save(keyringTestTokens("docs", "https://example.com/old")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(tokens); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if len(fake.values) != 1 {
		t.Fatalf("keyring entries = %d, want 1", len(fake.values))
	}
	if _, err := os.Stat(filepath.Join(home, mcpOAuthFallbackFilename)); err != nil {
		t.Fatalf("keyring save wrote a credentials file: %v", err)
	}

	fresh := NewOAuthStore(home)
	fresh.Keyring = fake
	loaded, err := fresh.Load(tokens.ServerName, tokens.ServerURL)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != "access-token" || loaded.RefreshToken != "refresh-token" {
		t.Fatalf("loaded tokens = %#v", loaded)
	}

	removed, err := store.Delete(tokens.ServerName, tokens.ServerURL)
	if err != nil || !removed {
		t.Fatalf("Delete = %v, %v", removed, err)
	}
	if reloaded, err := fresh.Load(tokens.ServerName, tokens.ServerURL); err != nil || reloaded != nil {
		t.Fatalf("Load after Delete = %#v, %v", reloaded, err)
	}
}

// TestOAuthStoreAutoFallsBackToTheFile pins Rust's documented auto fallback: a
// keyring write failure falls back to the credentials file, and a keyring load
// miss/error reads the file.
func TestOAuthStoreAutoFallsBackToTheFile(t *testing.T) {
	enableOAuthKeyring(t)
	home := t.TempDir()
	fake := newFakeKeyring()
	fake.failSave = errors.New("keyring down")
	store := NewOAuthStore(home)
	store.Keyring = fake
	tokens := keyringTestTokens("docs", "https://example.com/mcp")
	if err := store.Save(tokens); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, mcpOAuthFallbackFilename)); err != nil {
		t.Fatalf("auto did not fall back to the credentials file: %v", err)
	}

	fake.failLoad = errors.New("keyring down")
	fresh := NewOAuthStore(home)
	fresh.Keyring = fake
	loaded, err := fresh.Load(tokens.ServerName, tokens.ServerURL)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != "access-token" {
		t.Fatalf("loaded tokens = %#v", loaded)
	}
}

// TestOAuthStoreKeyringModeRequiresTheKeyring pins the strict mode: with the
// backend available, keyring mode reads and writes only the keyring.
func TestOAuthStoreKeyringModeRequiresTheKeyring(t *testing.T) {
	enableOAuthKeyring(t)
	home := t.TempDir()
	fake := newFakeKeyring()
	store := NewOAuthStoreWithMode(home, OAuthCredentialsStoreKeyring)
	store.Keyring = fake
	if store.KeyringUnavailable() {
		t.Fatal("keyring mode reports unavailable while the backend is present")
	}
	// A legacy file entry must not satisfy keyring mode.
	if err := NewOAuthStoreWithMode(home, OAuthCredentialsStoreFile).Save(keyringTestTokens("docs", "https://example.com/mcp")); err != nil {
		t.Fatal(err)
	}
	if loaded, err := store.Load("docs", "https://example.com/mcp"); err != nil || loaded != nil {
		t.Fatalf("keyring mode loaded from the file: %#v, %v", loaded, err)
	}
	tokens := keyringTestTokens("docs", "https://example.com/mcp")
	if err := store.Save(tokens); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(tokens.ServerName, tokens.ServerURL)
	if err != nil || loaded == nil || loaded.AccessToken != "access-token" {
		t.Fatalf("loaded = %#v, %v", loaded, err)
	}
}

// TestOAuthStoreKeyringModeFailsWhenTheBackendErrors pins that keyring mode does
// not silently fall back to the file.
func TestOAuthStoreKeyringModeFailsWhenTheBackendErrors(t *testing.T) {
	enableOAuthKeyring(t)
	home := t.TempDir()
	fake := newFakeKeyring()
	fake.failSave = errors.New("keyring down")
	store := NewOAuthStoreWithMode(home, OAuthCredentialsStoreKeyring)
	store.Keyring = fake
	if err := store.Save(keyringTestTokens("docs", "https://example.com/mcp")); err == nil {
		t.Fatal("keyring mode must not fall back to the file")
	}
	if _, err := os.Stat(filepath.Join(home, mcpOAuthFallbackFilename)); !os.IsNotExist(err) {
		t.Fatalf("keyring mode wrote a credentials file: %v", err)
	}
}
