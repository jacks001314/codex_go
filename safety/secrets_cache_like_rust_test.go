package safety

// Rust parity: #56081 caches decrypted Codex auth and managed secrets. Each
// non-gateway namespace keeps at most one snapshot process-wide; gateway
// snapshots stay with their backend. A snapshot is reused only while the file
// path, ciphertext and keyring passphrase still match, and writes invalidate it.

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var secretsCacheTestLock sync.Mutex

type decryptCounter struct {
	mu    sync.Mutex
	calls int
}

func (c *decryptCounter) record() {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
}

func (c *decryptCounter) value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// countDecryptions replaces the scrypt decryption seam for one test.
func countDecryptions(t *testing.T) *decryptCounter {
	t.Helper()
	counter := &decryptCounter{}
	previous := decryptSecrets
	decryptSecrets = func(ciphertext []byte, passphrase string) ([]byte, error) {
		counter.record()
		return previous(ciphertext, passphrase)
	}
	t.Cleanup(func() { decryptSecrets = previous })
	return counter
}

func writeCachedTestDocument(t *testing.T, home string, namespace LocalNamespace, store *fakeKeyringStore, document string) {
	t.Helper()
	passphrase, err := store.Load(KeyringService(), SecretKeyringAccount(home, namespace))
	if err != nil {
		t.Fatalf("load passphrase: %v", err)
	}
	ciphertext, err := encryptWithPassphrase([]byte(document), passphrase)
	if err != nil {
		t.Fatalf("encrypt document: %v", err)
	}
	dir := filepath.Join(home, "secrets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir secrets: %v", err)
	}
	if err := writeSecretsFileAtomically(filepath.Join(dir, string(namespace)), ciphertext); err != nil {
		t.Fatalf("write secrets file: %v", err)
	}
}

func TestLocalSecretCacheReusesPlaintextLikeRust(t *testing.T) {
	secretsCacheTestLock.Lock()
	defer secretsCacheTestLock.Unlock()
	counter := countDecryptions(t)
	scope := GlobalScope()
	name, err := NewSecretName("TEST_SECRET")
	if err != nil {
		t.Fatalf("NewSecretName: %v", err)
	}

	for _, namespace := range []LocalNamespace{LocalNamespaceManaged, LocalNamespaceCodexAuth, LocalNamespaceMCPOAuth} {
		t.Run(string(namespace), func(t *testing.T) {
			home := t.TempDir()
			store := newFakeKeyringStore()
			first := NewLocalSecretBackendWithKeyring(home, namespace, store)
			second := NewLocalSecretBackendWithKeyring(home, namespace, store)
			if err := first.Set(&scope, name, "one"); err != nil {
				t.Fatalf("Set: %v", err)
			}
			before := counter.value()

			var wait sync.WaitGroup
			values := make([]string, 2)
			for index, backend := range []*LocalBackend{first, second} {
				wait.Add(1)
				go func(index int, backend *LocalBackend) {
					defer wait.Done()
					value, ok, err := backend.Get(&scope, name)
					if err != nil || !ok {
						t.Errorf("concurrent Get = %q, %v, %v", value, ok, err)
						return
					}
					values[index] = value
				}(index, backend)
			}
			wait.Wait()
			if values[0] != "one" || values[1] != "one" {
				t.Fatalf("concurrent values = %#v", values)
			}
			if got := counter.value() - before; got != 1 {
				t.Fatalf("concurrent decryptions = %d, want 1", got)
			}
			if value, ok, err := second.Get(&scope, name); err != nil || !ok || value != "one" {
				t.Fatalf("cached Get = %q, %v, %v", value, ok, err)
			}
			if got := counter.value() - before; got != 1 {
				t.Fatalf("decryptions after cached read = %d, want 1", got)
			}

			// A write invalidates the snapshot so the next read sees "two".
			if err := first.Set(&scope, name, "two"); err != nil {
				t.Fatalf("Set(two): %v", err)
			}
			if value, ok, err := second.Get(&scope, name); err != nil || !ok || value != "two" {
				t.Fatalf("Get after write = %q, %v, %v", value, ok, err)
			}
			if got := counter.value() - before; got != 2 {
				t.Fatalf("decryptions after write = %d, want 2", got)
			}
		})
	}
}

func TestLocalGatewayCacheIsScopedToBackendLikeRust(t *testing.T) {
	secretsCacheTestLock.Lock()
	defer secretsCacheTestLock.Unlock()
	counter := countDecryptions(t)
	home := t.TempDir()
	store := newFakeKeyringStore()
	scope := GlobalScope()
	name, err := NewSecretName("TEST_SECRET")
	if err != nil {
		t.Fatalf("NewSecretName: %v", err)
	}
	first := NewLocalSecretBackendWithKeyring(home, LocalNamespaceGatewayOAuth, store)
	second := NewLocalSecretBackendWithKeyring(home, LocalNamespaceGatewayOAuth, store)
	if err := first.Set(&scope, name, "gateway"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	before := counter.value()
	for range 2 {
		if value, ok, err := first.Get(&scope, name); err != nil || !ok || value != "gateway" {
			t.Fatalf("first Get = %q, %v, %v", value, ok, err)
		}
	}
	if got := counter.value() - before; got != 1 {
		t.Fatalf("first backend decryptions = %d, want 1", got)
	}
	// An independently constructed backend keeps its own snapshot.
	if value, ok, err := second.Get(&scope, name); err != nil || !ok || value != "gateway" {
		t.Fatalf("second Get = %q, %v, %v", value, ok, err)
	}
	if got := counter.value() - before; got != 2 {
		t.Fatalf("two backend decryptions = %d, want 2", got)
	}
}

func TestLocalSecretCacheDetectsExternalChangesLikeRust(t *testing.T) {
	secretsCacheTestLock.Lock()
	defer secretsCacheTestLock.Unlock()
	counter := countDecryptions(t)
	home := t.TempDir()
	store := newFakeKeyringStore()
	namespace := LocalNamespaceManaged
	backend := NewLocalSecretBackendWithKeyring(home, namespace, store)
	scope := GlobalScope()
	name, err := NewSecretName("TEST_SECRET")
	if err != nil {
		t.Fatalf("NewSecretName: %v", err)
	}
	if err := backend.Set(&scope, name, "one"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	before := counter.value()
	if _, ok, err := backend.Get(&scope, name); err != nil || !ok {
		t.Fatalf("prime Get = %v, %v", ok, err)
	}
	if got := counter.value() - before; got != 1 {
		t.Fatalf("prime decryptions = %d, want 1", got)
	}

	// External replacement with the same passphrase changes the ciphertext hash.
	writeCachedTestDocument(t, home, namespace, store, `{"version":1,"secrets":{"global/TEST_SECRET":"two"}}`)
	if value, ok, err := backend.Get(&scope, name); err != nil || !ok || value != "two" {
		t.Fatalf("Get after replacement = %q, %v, %v", value, ok, err)
	}
	if got := counter.value() - before; got != 2 {
		t.Fatalf("decryptions after replacement = %d, want 2", got)
	}

	// File removal is observed even with a matching snapshot in memory.
	path := filepath.Join(home, "secrets", string(namespace))
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove secrets file: %v", err)
	}
	if _, ok, err := backend.Get(&scope, name); err != nil || ok {
		t.Fatalf("Get after removal = %v, %v, want no entry", ok, err)
	}
	if got := counter.value() - before; got != 2 {
		t.Fatalf("decryptions after removal = %d, want 2", got)
	}

	// Invalid ciphertext is an error, never the previously cached plaintext.
	if err := os.WriteFile(path, []byte("not an age file"), 0o600); err != nil {
		t.Fatalf("write invalid ciphertext: %v", err)
	}
	if _, _, err := backend.Get(&scope, name); err == nil {
		t.Fatal("invalid ciphertext reused the cached plaintext")
	}

	// Key rotation re-encrypts with a new keyring passphrase and is observed.
	rotated := "rotated-passphrase"
	if err := store.Save(KeyringService(), SecretKeyringAccount(home, namespace), rotated); err != nil {
		t.Fatalf("save rotated key: %v", err)
	}
	ciphertext, err := encryptWithPassphrase([]byte(`{"version":1,"secrets":{"global/TEST_SECRET":"rotated"}}`), rotated)
	if err != nil {
		t.Fatalf("encrypt rotated document: %v", err)
	}
	if err := writeSecretsFileAtomically(path, ciphertext); err != nil {
		t.Fatalf("write rotated document: %v", err)
	}
	if value, ok, err := backend.Get(&scope, name); err != nil || !ok || value != "rotated" {
		t.Fatalf("Get after rotation = %q, %v, %v", value, ok, err)
	}
}

type failingKeyringStore struct {
	*fakeKeyringStore
}

func (s *failingKeyringStore) Load(string, string) (string, error) {
	return "", errors.New("keyring unavailable")
}

func TestLocalSecretCacheStillChecksKeyringLikeRust(t *testing.T) {
	secretsCacheTestLock.Lock()
	defer secretsCacheTestLock.Unlock()
	home := t.TempDir()
	store := newFakeKeyringStore()
	backend := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, store)
	scope := GlobalScope()
	name, err := NewSecretName("TEST_SECRET")
	if err != nil {
		t.Fatalf("NewSecretName: %v", err)
	}
	if err := backend.Set(&scope, name, "one"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, ok, err := backend.Get(&scope, name); err != nil || !ok {
		t.Fatalf("prime Get = %v, %v", ok, err)
	}
	broken := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, &failingKeyringStore{fakeKeyringStore: store})
	if _, _, err := broken.Get(&scope, name); err == nil {
		t.Fatal("cached plaintext was served while the keyring failed")
	}
}

func TestLocalSecretCacheKeepsOneSnapshotPerNamespaceLikeRust(t *testing.T) {
	secretsCacheTestLock.Lock()
	defer secretsCacheTestLock.Unlock()
	counter := countDecryptions(t)
	scope := GlobalScope()
	name, err := NewSecretName("TEST_SECRET")
	if err != nil {
		t.Fatalf("NewSecretName: %v", err)
	}
	firstHome := t.TempDir()
	secondHome := t.TempDir()
	firstStore := newFakeKeyringStore()
	secondStore := newFakeKeyringStore()
	first := NewLocalSecretBackendWithKeyring(firstHome, LocalNamespaceManaged, firstStore)
	second := NewLocalSecretBackendWithKeyring(secondHome, LocalNamespaceManaged, secondStore)
	if err := first.Set(&scope, name, "first"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := second.Set(&scope, name, "second"); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	before := counter.value()
	if value, _, _ := first.Get(&scope, name); value != "first" {
		t.Fatalf("first value = %q", value)
	}
	if value, _, _ := second.Get(&scope, name); value != "second" {
		t.Fatalf("second value = %q", value)
	}
	// Returning to the first home must re-decrypt because one snapshot per
	// namespace means the second read replaced it.
	if value, _, _ := first.Get(&scope, name); value != "first" {
		t.Fatalf("first value after second home = %q", value)
	}
	if got := counter.value() - before; got != 3 {
		t.Fatalf("decryptions across homes = %d, want 3", got)
	}

	// Namespaces never share a snapshot even for the same home.
	gateway := NewLocalSecretBackendWithKeyring(firstHome, LocalNamespaceGatewayOAuth, firstStore)
	if err := gateway.Set(&scope, name, "gateway"); err != nil {
		t.Fatalf("gateway Set: %v", err)
	}
	before = counter.value()
	if value, _, _ := first.Get(&scope, name); value != "first" {
		t.Fatalf("managed value = %q", value)
	}
	if value, _, _ := gateway.Get(&scope, name); value != "gateway" {
		t.Fatalf("gateway value = %q", value)
	}
	// The managed snapshot is still live, so only the gateway decrypted.
	if got := counter.value() - before; got != 1 {
		t.Fatalf("cross-namespace decryptions = %d, want 1", got)
	}
	if value, _, _ := first.Get(&scope, name); value != "first" {
		t.Fatalf("managed value after gateway read = %q", value)
	}
	if got := counter.value() - before; got != 1 {
		t.Fatalf("managed snapshot was evicted by the gateway read: %d decryptions", got)
	}
}
