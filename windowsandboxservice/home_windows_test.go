//go:build windows

package windowsandboxservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
)

// TestPrepareCodexHomePinsSandboxDirectoriesLikeRust pins Rust
// ipc::home::prepare_codex_home for the legacy runtime: the home is
// canonicalized, the sandbox leaves are created and guarded, and every pinned
// handle is returned to the caller.
func TestPrepareCodexHomePinsSandboxDirectoriesLikeRust(t *testing.T) {
	home := t.TempDir()
	canonical, handles, err := prepareCodexHome(home, false, windowssandbox.DirectoryOpenOrCreate)
	if err != nil {
		t.Fatalf("prepareCodexHome() error = %v", err)
	}
	defer closeHandles(handles)
	if len(handles) == 0 {
		t.Fatal("no directory handles were pinned")
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if !strings.EqualFold(canonical, resolved) {
		t.Fatalf("canonical home = %q, want %q", canonical, resolved)
	}
	for _, name := range []string{".sandbox", ".sandbox-secrets", ".sandbox-bin"} {
		info, err := os.Stat(filepath.Join(canonical, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("sandbox directory %q = %v, %v", name, info, err)
		}
	}
	guards, err := os.ReadDir(filepath.Join(canonical, ".sandbox"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(guards) == 0 {
		t.Fatal("the sandbox directory guard was not created")
	}
}

// TestPrepareCodexHomeRegisteredPinsThreeDirectoriesLikeRust pins the
// registered-Core variant: .sandbox-bin belongs to the service registration, so
// prepare_codex_home leaves it alone.
func TestPrepareCodexHomeRegisteredPinsThreeDirectoriesLikeRust(t *testing.T) {
	home := t.TempDir()
	canonical, handles, err := prepareCodexHome(home, true, windowssandbox.DirectoryOpenOrCreate)
	if err != nil {
		t.Fatalf("prepareCodexHome() error = %v", err)
	}
	defer closeHandles(handles)
	if _, err := os.Stat(filepath.Join(canonical, ".sandbox-bin")); !os.IsNotExist(err) {
		t.Fatalf(".sandbox-bin must not be created for registered Core: %v", err)
	}
	for _, name := range []string{".sandbox", ".sandbox-secrets"} {
		info, err := os.Stat(filepath.Join(canonical, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("sandbox directory %q = %v, %v", name, info, err)
		}
	}
}

// TestPrepareCodexHomeRejectsUnsafePathsLikeRust keeps relative and UNC homes
// out of the pinning path entirely.
func TestPrepareCodexHomeRejectsUnsafePathsLikeRust(t *testing.T) {
	for _, home := range []string{`relative\home`, `\\server\share\home`} {
		handles := []uintptr{}
		_ = handles
		if _, _, err := prepareCodexHome(home, false, windowssandbox.DirectoryOpenOrCreate); err == nil {
			t.Fatalf("prepareCodexHome(%q) accepted an unsafe path", home)
		}
	}
}
