package auth

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"codex_go/keyring"
)

var ErrKeyringSecretNotFound = errors.New("secret not found")

// OSKeyringAvailable reports whether this build has a durable OS keyring
// backend (Windows Credential Manager). Rust's credentials-store modes fall back
// to the file when keyring storage is unavailable (auto) and fail when it is
// required (keyring), which is what the auth storage backends do here. It is a
// var so tests can pin the unavailable behavior deterministically.
var OSKeyringAvailable = keyring.Available()

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
	// osStore, when set, is the durable platform backend. The in-process map
	// remains for injected test seams and hosts without a keyring.
	osStore keyring.Store
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

// NewOSKeyringStore returns a store backed by the durable platform keyring.
// Go does not model Rust's aggregate "secrets" namespace, so both keyring
// backends resolve to the OS keyring here.
func NewOSKeyringStore(backend KeyringBackendKind) *KeyringStore {
	if backend == "" || backend == KeyringBackendAuto {
		backend = KeyringBackendDirect
	}
	return &KeyringStore{backend: backend, osStore: keyring.New()}
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
	if s.osStore != nil {
		return s.osStore.Save(service, account, secret)
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
	if s.osStore != nil {
		value, err := s.osStore.Load(service, account)
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrKeyringSecretNotFound
		}
		return value, err
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
	if s.osStore != nil {
		return s.osStore.Delete(service, account)
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
