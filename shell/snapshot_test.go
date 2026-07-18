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

func TestCleanupStaleSnapshots(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, SnapshotDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	oldPath := filepath.Join(dir, "session-1.1.sh")
	newPath := filepath.Join(dir, "session-1.2.sh")
	otherPath := filepath.Join(dir, "session-2.1.sh")
	for _, path := range []string{oldPath, newPath, otherPath} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	now := time.Now()
	oldTime := now.Add(-SnapshotRetention - time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(old) error = %v", err)
	}
	removed, err := CleanupStaleSnapshots(home, "session-1", now)
	if err != nil {
		t.Fatalf("CleanupStaleSnapshots() error = %v", err)
	}
	if len(removed) != 1 || removed[0] != oldPath {
		t.Fatalf("removed = %v, want [%s]", removed, oldPath)
	}
	for _, path := range []string{newPath, otherPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to remain: %v", path, err)
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
