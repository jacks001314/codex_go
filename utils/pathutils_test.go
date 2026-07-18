package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeForWSLDrivePaths(t *testing.T) {
	if got := NormalizeForWSL("/mnt/C/Users/Dev", true); got != "/mnt/c/users/dev" {
		t.Fatalf("wsl path = %q", got)
	}
	if got := NormalizeForWSL("/home/Dev", true); got != "/home/Dev" {
		t.Fatalf("non drive path = %q", got)
	}
}

func TestNativeWorkdirSimplifiesWindowsVerbatimPaths(t *testing.T) {
	got := NormalizeForNativeWorkdirWithFlag(`\\?\D:\c\x\worktrees\2508\swift-base`, true)
	if got == `\\?\D:\c\x\worktrees\2508\swift-base` {
		t.Fatalf("verbatim path was not simplified")
	}
	unchanged := NormalizeForNativeWorkdirWithFlag(`\\?\D:\c\x`, false)
	if unchanged != `\\?\D:\c\x` {
		t.Fatalf("non-windows path = %q", unchanged)
	}
}

func TestPathsMatchAfterNormalizationFallback(t *testing.T) {
	if !PathsMatchAfterNormalization("missing", "missing") {
		t.Fatalf("identical missing paths should match")
	}
	if PathsMatchAfterNormalization("missing-a", "missing-b") {
		t.Fatalf("different missing paths should not match")
	}
}

func TestResolveSymlinkWritePathsForMissingAndRealFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.txt")
	resolved, err := ResolveSymlinkWritePaths(missing)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ReadPath == nil || *resolved.ReadPath != missing || resolved.WritePath != missing {
		t.Fatalf("missing resolved = %#v", resolved)
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err = ResolveSymlinkWritePaths(file)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ReadPath == nil || *resolved.ReadPath != file {
		t.Fatalf("file resolved = %#v", resolved)
	}
}

func TestResolveSymlinkWritePathsCycleOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs platform-specific permissions on Windows")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveSymlinkWritePaths(a)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ReadPath != nil || resolved.WritePath != a {
		t.Fatalf("cycle resolved = %#v", resolved)
	}
}

func TestWriteAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "file.txt")
	if err := WriteAtomically(path, "hello"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q", string(data))
	}
}
