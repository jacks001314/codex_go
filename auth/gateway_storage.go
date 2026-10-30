package auth

// Gateway credential storage and the cross-process exchange lock.
//
// Rust parity: codex-rs/login/src/gateway_auth_storage.rs. Credentials live in
// the dedicated encrypted gateway namespace; configurations have separate
// entries but rewrite the same encrypted file, so a stable sidecar lock is held
// from the initial read through token exchange and save.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/keyring"
	"codex_go/safety"
	"codex_go/utils"
)

const (
	gatewayCredentialLockWait   = 60 * time.Second
	gatewayCredentialLockRetry  = 50 * time.Millisecond
	gatewayCredentialLockName   = "gateway_oauth.lock"
	gatewayCredentialIDPrefix   = "provider-oauth|"
	gatewayCredentialSecretBase = "PROVIDER_OAUTH_"
)

// gatewayCredentialID mirrors Rust `GatewayAuthManager::credential_id`: one
// entry per codex home, issuer, client, resource, and scope set.
func gatewayCredentialID(codexHome string, config GatewayAuthConfig) string {
	digest := sha256.New()
	digest.Write([]byte(codexHome))
	digest.Write([]byte{0})
	for _, value := range []string{config.AuthorizationURL, config.TokenURL, config.ClientID, config.Resource} {
		digest.Write([]byte(value))
		digest.Write([]byte{0})
	}
	for _, scope := range config.Scopes {
		digest.Write([]byte(scope))
		digest.Write([]byte{0})
	}
	return fmt.Sprintf("%s%x", gatewayCredentialIDPrefix, digest.Sum(nil))
}

// gatewayCredentialSecretName mirrors Rust `storage::secret_name`: the account
// digest becomes the `PROVIDER_OAUTH_<DIGEST>` secret name, and anything that is
// not a provider OAuth account id is rejected.
func gatewayCredentialSecretName(credentialID string) (*safety.SecretName, error) {
	if !strings.HasPrefix(credentialID, gatewayCredentialIDPrefix) {
		return nil, errors.New("invalid provider OAuth credential account")
	}
	digest := strings.TrimPrefix(credentialID, gatewayCredentialIDPrefix)
	if digest == "" {
		return nil, errors.New("invalid provider OAuth credential account")
	}
	name, err := safety.NewSecretName(gatewayCredentialSecretBase + strings.ToUpper(digest))
	if err != nil {
		return nil, errors.New("invalid provider OAuth credential account")
	}
	return name, nil
}

// gatewayAuthStorage reads and writes gateway credentials in their own encrypted
// namespace with an independent keyring key, or in a single JSON file when the
// provider selects the file-backed store (Rust #58219 OAuthCredentialsStore).
type gatewayAuthStorage struct {
	backend *safety.LocalBackend
	// filePath is set for the file-backed store; the credential entries share
	// one JSON object keyed by credential id.
	filePath string
}

func newGatewayAuthStorage(codexHome string, store keyring.Store, credentialsStore ...string) *gatewayAuthStorage {
	mode := ""
	if len(credentialsStore) > 0 {
		mode = strings.TrimSpace(credentialsStore[0])
	}
	if mode == "file" {
		return &gatewayAuthStorage{filePath: filepath.Join(codexHome, "secrets", "gateway_oauth.json")}
	}
	return &gatewayAuthStorage{
		backend: safety.NewLocalSecretBackendWithKeyring(codexHome, safety.LocalNamespaceGatewayOAuth, store),
	}
}

// readGatewayTokenFile returns the stored credential entries, treating a missing
// file as an empty store. Deserialization errors never surface credential text.
func readGatewayTokenFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	entries := map[string]string{}
	if len(bytes.TrimSpace(data)) == 0 {
		return entries, nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, errors.New("stored provider OAuth credentials are invalid")
	}
	return entries, nil
}

func writeGatewayTokenFile(path string, entries map[string]string) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return utils.WriteAtomically(path, string(data))
}

func (s *gatewayAuthStorage) load(credentialID string) (string, bool, error) {
	if s.filePath != "" {
		if _, err := gatewayCredentialSecretName(credentialID); err != nil {
			return "", false, err
		}
		entries, err := readGatewayTokenFile(s.filePath)
		if err != nil {
			return "", false, errors.New("failed to load provider OAuth credentials")
		}
		value, ok := entries[credentialID]
		return value, ok, nil
	}
	name, err := gatewayCredentialSecretName(credentialID)
	if err != nil {
		return "", false, err
	}
	scope := safety.GlobalScope()
	value, ok, err := s.backend.Get(&scope, name)
	if err != nil {
		return "", false, errors.New("failed to load provider OAuth credentials")
	}
	return value, ok, nil
}

func (s *gatewayAuthStorage) save(credentialID string, value string) error {
	if s.filePath != "" {
		if _, err := gatewayCredentialSecretName(credentialID); err != nil {
			return err
		}
		entries, err := readGatewayTokenFile(s.filePath)
		if err != nil {
			return errors.New("failed to save provider OAuth credentials")
		}
		entries[credentialID] = value
		if err := writeGatewayTokenFile(s.filePath, entries); err != nil {
			return errors.New("failed to save provider OAuth credentials")
		}
		return nil
	}
	name, err := gatewayCredentialSecretName(credentialID)
	if err != nil {
		return err
	}
	scope := safety.GlobalScope()
	if err := s.backend.Set(&scope, name, value); err != nil {
		return errors.New("failed to save provider OAuth credentials")
	}
	return nil
}

// delete removes the persisted grant and reports whether one was present
// (Rust OAuthSessionManager::logout).
func (s *gatewayAuthStorage) delete(credentialID string) (bool, error) {
	if s.filePath != "" {
		if _, err := gatewayCredentialSecretName(credentialID); err != nil {
			return false, err
		}
		entries, err := readGatewayTokenFile(s.filePath)
		if err != nil {
			return false, errors.New("failed to remove provider OAuth credentials")
		}
		if _, ok := entries[credentialID]; !ok {
			return false, nil
		}
		delete(entries, credentialID)
		if err := writeGatewayTokenFile(s.filePath, entries); err != nil {
			return false, errors.New("failed to remove provider OAuth credentials")
		}
		return true, nil
	}
	name, err := gatewayCredentialSecretName(credentialID)
	if err != nil {
		return false, err
	}
	scope := safety.GlobalScope()
	removed, err := s.backend.Delete(&scope, name)
	if err != nil {
		return false, errors.New("failed to remove provider OAuth credentials")
	}
	return removed, nil
}

// lockGatewayCredentials mirrors Rust `storage::lock_credentials`: one stable
// sidecar lock, retried every 50 ms for up to 60 s, so a concurrent
// configuration cannot rewrite the encrypted file mid-exchange.
func lockGatewayCredentials(codexHome string) (*gatewayCredentialLock, error) {
	directory := filepath.Join(codexHome, "secrets")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create provider OAuth lock directory %s: %w", directory, err)
	}
	path := filepath.Join(directory, gatewayCredentialLockName)
	deadline := time.Now().Add(gatewayCredentialLockWait)
	for {
		lock, acquired, err := tryAcquireGatewayCredentialLock(path)
		if err != nil {
			return nil, err
		}
		if acquired {
			return lock, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for provider OAuth credentials")
		}
		time.Sleep(gatewayCredentialLockRetry)
	}
}

func openGatewayCredentialLockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open provider OAuth lock file %s: %w", path, err)
	}
	return file, nil
}
