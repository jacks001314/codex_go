//go:build !windows

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"codex_go/appserver"
	"codex_go/codexuds"
)

// assertVerifiedDefaultSocket mirrors Rust #45984/#54421 on Unix: a private
// directory is not enough, only the alias the daemon publishes at the advertised
// path verifies.
func assertVerifiedDefaultSocket(t *testing.T, socketPath string) {
	t.Helper()
	if _, verified := appserver.VerifiedLocalDaemonSocket(socketPath); verified {
		t.Fatalf("VerifiedLocalDaemonSocket(%q) verified a direct socket path", socketPath)
	}
	publishTestControlSocketAlias(t, socketPath)
	if _, verified := appserver.VerifiedLocalDaemonSocket(socketPath); !verified {
		t.Fatalf("VerifiedLocalDaemonSocket(%q) is unverified after publishing the daemon alias", socketPath)
	}
}

// publishTestControlSocketAlias publishes the symlink the daemon creates: the
// advertised path points at the hashed physical socket in the shared directory,
// so the endpoint verifies without starting a server.
func publishTestControlSocketAlias(t *testing.T, socketPath string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(socketPath))
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", filepath.Dir(socketPath), err)
	}
	shared, err := codexuds.SharedDaemonSocketDirectory()
	if err != nil {
		t.Fatalf("SharedDaemonSocketDirectory() error = %v", err)
	}
	sum := sha256.Sum256([]byte(filepath.Join(parent, filepath.Base(socketPath))))
	if err := os.Symlink(filepath.Join(shared, hex.EncodeToString(sum[:])), socketPath); err != nil {
		t.Fatalf("Symlink(%s) error = %v", socketPath, err)
	}
}
