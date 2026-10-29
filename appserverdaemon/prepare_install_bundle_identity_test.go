package appserverdaemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStagedIdentityExecutablePrefersTheMacOSBundleLikeRust pins the choice
// Rust #45558 makes for a provisioned bundle: identity is read from
// CodexCLI.app/Contents/MacOS/codex, not from the packaged launcher.
func TestStagedIdentityExecutablePrefersTheMacOSBundleLikeRust(t *testing.T) {
	stage := t.TempDir()
	entrypoint := filepath.Join(stage, "bin", managedCodexFileName())
	bundle := filepath.Join(stage, macOSBundleDirname, "Contents", "MacOS", "codex")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatalf("MkdirAll(bundle) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	for _, path := range []string{bundle, entrypoint} {
		if err := os.WriteFile(path, []byte(path), 0o755); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	if got := stagedIdentityExecutable("darwin", stage, "bin/"+managedCodexFileName()); got != bundle {
		t.Fatalf("darwin identity executable = %q, want %q", got, bundle)
	}
	for _, goos := range []string{"linux", "windows"} {
		if got := stagedIdentityExecutable(goos, stage, "bin/"+managedCodexFileName()); got != entrypoint {
			t.Fatalf("%s identity executable = %q, want the manifest entrypoint %q", goos, got, entrypoint)
		}
	}

	// A package without the bundle falls back to the manifest entrypoint even
	// on macOS.
	plain := t.TempDir()
	plainEntrypoint := filepath.Join(plain, "bin", managedCodexFileName())
	if err := os.MkdirAll(filepath.Dir(plainEntrypoint), 0o755); err != nil {
		t.Fatalf("MkdirAll(plain bin) error = %v", err)
	}
	if got := stagedIdentityExecutable("darwin", plain, "bin/"+managedCodexFileName()); got != plainEntrypoint {
		t.Fatalf("bundle-less darwin identity executable = %q, want %q", got, plainEntrypoint)
	}
}
