package safety

import (
	"errors"
	"strings"
	"testing"
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
	manager := NewSecretManager(t.TempDir(), SecretBackendLocal)
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
