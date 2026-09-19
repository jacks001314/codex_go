package safety

// Rust parity: codex-rs/secrets/src/local.rs (#46318). Namespaced secret files
// are age-encrypted with a scrypt recipient/identity derived from a passphrase
// held in the OS keyring, so secrets never reach disk in plaintext. The gateway
// namespace uses its own file and its own keyring key so a concurrent first
// write cannot overwrite the primary store's newly generated key.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"filippo.io/age"

	"codex_go/keyring"
)

// SecretsVersion is the on-disk schema version Rust supports.
const SecretsVersion = 1

// SecretKeyringAccount mirrors Rust compute_keyring_account: the passphrase for a
// local namespace lives in the `codex` keyring service under
// `secrets|<sha256(canonical home)[:16]>`, with an independent key for the
// gateway namespace.
func SecretKeyringAccount(codexHome string, namespace LocalNamespace) string {
	sum := sha256.Sum256([]byte(canonicalSecretsHomePath(codexHome)))
	account := "secrets|" + hex.EncodeToString(sum[:])[:16]
	if namespace == LocalNamespaceGatewayOAuth {
		return account + "|gateway-oauth"
	}
	return account
}

// canonicalSecretsHomePath mirrors Rust's `codex_home.canonicalize()`: the
// resolved absolute path, with the Windows verbatim prefix std canonicalize
// produces, falling back to the caller's value when the directory cannot be
// resolved.
func canonicalSecretsHomePath(codexHome string) string {
	abs, err := filepath.Abs(codexHome)
	if err != nil {
		return codexHome
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return codexHome
	}
	if runtime.GOOS == "windows" {
		return windowsVerbatimPath(canonical)
	}
	return canonical
}

func windowsVerbatimPath(path string) string {
	path = strings.TrimRight(path, `\/`)
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\\?\` + path
}

func (b *LocalBackend) keyringStore() keyring.Store {
	if b != nil && b.keyring != nil {
		return b.keyring
	}
	return keyring.New()
}

func (b *LocalBackend) loadOrCreatePassphrase() (string, error) {
	account := SecretKeyringAccount(b.codexHome, b.namespace)
	store := b.keyringStore()
	existing, err := store.Load(KeyringService(), account)
	switch {
	case err == nil:
		return existing, nil
	case errors.Is(err, keyring.ErrNotFound):
	default:
		return "", fmt.Errorf("failed to load secrets key from keyring for %s: %w", account, err)
	}
	// Generate a high-entropy key and persist it in the OS keyring so secrets
	// stay out of plaintext files while remaining fully local.
	generated, err := generateSecretsPassphrase()
	if err != nil {
		return "", err
	}
	if err := store.Save(KeyringService(), account, generated); err != nil {
		return "", fmt.Errorf("failed to persist secrets key in keyring: %w", err)
	}
	return generated, nil
}

func generateSecretsPassphrase() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to generate random secrets key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func encryptWithPassphrase(plaintext []byte, passphrase string) ([]byte, error) {
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to build secrets recipient: %w", err)
	}
	var buffer bytes.Buffer
	writer, err := age.Encrypt(&buffer, recipient)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt secrets file: %w", err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		return nil, fmt.Errorf("failed to encrypt secrets file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to encrypt secrets file: %w", err)
	}
	return buffer.Bytes(), nil
}

func decryptWithPassphrase(ciphertext []byte, passphrase string) ([]byte, error) {
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to build secrets identity: %w", err)
	}
	reader, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt secrets file: %w", err)
	}
	plaintext, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt secrets file: %w", err)
	}
	return plaintext, nil
}

// writeSecretsFileAtomically writes the ciphertext through a per-process temp
// file so a crash cannot leave a truncated store behind. Unlike Rust's
// `std::fs::rename`, Go's rename already replaces an existing destination on
// Windows, so no remove-then-rename fallback is needed.
func writeSecretsFileAtomically(path string, contents []byte) error {
	dir := filepath.Dir(path)
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.tmp-%d-%d", filepath.Base(path), os.Getpid(), time.Now().UnixNano()))
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create temp secrets file at %s: %w", tmpPath, err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp secrets file at %s: %w", tmpPath, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to sync temp secrets file at %s: %w", tmpPath, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp secrets file at %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to atomically replace secrets file at %s with %s: %w", path, tmpPath, err)
	}
	return nil
}
