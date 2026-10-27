package rollout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// Rust #57358: a busy index lock makes appends time out without writing, and
// the next append succeeds once contention clears.
func TestSessionIndexAppendTimesOutWithoutWritingLikeRust(t *testing.T) {
	home := t.TempDir()
	held := flock.New(filepath.Join(home, sessionIndexLockFilename))
	locked, err := held.TryLock()
	if err != nil || !locked {
		t.Fatalf("hold index lock: locked=%v err=%v", locked, err)
	}
	defer func() { _ = held.Unlock() }()

	previous := sessionIndexLockTimeout
	sessionIndexLockTimeout = 100 * time.Millisecond
	defer func() { sessionIndexLockTimeout = previous }()

	if err := AppendThreadName(home, "thread-blocked", "blocked"); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("AppendThreadName() error = %v, want deadline exceeded", err)
	}
	if _, err := os.Stat(filepath.Join(home, SessionIndexFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index exists after timeout, stat error = %v", err)
	}
	if err := held.Unlock(); err != nil {
		t.Fatalf("release index lock: %v", err)
	}
	if err := AppendThreadName(home, "thread-blocked", "after contention"); err != nil {
		t.Fatalf("AppendThreadName() after release error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, SessionIndexFilename)); err != nil {
		t.Fatalf("index missing after released append: %v", err)
	}
}

// Rust #57358: removing names from an absent home is a no-op and does not
// create the home or an index.
func TestRemoveThreadNameEntriesSkipsMissingHomeLikeRust(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing")
	if err := RemoveThreadNameEntries(home, "thread-missing"); err != nil {
		t.Fatalf("RemoveThreadNameEntries() error = %v", err)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing home was created, stat error = %v", err)
	}
}

// Rust #57358: the rewrite that drops names preserves the index permissions.
func TestRemoveThreadNameEntriesPreservesIndexPermissionsLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits to preserve")
	}
	home := t.TempDir()
	indexPath := filepath.Join(home, SessionIndexFilename)
	contents := fmt.Sprintf("%s\n%s\n",
		`{"id":"thread-remove","thread_name":"drop","updated_at":"2024-01-01T00:00:00Z"}`,
		`{"id":"thread-keep","thread_name":"keep","updated_at":"2024-01-01T00:00:00Z"}`,
	)
	if err := os.WriteFile(indexPath, []byte(contents), 0o640); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.Chmod(indexPath, 0o640); err != nil {
		t.Fatalf("chmod index: %v", err)
	}
	if err := RemoveThreadNameEntries(home, "thread-remove"); err != nil {
		t.Fatalf("RemoveThreadNameEntries() error = %v", err)
	}
	info, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("index mode = %o, want 640", got)
	}
	remaining := readFileForSessionIndexTest(t, indexPath)
	if remaining != `{"id":"thread-keep","thread_name":"keep","updated_at":"2024-01-01T00:00:00Z"}`+"\n" {
		t.Fatalf("remaining index = %q", remaining)
	}
}

func readFileForSessionIndexTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
