package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotPath(t *testing.T) {
	path, temp := SnapshotPath("/home/codex", "session-1", ShellBash, 42)
	if filepath.Base(path) != "session-1.42.sh" {
		t.Fatalf("path = %q", path)
	}
	if filepath.Base(temp) != "session-1.tmp-42" {
		t.Fatalf("temp = %q", temp)
	}
	psPath, _ := SnapshotPath("/home/codex", "session-1", ShellPowerShell, 42)
	if filepath.Ext(psPath) != ".ps1" {
		t.Fatalf("powershell extension = %q", filepath.Ext(psPath))
	}
}

func TestStripSnapshotPreamble(t *testing.T) {
	got, ok := StripSnapshotPreamble("noise\n# Snapshot file\nexport A='b'\n")
	if !ok || got != "# Snapshot file\nexport A='b'\n" {
		t.Fatalf("StripSnapshotPreamble() = %q/%v", got, ok)
	}
	if _, ok := StripSnapshotPreamble("noise"); ok {
		t.Fatalf("StripSnapshotPreamble(no marker) ok = true")
	}
}

func TestBuildPOSIXSnapshot(t *testing.T) {
	snapshot := BuildPOSIXSnapshot(
		map[string]string{"B": "two", "A": "a'b", "PWD": "/tmp"},
		map[string]string{"ll": "ls -l"},
	)
	if strings.Contains(snapshot, "PWD") {
		t.Fatalf("snapshot included PWD:\n%s", snapshot)
	}
	if !strings.Contains(snapshot, "export A='a'\\''b'") || !strings.Contains(snapshot, "alias ll='ls -l'") {
		t.Fatalf("snapshot missing quoted entries:\n%s", snapshot)
	}
}

// Rust's cleanup drops snapshots whose session has no rollout any more or whose
// rollout is older than the retention window, removes files it cannot attribute
// to a session, and always keeps the active session's own files.
func TestCleanupSnapshotsFollowsTheRolloutLikeRust(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, SnapshotDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	names := []string{
		"active.1.sh", "active.tmp-2", "fresh.1.sh", "session-1.9.ps1",
		"gone.1.sh", "stale.1.sh", "nodotextension", "weird.txt",
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	now := time.Now()
	lookup := func(sessionID string) (time.Time, bool) {
		switch sessionID {
		case "fresh", "session-1":
			return now.Add(-time.Hour), true
		case "stale":
			return now.Add(-SnapshotRetention - time.Hour), true
		default:
			return time.Time{}, false
		}
	}
	removed, err := CleanupSnapshots(home, "active", now, lookup)
	if err != nil {
		t.Fatalf("CleanupSnapshots() error = %v", err)
	}
	wantRemoved := map[string]bool{
		filepath.Join(dir, "gone.1.sh"):      true,
		filepath.Join(dir, "stale.1.sh"):     true,
		filepath.Join(dir, "nodotextension"): true,
		filepath.Join(dir, "weird.txt"):      true,
	}
	if len(removed) != len(wantRemoved) {
		t.Fatalf("removed = %v", removed)
	}
	for _, path := range removed {
		if !wantRemoved[path] {
			t.Fatalf("removed %s, which should have been kept", path)
		}
	}
	for _, name := range []string{"active.1.sh", "active.tmp-2", "fresh.1.sh", "session-1.9.ps1"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to remain: %v", name, err)
		}
	}
	// Without a rollout lookup only the unattributable files go.
	removed, err = CleanupSnapshots(home, "active", now, nil)
	if err != nil {
		t.Fatalf("CleanupSnapshots() error = %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want nothing without a lookup", removed)
	}
}

func TestSnapshotSessionIDFromFileNameMatchesRust(t *testing.T) {
	cases := map[string]string{
		"session-1.1789.sh":      "session-1",
		"session-1.1789.ps1":     "session-1",
		"session-1.tmp-1789":     "session-1",
		"session-1.1789.1788.sh": "session-1",
	}
	for name, want := range cases {
		got, ok := SnapshotSessionIDFromFileName(name)
		if !ok || got != want {
			t.Fatalf("SnapshotSessionIDFromFileName(%q) = %q/%v, want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"", "noextension", ".sh", "session.txt", "session-1.9.sh.bak"} {
		if got, ok := SnapshotSessionIDFromFileName(name); ok {
			t.Fatalf("SnapshotSessionIDFromFileName(%q) = %q, want no session", name, got)
		}
	}
}

func TestSnapshotFileClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.sh")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	file := NewSnapshotFile(path)
	if file.Path() != path {
		t.Fatalf("Path() = %q", file.Path())
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat after Close() = %v, want not exist", err)
	}
}
