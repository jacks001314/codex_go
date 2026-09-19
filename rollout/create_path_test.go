package rollout

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Mirrors Rust's pinned rollout filename: when the caller supplies a path (the
// app server records one at thread start), the recorder writes there instead of
// deriving a new timestamped name.
func TestNewRecorderHonorsPinnedPathLikeRust(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pinned := filepath.Join(home, SessionsSubdir, "2026", "01", "02", "rollout-2026-01-02T03-04-05-thread-pinned.jsonl")

	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home,
		ThreadID:  "thread-pinned",
		Path:      pinned,
		Now:       now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("NewRecorder(pinned) error = %v", err)
	}
	if recorder.Path() != pinned {
		t.Fatalf("recorder path = %q, want %q", recorder.Path(), pinned)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(pinned); err != nil {
		t.Fatalf("pinned rollout missing: %v", err)
	}
	// The derived name (from the later timestamp) is not used.
	if derived := PathForThread(home, "thread-pinned", now.Add(3*time.Second)); derived == pinned {
		t.Fatal("test setup: the derived path equals the pinned path")
	} else if _, err := os.Stat(derived); !os.IsNotExist(err) {
		t.Fatalf("derived rollout unexpectedly exists (err=%v)", err)
	}

	// Without a pinned path the recorder still derives the standard name.
	derivedRecorder, err := NewRecorder(&CreateParams{CodexHome: home, ThreadID: "thread-derived", Now: now})
	if err != nil {
		t.Fatalf("NewRecorder(derived) error = %v", err)
	}
	defer derivedRecorder.Close()
	if want := PathForThread(home, "thread-derived", now); derivedRecorder.Path() != want {
		t.Fatalf("derived recorder path = %q, want %q", derivedRecorder.Path(), want)
	}
}
