package safety

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingKeyringStore counts keyring traffic so tests can prove an update
// reuses the passphrase it already loaded (Rust #61401).
type recordingKeyringStore struct {
	*fakeKeyringStore
	loads int
	saves int
}

func newRecordingKeyringStore() *recordingKeyringStore {
	return &recordingKeyringStore{fakeKeyringStore: newFakeKeyringStore()}
}

func (s *recordingKeyringStore) Load(service string, account string) (string, error) {
	s.loads++
	return s.fakeKeyringStore.Load(service, account)
}

func (s *recordingKeyringStore) Save(service string, account string, secret string) error {
	s.saves++
	return s.fakeKeyringStore.Save(service, account, secret)
}

func TestLocalSecretUpdateLoadsKeyringOnceLikeRust(t *testing.T) {
	home := t.TempDir()
	store := newRecordingKeyringStore()
	backend := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, store)
	scope := GlobalScope()
	first, _ := NewSecretName("FIRST_TOKEN")
	second, _ := NewSecretName("SECOND_TOKEN")

	if err := backend.Set(&scope, first, "one"); err != nil {
		t.Fatalf("first Set() error = %v", err)
	}
	store.loads, store.saves = 0, 0
	if err := backend.Set(&scope, second, "two"); err != nil {
		t.Fatalf("second Set() error = %v", err)
	}
	if store.loads != 1 || store.saves != 0 {
		t.Fatalf("update keyring traffic = loads %d saves %d, want one load and no save", store.loads, store.saves)
	}
	for name, want := range map[*SecretName]string{first: "one", second: "two"} {
		value, ok, err := backend.Get(&scope, name)
		if err != nil || !ok || value != want {
			t.Fatalf("Get(%s) = %q, %v, %v; want %q", name.String(), value, ok, err, want)
		}
	}
}

func TestLocalSecretExistingFileRequiresKeyringPassphraseLikeRust(t *testing.T) {
	home := t.TempDir()
	store := newRecordingKeyringStore()
	backend := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, store)
	scope := GlobalScope()
	name, _ := NewSecretName("GITHUB_TOKEN")
	if err := backend.Set(&scope, name, "token-1"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	path := filepath.Join(home, "secrets", string(LocalNamespaceManaged))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	account := SecretKeyringAccount(home, LocalNamespaceManaged)
	if _, err := store.Delete(KeyringService(), account); err != nil {
		t.Fatalf("Delete keyring entry error = %v", err)
	}
	store.loads, store.saves = 0, 0

	reopened := NewLocalSecretBackendWithKeyring(home, LocalNamespaceManaged, store)
	if _, _, err := reopened.Get(&scope, name); err == nil || !strings.Contains(err.Error(), "missing keyring passphrase") {
		t.Fatalf("Get() error = %v, want missing passphrase", err)
	}
	if err := reopened.Set(&scope, name, "token-2"); err == nil || !strings.Contains(err.Error(), "missing keyring passphrase") {
		t.Fatalf("Set() error = %v, want missing passphrase", err)
	}
	if store.saves != 0 {
		t.Fatalf("a replacement passphrase was generated and saved: %d saves", store.saves)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() after update error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("encrypted secrets file changed without the original keyring passphrase")
	}
}

func TestLocalSecretDeleteWithoutFileSkipsKeyringLikeRust(t *testing.T) {
	store := newRecordingKeyringStore()
	backend := NewLocalSecretBackendWithKeyring(t.TempDir(), LocalNamespaceManaged, store)
	scope := GlobalScope()
	name, _ := NewSecretName("MISSING_TOKEN")

	deleted, err := backend.Delete(&scope, name)
	if err != nil || deleted {
		t.Fatalf("Delete() = %v, %v; want false, nil", deleted, err)
	}
	if store.loads != 0 || store.saves != 0 {
		t.Fatalf("Delete() keyring traffic = loads %d saves %d, want none", store.loads, store.saves)
	}
}
