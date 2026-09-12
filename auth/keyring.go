package auth

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrKeyringSecretNotFound = errors.New("secret not found")

// OSKeyringAvailable reports whether this build has a durable OS keyring
// backend. Go does not link one: KeyringStore is an in-process map used as a
// seam for tests, so production keyring storage cannot persist credentials.
// Rust's credentials-store modes fall back to the file when keyring storage is
// unavailable (auto) and fail when it is required (keyring), which is what the
// auth storage backends do here.
const OSKeyringAvailable = false

// KeyringUnavailableError reports that keyring-backed credentials were
// required but no durable keyring backend exists in this build.
const KeyringUnavailableError = "OS keyring storage is unavailable; set cli_auth_credentials_store = \"file\" or \"auto\""

type KeyringBackendKind string

const (
	KeyringBackendDirect  KeyringBackendKind = "direct"
	KeyringBackendSecrets KeyringBackendKind = "secrets"
	KeyringBackendKeyring KeyringBackendKind = "keyring"
	KeyringBackendAuto    KeyringBackendKind = "auto"
)

func ResolveKeyringBackendFromSecretAuthStorage(enabled bool) KeyringBackendKind {
	if enabled {
		return KeyringBackendSecrets
	}
	return KeyringBackendDirect
}

type KeyringStore struct {
	backend KeyringBackendKind
	values  map[string]string
}

var defaultKeyringValues = struct {
	sync.Mutex
	values map[string]string
}{values: map[string]string{}}

func NewKeyringStore(backend KeyringBackendKind) *KeyringStore {
	if backend == "" || backend == KeyringBackendAuto {
		backend = KeyringBackendDirect
	}
	return &KeyringStore{backend: backend, values: defaultKeyringValues.values}
}

func (s *KeyringStore) Backend() KeyringBackendKind {
	if s == nil {
		return KeyringBackendDirect
	}
	return s.backend
}

func (s *KeyringStore) Set(service string, account string, secret string) error {
	if s == nil {
		return fmt.Errorf("keyring store is nil")
	}
	key := secretKey(service, account)
	if key == "" {
		return fmt.Errorf("service and account are required")
	}
	defaultKeyringValues.Lock()
	defer defaultKeyringValues.Unlock()
	s.values[key] = secret
	return nil
}

func (s *KeyringStore) Get(service string, account string) (string, error) {
	if s == nil {
		return "", ErrKeyringSecretNotFound
	}
	key := secretKey(service, account)
	defaultKeyringValues.Lock()
	defer defaultKeyringValues.Unlock()
	value, ok := s.values[key]
	if !ok {
		return "", ErrKeyringSecretNotFound
	}
	return value, nil
}

func (s *KeyringStore) Delete(service string, account string) (bool, error) {
	if s == nil {
		return false, nil
	}
	key := secretKey(service, account)
	defaultKeyringValues.Lock()
	defer defaultKeyringValues.Unlock()
	_, existed := s.values[key]
	delete(s.values, key)
	return existed, nil
}

func secretKey(service string, account string) string {
	service = strings.TrimSpace(service)
	account = strings.TrimSpace(account)
	if service == "" || account == "" {
		return ""
	}
	return service + "\x00" + account
}
