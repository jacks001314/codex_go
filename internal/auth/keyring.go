package auth

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrKeyringSecretNotFound = errors.New("secret not found")

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
