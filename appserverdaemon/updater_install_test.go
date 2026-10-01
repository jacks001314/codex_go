package appserverdaemon

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stubStrictManagedVersion pins the version the strict installer check reads.
func stubStrictManagedVersion(t *testing.T, version string) {
	t.Helper()
	previous := managedCodexVersionStrict
	managedCodexVersionStrict = func(string) (string, error) { return version, nil }
	t.Cleanup(func() { managedCodexVersionStrict = previous })
}

// TestRequiredPackageArtifactsLikeRust pins the files a complete package must
// carry on each platform, including the bundled sandbox launcher on Linux.
func TestRequiredPackageArtifactsLikeRust(t *testing.T) {
	manifest := &daemonPackageManifest{
		Entrypoint:   "bin/codex.exe",
		ResourcesDir: "codex-resources",
		PathDir:      "codex-path",
	}
	wantWindows := []string{
		"bin/codex.exe",
		"bin/codex-code-mode-host.exe",
		"codex-path/rg.exe",
		"codex-resources/codex-command-runner.exe",
		"codex-resources/codex-windows-sandbox-setup.exe",
	}
	if got := requiredPackageArtifacts(manifest, "windows"); !reflect.DeepEqual(got, wantWindows) {
		t.Fatalf("windows artifacts = %#v, want %#v", got, wantWindows)
	}
	linux := &daemonPackageManifest{Entrypoint: "bin/codex", PathDir: "codex-path"}
	wantLinux := []string{"bin/codex", "bin/codex-code-mode-host", "codex-path/rg", "codex-resources/bwrap"}
	if got := requiredPackageArtifacts(linux, "linux"); !reflect.DeepEqual(got, wantLinux) {
		t.Fatalf("linux artifacts = %#v, want %#v", got, wantLinux)
	}
	wantDarwin := []string{"bin/codex", "bin/codex-code-mode-host", "codex-path/rg"}
	if got := requiredPackageArtifacts(linux, "darwin"); !reflect.DeepEqual(got, wantDarwin) {
		t.Fatalf("darwin artifacts = %#v, want %#v", got, wantDarwin)
	}
}

// TestUpdateFromCLIPinsTheCLIPackage mirrors Rust prepare_install::update_from_cli:
// the invoking CLI's package replaces the selected one, the installed release is
// a pinned local directory, and the latest-version marker is cleared so the
// managed updater stops following production releases.
func TestUpdateFromCLIPinsTheCLIPackage(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "packages", daemonPackagesDirname)
	existing := filepath.Join(root, releasesDirName, "1.0.0-"+platformTarget())
	writeTestFile(t, filepath.Join(existing, "bin", managedCodexFileName()))
	// The installer publishes the marker naming the release it follows.
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(root) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, autoUpdateVersionFileName), []byte("1.0.0-"+platformTarget()), 0o600); err != nil {
		t.Fatalf("write marker error = %v", err)
	}
	if err := selectDaemonRelease(root, existing); err != nil {
		t.Fatalf("selectDaemonRelease(existing) error = %v", err)
	}
	if !isStableStandaloneRelease(home, ManagedCodexBin(home)) {
		t.Fatal("the installer-published release must be updater-eligible before the update")
	}

	source := filepath.Join(t.TempDir(), "cli-package")
	entrypoint := writeTestPackage(t, source, "1.2.3", true)
	stubPrepareSource(t, source, entrypoint, "1.2.3")
	stubStrictManagedVersion(t, "1.2.3")

	confirmed := 0
	output, err := UpdateFromCLI(home, func(request *DaemonInstallRequest) (bool, error) {
		confirmed++
		if request.Version != "1.2.3" || request.Destination != root || request.RestartRequired {
			t.Errorf("install request = %+v", request)
		}
		if request.InstalledVersion == nil || *request.InstalledVersion != "1.2.3" {
			t.Errorf("installed version = %v", request.InstalledVersion)
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("UpdateFromCLI error = %v", err)
	}
	if confirmed != 1 {
		t.Fatalf("confirmations = %d, want 1", confirmed)
	}
	if output == nil || output.Status != UpdateUpdated {
		t.Fatalf("UpdateFromCLI output = %#v", output)
	}
	if output.InstalledVersion == nil || *output.InstalledVersion != "1.2.3" {
		t.Fatalf("installed version = %v", output.InstalledVersion)
	}
	if !strings.Contains(output.Message, "selected and pinned") {
		t.Fatalf("message = %q", output.Message)
	}
	selected := ManagedCodexBin(home)
	if !isRegularFile(selected) {
		t.Fatalf("selected managed binary %s is missing", selected)
	}
	if !pathWithin(canonicalPath(selected), canonicalPath(filepath.Join(root, releasesDirName))) {
		t.Fatalf("selected binary %s is outside the daemon releases directory", selected)
	}
	if strings.Contains(filepath.Base(canonicalPath(selected)), "1.0.0-") {
		t.Fatalf("selected binary %s still resolves to the replaced release", selected)
	}
	if _, err := os.Stat(filepath.Join(root, autoUpdateVersionFileName)); !os.IsNotExist(err) {
		t.Fatalf("auto-update marker still present: %v", err)
	}
	if isStableStandaloneRelease(home, selected) {
		t.Fatal("a pinned local package must not be eligible for the managed updater")
	}
}

// TestUpdateFromCLICancellationChangesNothing mirrors Rust's `Ok(None)`: a
// refused replacement leaves the selected package alone.
func TestUpdateFromCLICancellationChangesNothing(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "packages", daemonPackagesDirname)
	existing := filepath.Join(root, releasesDirName, "1.0.0-"+platformTarget())
	writeTestFile(t, filepath.Join(existing, "bin", managedCodexFileName()))
	if err := selectDaemonRelease(root, existing); err != nil {
		t.Fatalf("selectDaemonRelease(existing) error = %v", err)
	}
	selected := ManagedCodexBin(home)

	source := filepath.Join(t.TempDir(), "cli-package")
	entrypoint := writeTestPackage(t, source, "1.2.3", true)
	stubPrepareSource(t, source, entrypoint, "1.2.3")
	stubStrictManagedVersion(t, "1.2.3")

	output, err := UpdateFromCLI(home, func(*DaemonInstallRequest) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("UpdateFromCLI error = %v", err)
	}
	if output != nil {
		t.Fatalf("UpdateFromCLI output = %#v, want nil for a cancelled install", output)
	}
	if got := ManagedCodexBin(home); got != selected {
		t.Fatalf("selected binary changed from %q to %q", selected, got)
	}
}

// TestStableStandaloneReleaseLikeRust pins the updater eligibility rules.
func TestStableStandaloneReleaseLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "packages", daemonPackagesDirname)
	release := filepath.Join(root, releasesDirName, "1.2.3-"+platformTarget())
	writeTestFile(t, filepath.Join(release, "bin", managedCodexFileName()))
	if err := selectDaemonRelease(root, release); err != nil {
		t.Fatalf("selectDaemonRelease error = %v", err)
	}
	selected := ManagedCodexBin(home)
	// The installer publishes the marker for the release it follows.
	writeTestFile(t, filepath.Join(root, autoUpdateVersionFileName))
	if err := os.WriteFile(filepath.Join(root, autoUpdateVersionFileName), []byte("1.2.3-"+platformTarget()), 0o600); err != nil {
		t.Fatalf("write marker error = %v", err)
	}
	if !isStableStandaloneRelease(home, selected) {
		t.Fatal("a versioned release with a matching marker is updater-eligible")
	}
	// A pinned local release name never is.
	local := filepath.Join(root, releasesDirName, "local-abc123-"+platformTarget())
	writeTestFile(t, filepath.Join(local, "bin", managedCodexFileName()))
	if err := os.WriteFile(filepath.Join(root, autoUpdateVersionFileName), []byte(filepath.Base(local)), 0o600); err != nil {
		t.Fatalf("write marker error = %v", err)
	}
	if isStableStandaloneRelease(home, filepath.Join(local, "bin", managedCodexFileName())) {
		t.Fatal("a pinned local release must not be updater-eligible")
	}
}
