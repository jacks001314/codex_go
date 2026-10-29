//go:build darwin

package appserverdaemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPrepareDaemonInstallSeedsAProvisionedMacOSBundleLikeRust ports Rust
// `provisioned_macos_bundle_seeds_from_its_running_executable` (#45558): when
// the CLI runs from CodexCLI.app/Contents/MacOS, the staged copy validates
// identity against that executable, keeps it in the release, and records no
// auto-update marker for a prerelease version.
func TestPrepareDaemonInstallSeedsAProvisionedMacOSBundleLikeRust(t *testing.T) {
	home := privateDaemonTestHome(t)
	source := filepath.Join(t.TempDir(), "package")
	const version = "0.1.0-internal-test.202609091200.1"
	entrypoint := writeTestPackage(t, source, version, true)
	if err := os.WriteFile(entrypoint, []byte("#!/bin/sh\necho codex 0.0.0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(launcher) error = %v", err)
	}
	bundle := filepath.Join(source, macOSBundleDirname, "Contents", "MacOS", "codex")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatalf("MkdirAll(bundle) error = %v", err)
	}
	bundleBytes := []byte("provisioned executable")
	if err := os.WriteFile(bundle, bundleBytes, 0o755); err != nil {
		t.Fatalf("WriteFile(bundle) error = %v", err)
	}
	stubPrepareSource(t, source, bundle, version)

	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	if err := prepareDaemonInstall(daemon, &DaemonSettings{}); err != nil {
		t.Fatalf("prepareDaemonInstall() error = %v", err)
	}

	root := filepath.Join(home, "packages", daemonPackagesDirname)
	selected := canonicalPath(filepath.Join(root, "current"))
	if selected == "" {
		t.Fatalf("dedicated selection %s was not published", filepath.Join(root, "current"))
	}
	installed, err := os.ReadFile(filepath.Join(selected, macOSBundleDirname, "Contents", "MacOS", "codex"))
	if err != nil {
		t.Fatalf("ReadFile(installed bundle) error = %v", err)
	}
	if string(installed) != string(bundleBytes) {
		t.Fatalf("installed bundle bytes = %q, want %q", installed, bundleBytes)
	}
	if _, err := os.Lstat(filepath.Join(root, autoUpdateVersionFileName)); !os.IsNotExist(err) {
		t.Fatalf("prerelease install wrote auto-update-version (err = %v)", err)
	}
}
