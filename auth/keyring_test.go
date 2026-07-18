package auth

import (
	"errors"
	"testing"
)

func TestResolveKeyringBackendFromSecretAuthStorage(t *testing.T) {
	if ResolveKeyringBackendFromSecretAuthStorage(true) != KeyringBackendSecrets {
		t.Fatalf("enabled should select secrets")
	}
	if ResolveKeyringBackendFromSecretAuthStorage(false) != KeyringBackendDirect {
		t.Fatalf("disabled should select direct")
	}
}

func TestKeyringStoreSetGetDelete(t *testing.T) {
	store := NewKeyringStore(KeyringBackendSecrets)
	if store.Backend() != KeyringBackendSecrets {
		t.Fatalf("backend = %s", store.Backend())
	}
	if err := store.Set("codex", "auth", "secret"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value, err := store.Get("codex", "auth")
	if err != nil || value != "secret" {
		t.Fatalf("Get() = %q, %v", value, err)
	}
	removed, err := store.Delete("codex", "auth")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !removed {
		t.Fatal("Delete() removed = false, want true")
	}
	if _, err := store.Get("codex", "auth"); !errors.Is(err, ErrKeyringSecretNotFound) {
		t.Fatalf("expected ErrKeyringSecretNotFound, got %v", err)
	}
}
