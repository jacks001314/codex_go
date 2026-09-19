package auth

// Gateway credential storage and the cross-process exchange lock.
//
// Rust parity: codex-rs/login/src/gateway_auth_storage.rs. Credentials live in
// the dedicated encrypted gateway namespace; configurations have separate
// entries but rewrite the same encrypted file, so a stable sidecar lock is held
// from the initial read through token exchange and save.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/keyring"
	"codex_go/safety"
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
func gatewayCredentialID(codexHome string, config gatewayAuthConfig) string {
	digest := sha256.New()
	digest.Write([]byte(codexHome))
	digest.Write([]byte{0})
	for _, value := range []string{config.authorizationURL, config.tokenURL, config.clientID, config.resource} {
		digest.Write([]byte(value))
		digest.Write([]byte{0})
	}
	for _, scope := range config.scopes {
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
// namespace with an independent keyring key.
type gatewayAuthStorage struct {
	backend *safety.LocalBackend
}

func newGatewayAuthStorage(codexHome string, store keyring.Store) *gatewayAuthStorage {
	return &gatewayAuthStorage{
		backend: safety.NewLocalSecretBackendWithKeyring(codexHome, safety.LocalNamespaceGatewayOAuth, store),
	}
}

func (s *gatewayAuthStorage) load(credentialID string) (string, bool, error) {
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

