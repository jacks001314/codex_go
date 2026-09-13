package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// Cross-process serialization for one MCP OAuth credential's refresh
// transaction. Mirrors Rust rmcp-client oauth/refresh_lock.rs: the guard is
// acquired before the authoritative credential reread and retained through the
// provider refresh and persistence, so two processes cannot replay a rotating
// refresh token or observe a partially persisted transaction.
const (
	mcpOAuthCredentialLockDir            = "mcp-oauth-locks"
	mcpOAuthCredentialLockAcquireTimeout = 60 * time.Second
	mcpOAuthCredentialLockRetryInterval  = 50 * time.Millisecond
)

// mcpOAuthCredentialLock holds an exclusive cross-process credential lock.
type mcpOAuthCredentialLock struct {
	lock *flock.Flock
}

// acquireMCPOAuthCredentialLockForServer acquires the credential lock for one
// server credential, scoped to the same Codex home as the OAuth store.
func acquireMCPOAuthCredentialLockForServer(codexHome string, serverName string, serverURL string) (*mcpOAuthCredentialLock, error) {
	storeKey, err := computeMCPOAuthStoreKey(codexHome, serverName, serverURL)
	if err != nil {
		return nil, err
	}
	lock, err := acquireMCPOAuthCredentialLockIn(codexHome, storeKey, mcpOAuthCredentialLockAcquireTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire OAuth credential lock for %s: %w", strings.TrimSpace(serverName), err)
	}
	return lock, nil
}

// acquireMCPOAuthCredentialLockIn mirrors Rust RefreshCredentialLock::acquire_in:
// a bounded retry loop with a fixed sleep, never stealing a held lock.
func acquireMCPOAuthCredentialLockIn(codexHome string, storeKey string, acquireTimeout time.Duration) (*mcpOAuthCredentialLock, error) {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil, fmt.Errorf("failed to open OAuth refresh lock: CODEX_HOME is required")
	}
	storeKey = strings.TrimSpace(storeKey)
	if storeKey == "" {
		return nil, fmt.Errorf("failed to open OAuth refresh lock: store key is required")
	}
	sum := sha256.Sum256([]byte(storeKey))
	lockPath := filepath.Join(codexHome, mcpOAuthCredentialLockDir, hex.EncodeToString(sum[:])+".lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("failed to open OAuth refresh lock %s: %w", lockPath, err)
	}
	lock := flock.New(lockPath)
	deadline := time.Now().Add(acquireTimeout)
	for {
		locked, err := lock.TryLock()
		if err != nil {
			return nil, fmt.Errorf("failed to lock OAuth refresh lock %s: %w", lockPath, err)
		}
		if locked {
			return &mcpOAuthCredentialLock{lock: lock}, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for OAuth refresh lock %s", acquireTimeout, lockPath)
		}
		time.Sleep(mcpOAuthCredentialLockRetryInterval)
	}
}

// Release unlocks the credential lock. Safe to call on a nil guard.
func (l *mcpOAuthCredentialLock) Release() {
	if l == nil || l.lock == nil {
		return
	}
	_ = l.lock.Unlock()
}
