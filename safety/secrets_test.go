package safety

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/keyring"
)

func TestSecretNameValidation(t *testing.T) {
	if _, err := NewSecretName("GITHUB_TOKEN"); err != nil {
		t.Fatalf("NewSecretName() error = %v", err)
	}
	if _, err := NewSecretName("github-token"); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("NewSecretName(invalid) error = %v", err)
	}
}

func TestManagerRoundTripsLocalBackend(t *testing.T) {
	home := t.TempDir()
	manager := NewSecretManagerWithBackend(NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, newFakeKeyringStore()))
	name, err := NewSecretName("GITHUB_TOKEN")
	if err != nil {
		t.Fatalf("NewSecretName() error = %v", err)
	}
	scope := GlobalScope()
	if err := manager.Set(&scope, name, "token-1"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value, ok, err := manager.Get(&scope, name)
	if err != nil || !ok || value != "token-1" {
		t.Fatalf("Get() = %q, %v, %v", value, ok, err)
	}
	entries, err := manager.List(nil)
	if err != nil || len(entries) != 1 || entries[0].Name.String() != "GITHUB_TOKEN" {
		t.Fatalf("List() = %+v, %v", entries, err)
	}
	deleted, err := manager.Delete(&scope, name)
	if err != nil || !deleted {
		t.Fatalf("Delete() = %v, %v", deleted, err)
	}
}

func TestEnvironmentIDAndKeyringAccount(t *testing.T) {
	dir := t.TempDir()
	if got := EnvironmentIDFromCWD(dir); got == "" {
		t.Fatalf("EnvironmentIDFromCWD() empty")
	}
	account := ComputeKeyringAccount(dir)
	if !strings.HasPrefix(account, "secrets|") || len(account) != len("secrets|")+16 {
		t.Fatalf("ComputeKeyringAccount() = %q", account)
	}
}

func TestRedactSecrets(t *testing.T) {
	input := "api_key=123456789 token:abcdefghij Bearer abcdefghijklmnop sk-abcdefghijklmnopqrstuvwxyz"
	got := RedactSecrets(input)
	if strings.Contains(got, "123456789") || strings.Contains(got, "abcdefghijklmnop") || strings.Contains(got, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("RedactSecrets() leaked input: %q", got)
	}
}

// fakeKeyringStore is an in-memory keyring so tests never touch the OS store.
type fakeKeyringStore struct {
	values map[string]string
}

func newFakeKeyringStore() *fakeKeyringStore {
	return &fakeKeyringStore{values: map[string]string{}}
}

func (s *fakeKeyringStore) key(service string, account string) string {
	return service + "\x00" + account
}

func (s *fakeKeyringStore) Load(service string, account string) (string, error) {
	value, ok := s.values[s.key(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (s *fakeKeyringStore) Save(service string, account string, secret string) error {
	s.values[s.key(service, account)] = secret
	return nil
}

func (s *fakeKeyringStore) Delete(service string, account string) (bool, error) {
	key := s.key(service, account)
	if _, ok := s.values[key]; !ok {
		return false, nil
	}
	delete(s.values, key)
	return true, nil
}

func newTestLocalBackend(t *testing.T, home string, namespace LocalNamespace) *LocalBackend {
	t.Helper()
	return NewLocalSecretBackendWithKeyring(home, namespace, newFakeKeyringStore())
}

// TestLocalSecretBackendEncryptsLikeRust mirrors Rust #46318's local backend: the
// namespace file is age-encrypted with a keyring-held passphrase, so the stored
// bytes never contain the plaintext secret.
func TestLocalSecretBackendEncryptsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := newFakeKeyringStore()
	backend := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, store)
	name, err := NewSecretName("GITHUB_TOKEN")
	if err != nil {
		t.Fatalf("NewSecretName() error = %v", err)
	}
	scope := GlobalScope()
	if err := backend.Set(&scope, name, "token-1"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	path := filepath.Join(home, "secrets", string(LocalNamespaceManaged))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if bytes.Contains(data, []byte("token-1")) || bytes.Contains(data, []byte("GITHUB_TOKEN")) {
		t.Fatalf("stored file leaked plaintext: %q", data)
	}
	if !bytes.HasPrefix(data, []byte("age-encryption.org/")) {
		t.Fatalf("stored file is not an age file: %q", data[:min(len(data), 32)])
	}
	// Rust encrypts with age's scrypt recipient, so the file carries a scrypt
	// stanza; matching the stanza keeps the two implementations interoperable at
	// the format level.
	if !bytes.Contains(data, []byte("-> scrypt ")) {
		t.Fatalf("stored file has no scrypt stanza: %q", data[:min(len(data), 128)])
	}
	value, ok, err := backend.Get(&scope, name)
	if err != nil || !ok || value != "token-1" {
		t.Fatalf("Get() = %q, %v, %v", value, ok, err)
	}
	// The passphrase is held in the keyring under the namespace account.
	account := SecretKeyringAccount(home, LocalNamespaceManaged)
	passphrase, err := store.Load(KeyringService(), account)
	if err != nil || passphrase == "" {
		t.Fatalf("keyring passphrase = %q, %v", passphrase, err)
	}
	// Entries survive an unrelated process re-reading the file.
	reopened := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, store)
	value, ok, err = reopened.Get(&scope, name)
	if err != nil || !ok || value != "token-1" {
		t.Fatalf("reopened Get() = %q, %v, %v", value, ok, err)
	}
	entries, err := reopened.List(nil)
	if err != nil || len(entries) != 1 || entries[0].Name.String() != "GITHUB_TOKEN" {
		t.Fatalf("List() = %+v, %v", entries, err)
	}
}

// TestLocalSecretNamespacesAreIndependentLikeRust covers Rust's independent
// gateway namespace: its own file, its own keyring key, and an isolated map.
func TestLocalSecretNamespacesAreIndependentLikeRust(t *testing.T) {
	home := t.TempDir()
	store := newFakeKeyringStore()
	managed := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, store)
	gateway := NewLocalSecretBackendWithKeyring(home, LocalNamespaceGatewayOAuth, store)
	name, err := NewSecretName("PROVIDER_OAUTH_ABC")
	if err != nil {
		t.Fatalf("NewSecretName() error = %v", err)
	}
	scope := GlobalScope()
	if err := managed.Set(&scope, name, "managed-value"); err != nil {
		t.Fatalf("managed Set() error = %v", err)
	}
	if err := gateway.Set(&scope, name, "gateway-value"); err != nil {
		t.Fatalf("gateway Set() error = %v", err)
	}
	if value, ok, _ := managed.Get(&scope, name); !ok || value != "managed-value" {
		t.Fatalf("managed value = %q, %v", value, ok)
	}
	if value, ok, _ := gateway.Get(&scope, name); !ok || value != "gateway-value" {
		t.Fatalf("gateway value = %q, %v", value, ok)
	}
	if !strings.HasSuffix(SecretKeyringAccount(home, LocalNamespaceGatewayOAuth), "|gateway-oauth") {
		t.Fatalf("gateway account = %q", SecretKeyringAccount(home, LocalNamespaceGatewayOAuth))
	}
	if SecretKeyringAccount(home, LocalNamespaceGatewayOAuth) == SecretKeyringAccount(home, LocalNamespaceManaged) {
		t.Fatal("gateway credentials must use an independent keyring key")
	}
	// Deleting the gateway secret leaves the managed entry untouched.
	if deleted, err := gateway.Delete(&scope, name); err != nil || !deleted {
		t.Fatalf("gateway Delete() = %v, %v", deleted, err)
	}
	if value, ok, _ := managed.Get(&scope, name); !ok || value != "managed-value" {
		t.Fatalf("managed value after gateway delete = %q, %v", value, ok)
	}
	for _, filename := range []string{string(LocalNamespaceManaged), string(LocalNamespaceGatewayOAuth)} {
		if _, err := os.Stat(filepath.Join(home, "secrets", filename)); err != nil {
			t.Fatalf("namespace file %s missing: %v", filename, err)
		}
	}
}

// TestLocalSecretBackendRejectsInvalidDocumentsLikeRust covers the empty-value
// guard and the newer-version rejection.
func TestLocalSecretBackendRejectsInvalidDocumentsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := newFakeKeyringStore()
	backend := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, store)
	name, err := NewSecretName("GITHUB_TOKEN")
	if err != nil {
		t.Fatalf("NewSecretName() error = %v", err)
	}
	scope := GlobalScope()
	if err := backend.Set(&scope, name, "  "); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("empty Set() error = %v, want ErrInvalidSecret", err)
	}

	passphrase, err := backend.loadOrCreatePassphrase()
	if err != nil {
		t.Fatalf("loadOrCreatePassphrase() error = %v", err)
	}
	ciphertext, err := encryptWithPassphrase([]byte(`{"version":2,"secrets":{"global/GITHUB_TOKEN":"x"}}`), passphrase)
	if err != nil {
		t.Fatalf("encryptWithPassphrase() error = %v", err)
	}
	dir := filepath.Join(home, "secrets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, string(LocalNamespaceManaged)), ciphertext, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := backend.load(); err == nil || !strings.Contains(err.Error(), "newer than supported version") {
		t.Fatalf("load() error = %v, want the version rejection", err)
	}
}
