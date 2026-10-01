package appserverdaemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

// TestPackageRootLikeRust mirrors Rust managed_install::package_root: the
// dedicated root wins as soon as it publishes a selection, and otherwise only
// the artifacts of an earlier launch decide.
func TestPackageRootLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	dedicated := filepath.Join(codexHome, "packages", daemonPackagesDirname)
	legacy := filepath.Join(codexHome, "packages", legacyPackagesDirname)
	state := filepath.Join(codexHome, StateDirName)

	if got := PackageRoot(codexHome); got != dedicated {
		t.Fatalf("PackageRoot() without artifacts = %q, want %q", got, dedicated)
	}
	writeTestFile(t, filepath.Join(state, LegacyPIDFileName))
	if got := PackageRoot(codexHome); got != legacy {
		t.Fatalf("PackageRoot() with a legacy pid record = %q, want %q", got, legacy)
	}
	if err := os.Remove(filepath.Join(state, LegacyPIDFileName)); err != nil {
		t.Fatalf("Remove(legacy pid) error = %v", err)
	}
	writeTestFile(t, filepath.Join(state, UpdatePIDFileName))
	if got := PackageRoot(codexHome); got != dedicated {
		t.Fatalf("PackageRoot() with a dedicated pid record = %q, want %q", got, dedicated)
	}
	writeTestFile(t, filepath.Join(state, LegacyPIDFileName))
	writeTestFile(t, filepath.Join(dedicated, "current", "marker"))
	if got := PackageRoot(codexHome); got != dedicated {
		t.Fatalf("PackageRoot() with a dedicated selection = %q, want %q", got, dedicated)
	}
}

// TestManagedCodexBinLikeRust mirrors Rust managed_install::managed_codex_bin:
// the packaged `bin/` layout wins when it exists, Windows and the dedicated
// root never fall back to a missing legacy file, and a legacy flat release is
// still resolvable.
func TestManagedCodexBinLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	root := filepath.Join(codexHome, "packages", daemonPackagesDirname)
	packaged := filepath.Join(root, "current", "bin", managedCodexFileName())
	legacy := filepath.Join(root, "current", managedCodexFileName())

	if got := ManagedCodexBin(codexHome); got != packaged {
		t.Fatalf("ManagedCodexBin() without files = %q, want %q", got, packaged)
	}
	writeTestFile(t, legacy)
	if runtime.GOOS == "windows" {
		if got := ManagedCodexBin(codexHome); got != legacy {
			t.Fatalf("ManagedCodexBin() with a flat release = %q, want %q", got, legacy)
		}
	}
	writeTestFile(t, packaged)
	if got := ManagedCodexBin(codexHome); got != packaged {
		t.Fatalf("ManagedCodexBin() with both layouts = %q, want %q", got, packaged)
	}
}

// TestPathsForCodexHomeSelectsPIDRecordsLikeRust mirrors Rust
// Daemon::from_environment: a launch that reuses the pre-dedicated package
// keeps writing the legacy pid records, so both clients agree on one backend.
func TestPathsForCodexHomeSelectsPIDRecordsLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	state := filepath.Join(codexHome, StateDirName)
	paths := PathsForCodexHome(codexHome)
	if paths.PIDFile != filepath.Join(state, PIDFileName) || paths.UpdatePIDFile != filepath.Join(state, UpdatePIDFileName) {
		t.Fatalf("dedicated pid paths = %q, %q", paths.PIDFile, paths.UpdatePIDFile)
	}
	if paths.ManagedCodexBin != ManagedCodexBin(codexHome) {
		t.Fatalf("managed bin = %q, want %q", paths.ManagedCodexBin, ManagedCodexBin(codexHome))
	}
	writeTestFile(t, filepath.Join(state, legacyStderrLogName))
	legacyPaths := PathsForCodexHome(codexHome)
	if legacyPaths.PIDFile != filepath.Join(state, LegacyPIDFileName) || legacyPaths.UpdatePIDFile != filepath.Join(state, LegacyUpdatePIDFileName) {
		t.Fatalf("legacy pid paths = %q, %q", legacyPaths.PIDFile, legacyPaths.UpdatePIDFile)
	}
}

// TestEnsureManagedCodexBinReportsRustMessage pins the diagnostic text Rust
// prints when a managed launch has no executable to start.
func TestEnsureManagedCodexBinReportsRustMessage(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", managedCodexFileName())
	err := EnsureManagedCodexBin(missing)
	if err == nil {
		t.Fatal("EnsureManagedCodexBin( missing ) error = nil")
	}
	want := "daemon executable not found at " + missing + "; repair the existing installation, or run `codex app-server daemon start` to install a missing daemon"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	// An existing managed binary is also probed for a launch the caller's Job
	// Object must permit, so the positive case uses this test executable.
	running, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	if err := EnsureManagedCodexBin(running); err != nil {
		t.Fatalf("EnsureManagedCodexBin( existing ) error = %v", err)
	}
}

// TestEnsureSupportedPlatformMatchesRust covers the platforms the managed
// lifecycle runs on and the text of the unsupported-platform error.
func TestEnsureSupportedPlatformMatchesRust(t *testing.T) {
	switch runtime.GOOS {
	case "windows", "darwin", "linux":
		if err := EnsureSupportedPlatform(); err != nil {
			t.Fatalf("EnsureSupportedPlatform() error = %v", err)
		}
	}
	want := "codex app-server daemon lifecycle is only supported on Unix and Windows platforms"
	if ErrUnsupportedPlatform.Error() != want {
		t.Fatalf("ErrUnsupportedPlatform = %q, want %q", ErrUnsupportedPlatform.Error(), want)
	}
}
