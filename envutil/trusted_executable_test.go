package envutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTrustedExecutableInRespectsInstallationRoots covers Rust #42324's trusted
// helper discovery: only files that resolve inside the trusted directories or
// installation roots are accepted.
func TestTrustedExecutableInRespectsInstallationRoots(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	helper := filepath.Join(dir, "codex-helper"+executableSuffix())
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	got, ok := trustedExecutableIn("codex-helper", []string{dir}, []string{root})
	if !ok {
		t.Fatal("trusted helper was not found")
	}
	want, err := filepath.EvalSymlinks(helper)
	if err != nil {
		t.Fatalf("EvalSymlinks error = %v", err)
	}
	if got != want {
		t.Fatalf("resolved helper = %q, want %q", got, want)
	}

	// A name containing a path must not escape the installation directories.
	if _, ok := trustedExecutableIn(helper, []string{dir}, []string{root}); ok {
		t.Fatal("path-containing name was accepted")
	}
	// A missing helper is not found.
	if _, ok := trustedExecutableIn("missing-helper", []string{dir}, []string{root}); ok {
		t.Fatal("missing helper was accepted")
	}
}

// TestTrustedExecutableInRejectsSymlinkOutsideRoots ensures a package-manager
// link that targets a workspace is not treated as an installed executable.
func TestTrustedExecutableInRejectsSymlinkOutsideRoots(t *testing.T) {
	root := t.TempDir()
	linkDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside"+executableSuffix())
	if err := os.WriteFile(outside, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	link := filepath.Join(linkDir, "codex-helper"+executableSuffix())
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, ok := trustedExecutableIn("codex-helper", []string{linkDir}, []string{root}); ok {
		t.Fatal("symlink escaping the installation roots was accepted")
	}
}

func TestTrustedSystemPathUsesTrustedDirectories(t *testing.T) {
	path, ok := TrustedSystemPath()
	if !ok {
		t.Skip("no trusted executable directories on this host")
	}
	for _, directory := range filepath.SplitList(path) {
		if !withinAny(directory, trustedInstallationRoots()) {
			t.Fatalf("trusted PATH contains an untrusted directory: %q", directory)
		}
	}
}
