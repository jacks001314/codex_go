//go:build !windows

package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/codexuds"
)

func TestProtectedUnixSocketPathIsDeterministic(t *testing.T) {
	rendezvous := filepath.Join(t.TempDir(), "codex.sock")
	first, err := protectedUnixSocketPath(rendezvous)
	if err != nil {
		t.Fatalf("protectedUnixSocketPath() error = %v", err)
	}
	second, err := protectedUnixSocketPath(rendezvous)
	if err != nil {
		t.Fatalf("protectedUnixSocketPath() second error = %v", err)
	}
	if first != second {
		t.Fatalf("protected path changed: %q != %q", first, second)
	}
	shared, err := codexuds.SharedDaemonSocketDirectory()
	if err != nil {
		t.Fatalf("SharedDaemonSocketDirectory() error = %v", err)
	}
	if filepath.Dir(first) != shared {
		t.Fatalf("protected path = %q, want directory %q", first, shared)
	}
}

func TestPrepareUnixSocketRendezvousRemovesOwnedAlias(t *testing.T) {
	rendezvous := filepath.Join(t.TempDir(), "codex.sock")
	physical, err := protectedUnixSocketPath(rendezvous)
	if err != nil {
		t.Fatalf("protectedUnixSocketPath() error = %v", err)
	}
	if _, err := codexuds.PrepareSharedDaemonSocketDirectory(); err != nil {
		t.Fatalf("PrepareSharedDaemonSocketDirectory() error = %v", err)
	}
	if err := os.Remove(physical); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove physical path: %v", err)
	}
	if err := os.Symlink(physical, rendezvous); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := prepareUnixSocketRendezvous(rendezvous, physical); err != nil {
		t.Fatalf("prepareUnixSocketRendezvous() error = %v", err)
	}
	if _, err := os.Lstat(rendezvous); !os.IsNotExist(err) {
		t.Fatalf("owned rendezvous still exists: %v", err)
	}
}

func TestPrepareUnixSocketRendezvousRejectsForeignSymlink(t *testing.T) {
	rendezvous := filepath.Join(t.TempDir(), "codex.sock")
	physical, err := protectedUnixSocketPath(rendezvous)
	if err != nil {
		t.Fatalf("protectedUnixSocketPath() error = %v", err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "foreign.sock"), rendezvous); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := prepareUnixSocketRendezvous(rendezvous, physical); err == nil {
		t.Fatal("prepareUnixSocketRendezvous() accepted a foreign symlink")
	}
	if info, err := os.Lstat(rendezvous); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("foreign symlink was modified: info=%v err=%v", info, err)
	}
}

func TestVerifiedLocalDaemonSocketRequiresProtectedAlias(t *testing.T) {
	rendezvous := filepath.Join(t.TempDir(), "codex.sock")
	physical, err := protectedUnixSocketPath(rendezvous)
	if err != nil {
		t.Fatalf("protectedUnixSocketPath() error = %v", err)
	}
	if _, err := codexuds.PrepareSharedDaemonSocketDirectory(); err != nil {
		t.Fatalf("PrepareSharedDaemonSocketDirectory() error = %v", err)
	}
	if err := os.Symlink(physical, rendezvous); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if verified, ok := verifiedLocalDaemonSocket(rendezvous); !ok || verified != rendezvous {
		t.Fatalf("verifiedLocalDaemonSocket() = %q, %v", verified, ok)
	}
	if err := os.Remove(rendezvous); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.WriteFile(rendezvous, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if verified, ok := verifiedLocalDaemonSocket(rendezvous); ok || verified != "" {
		t.Fatalf("direct path accepted: %q, %v", verified, ok)
	}
}

func TestVerifiedLocalDaemonSocketRejectsUnsafeDirectory(t *testing.T) {
	directory := t.TempDir()
	rendezvous := filepath.Join(directory, "codex.sock")
	physical, err := protectedUnixSocketPath(rendezvous)
	if err != nil {
		t.Fatalf("protectedUnixSocketPath() error = %v", err)
	}
	if _, err := codexuds.PrepareSharedDaemonSocketDirectory(); err != nil {
		t.Fatalf("PrepareSharedDaemonSocketDirectory() error = %v", err)
	}
	if err := os.Symlink(physical, rendezvous); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	if verified, ok := verifiedLocalDaemonSocket(rendezvous); ok || strings.TrimSpace(verified) != "" {
		t.Fatalf("unsafe directory accepted: %q, %v", verified, ok)
	}
}
