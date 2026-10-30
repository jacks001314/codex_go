package auth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gofrs/flock"
)

// Rust #58251: a save replaces auth.json atomically, so an overlapping reader
// keeps seeing the complete previous document and the new one is readable.
func TestFileStorageSaveReplacesAtomicallyLikeRust(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "auth.json")
	store := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile})
	first := AuthDotJSON{AuthMode: "apikey", OpenAIAPIKey: "first-key"}
	if err := store.Save(first); err != nil {
		t.Fatalf("save first credential: %v", err)
	}
	if runtime.GOOS != "windows" {
		reader, err := os.Open(path)
		if err != nil {
			t.Fatalf("open overlapping reader: %v", err)
		}
		defer reader.Close()
	}
	replacement := AuthDotJSON{AuthMode: "apikey", OpenAIAPIKey: "replacement-with-a-different-length"}
	if err := store.Save(replacement); err != nil {
		t.Fatalf("save replacement credential: %v", err)
	}
	loaded, err := store.Load()
	if err != nil || loaded == nil || loaded.OpenAIAPIKey != replacement.OpenAIAPIKey {
		t.Fatalf("load after replacement = %#v, %v", loaded, err)
	}
	if runtime.GOOS != "windows" {
		// Rust's readers keep their old file description across the rename.
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read replaced credential: %v", err)
		}
		if strings.Contains(string(contents), "first-key") {
			t.Fatalf("replaced credential still holds the old value: %s", contents)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat replaced credential: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential mode = %v, want 0600", info.Mode().Perm())
		}
	}
}

// Rust #58251: a failed replacement must not leave a partial credential file
// behind.
func TestFileStorageFailedReplacementCleansUpTemporaryFileLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "auth.json"), 0o700); err != nil {
		t.Fatalf("seed auth.json directory: %v", err)
	}
	store := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile})
	if err := store.Save(AuthDotJSON{AuthMode: "apikey", OpenAIAPIKey: "secret"}); err == nil {
		t.Fatal("save accepted a directory as the credential file")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read codex home: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".auth-") {
			t.Fatalf("failed replacement left temporary file %q", entry.Name())
		}
	}
}

// Rust #58251: read-only and hard-linked credential files fail instead of
// silently changing file identity.
func TestFileStorageDoesNotReplaceReadOnlyOrHardLinkedCredentialsLikeRust(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "auth.json")
	store := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile})
	first := AuthDotJSON{AuthMode: "apikey", OpenAIAPIKey: "first"}
	if err := store.Save(first); err != nil {
		t.Fatalf("save first credential: %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("make credential read-only: %v", err)
	}
	err := store.Save(AuthDotJSON{AuthMode: "apikey", OpenAIAPIKey: "second"})
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("read-only save error = %v, want permission denied", err)
	}
	if loaded, err := store.Load(); err != nil || loaded == nil || loaded.OpenAIAPIKey != "first" {
		t.Fatalf("read-only save changed the credential: %#v, %v", loaded, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("restore credential permissions: %v", err)
	}
	peer := filepath.Join(home, "peer.json")
	if err := os.Link(path, peer); err != nil {
		t.Logf("hard links unavailable: %v", err)
		return
	}
	err = store.Save(AuthDotJSON{AuthMode: "apikey", OpenAIAPIKey: "second"})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("hard-linked save error = %v, want unsupported", err)
	}
	if loaded, err := store.Load(); err != nil || loaded == nil || loaded.OpenAIAPIKey != "first" {
		t.Fatalf("hard-linked save changed the credential: %#v, %v", loaded, err)
	}
}

// Rust #58251: commits coordinate through the "<auth file>.lock" sidecar so
// another cooperating process cannot write concurrently.
func TestAuthCommitLockCoordinatesLikeRust(t *testing.T) {
	home := t.TempDir()
	store := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile})
	guard, err := store.lockCommit()
	if err != nil {
		t.Fatalf("lock commit: %v", err)
	}
	var path string
	path, err = store.commitLockPath()
	if err != nil {
		guard.release()
		t.Fatalf("commit lock path: %v", err)
	}
	other := flock.New(path)
	locked, err := other.TryLock()
	if err != nil {
		guard.release()
		t.Fatalf("competing lock: %v", err)
	}
	guard.release()
	if locked {
		_ = other.Unlock()
		t.Fatal("competing writer acquired the credential commit lock")
	}
	locked, err = other.TryLock()
	if err != nil || !locked {
		t.Fatalf("lock after release = %v, %v", locked, err)
	}
	_ = other.Unlock()
}

// Rust #58251: deleting an absent file-store credential must not create the
// codex home just to leave a lock behind.
func TestDeletingAbsentFileStoreDoesNotCreateHomeLikeRust(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing")
	store := NewStoreWithOptions(home, &StoreOptions{Mode: AuthCredentialsStoreFile})
	removed, err := store.Delete()
	if err != nil || removed {
		t.Fatalf("Delete() = %v, %v; want false, nil", removed, err)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("codex home was created: %v", err)
	}
}
