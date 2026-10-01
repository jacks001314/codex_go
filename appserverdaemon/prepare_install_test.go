package appserverdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/install"
)

// stubPrepareSource pins the CLI package and the running executable the daemon
// install checks compare, and reports the version its entrypoint serves.
func stubPrepareSource(t *testing.T, packageDir string, runningExe string, version string) {
	t.Helper()
	previousContext := currentInstallContext
	previousExe := currentExecutablePath
	previousVersion := managedCodexVersionBestEffort
	currentInstallContext = func() *install.InstallContext {
		return &install.InstallContext{PackageLayout: install.PackageLayoutFromExe(runningExe)}
	}
	currentExecutablePath = func() (string, error) { return runningExe, nil }
	managedCodexVersionBestEffort = func(string) *string {
		value := version
		return &value
	}
	t.Cleanup(func() {
		currentInstallContext = previousContext
		currentExecutablePath = previousExe
		managedCodexVersionBestEffort = previousVersion
	})
	if packageDir != "" {
		layout := install.PackageLayoutFromExe(runningExe)
		if layout == nil || layout.PackageDir != packageDir {
			t.Fatalf("entrypoint %q does not resolve to package %q", runningExe, packageDir)
		}
	}
}

// writeTestPackage writes a complete CLI package whose entrypoint is a copy of
// the running test binary, so the staged identity check passes.
func writeTestPackage(t *testing.T, root string, version string, complete bool) string {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	running, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	binary, err := os.ReadFile(running)
	if err != nil {
		t.Fatalf("ReadFile(running) error = %v", err)
	}
	entrypoint := filepath.Join(binDir, managedCodexFileName())
	if err := os.WriteFile(entrypoint, binary, 0o755); err != nil {
		t.Fatalf("WriteFile(entrypoint) error = %v", err)
	}
	manifest := map[string]any{
		"layoutVersion": 1,
		"version":       version,
		"target":        platformTarget(),
		"variant":       "codex",
		"entrypoint":    "bin/" + managedCodexFileName(),
		"resourcesDir":  "codex-resources",
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	writeTestFile(t, filepath.Join(root, packageMetadataFileName))
	if err := os.WriteFile(filepath.Join(root, packageMetadataFileName), encoded, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	if complete {
		writeTestFile(t, filepath.Join(binDir, codeModeHostExecutableName()))
		writeTestFile(t, filepath.Join(root, "codex-path", defaultRGCommandName()))
		if runtime.GOOS == "windows" {
			writeTestFile(t, filepath.Join(root, "codex-resources", "codex-command-runner.exe"))
			writeTestFile(t, filepath.Join(root, "codex-resources", "codex-windows-sandbox-setup.exe"))
		}
	}
	return entrypoint
}

// TestPrepareDaemonInstallSeedsTheCLIPackage mirrors Rust
// prepare_install::prepare: a fresh launch installs the CLI's own package into
// the daemon's releases directory, publishes the selection, and records the
// release the updater follows.
func TestPrepareDaemonInstallSeedsTheCLIPackage(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "cli-package")
	entrypoint := writeTestPackage(t, source, "1.2.3", true)
	stubPrepareSource(t, source, entrypoint, "1.2.3")
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	if err := prepareDaemonInstall(daemon, &DaemonSettings{}); err != nil {
		t.Fatalf("prepareDaemonInstall() error = %v", err)
	}

	root := filepath.Join(home, "packages", daemonPackagesDirname)
	release := filepath.Join(root, releasesDirName, "1.2.3-"+platformTarget())
	if !isRegularFile(filepath.Join(release, "bin", managedCodexFileName())) {
		t.Fatalf("release %s does not carry the packaged entrypoint", release)
	}
	selected := ManagedCodexBin(home)
	if !isRegularFile(selected) {
		t.Fatalf("managed binary %s was not published", selected)
	}
	if !pathWithin(canonicalPath(selected), canonicalPath(release)) {
		t.Fatalf("managed binary %s (%q) does not resolve inside the release %s (%q)",
			selected, canonicalPath(selected), release, canonicalPath(release))
	}
	marker, err := os.ReadFile(filepath.Join(root, autoUpdateVersionFileName))
	if err != nil {
		t.Fatalf("ReadFile(auto-update-version) error = %v", err)
	}
	if string(marker) != "1.2.3-"+platformTarget() {
		t.Fatalf("auto-update-version = %q", string(marker))
	}
	if staging, err := filepath.Glob(filepath.Join(root, releasesDirName, stagingDirPrefix+"*")); err != nil || len(staging) != 0 {
		t.Fatalf("staging directories left behind: %v (err = %v)", staging, err)
	}
}

// TestPrepareDaemonInstallIsIdempotent covers the second launch: a published
// selection is reused instead of installing the package again.
func TestPrepareDaemonInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "cli-package")
	entrypoint := writeTestPackage(t, source, "1.2.3", true)
	stubPrepareSource(t, source, entrypoint, "1.2.3")
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	if err := prepareDaemonInstall(daemon, &DaemonSettings{}); err != nil {
		t.Fatalf("first prepareDaemonInstall() error = %v", err)
	}
	selected := ManagedCodexBin(home)
	if err := prepareDaemonInstall(daemon, &DaemonSettings{}); err != nil {
		t.Fatalf("second prepareDaemonInstall() error = %v", err)
	}
	if got := ManagedCodexBin(home); got != selected {
		t.Fatalf("managed binary changed from %q to %q", selected, got)
	}
}

// TestPrepareDaemonInstallRequiresACompletePackage pins the two Rust refusals:
// a CLI without a package layout cannot seed a daemon, and an incomplete
// package is reported as the missing artifact.
func TestPrepareDaemonInstallRequiresACompletePackage(t *testing.T) {
	t.Run("no package layout", func(t *testing.T) {
		home := t.TempDir()
		previous := currentInstallContext
		currentInstallContext = func() *install.InstallContext { return &install.InstallContext{} }
		t.Cleanup(func() { currentInstallContext = previous })
		daemon := NewDaemonForCodexHome(home, "codex-go-test")
		err := prepareDaemonInstall(daemon, &DaemonSettings{})
		if err == nil || !strings.Contains(err.Error(), "this CLI has no complete local package") {
			t.Fatalf("prepareDaemonInstall() error = %v", err)
		}
	})

	t.Run("incomplete package", func(t *testing.T) {
		home := t.TempDir()
		source := filepath.Join(t.TempDir(), "cli-package")
		entrypoint := writeTestPackage(t, source, "1.2.3", false)
		stubPrepareSource(t, source, entrypoint, "1.2.3")
		daemon := NewDaemonForCodexHome(home, "codex-go-test")
		err := prepareDaemonInstall(daemon, &DaemonSettings{})
		if err == nil || !strings.Contains(err.Error(), "local Codex package is missing") {
			t.Fatalf("prepareDaemonInstall() error = %v", err)
		}
	})
}

// TestPrepareDaemonInstallRejectsAMismatchedPackage covers Rust's platform and
// executable check.
func TestPrepareDaemonInstallRejectsAMismatchedPackage(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "cli-package")
	entrypoint := writeTestPackage(t, source, "1.2.3", true)
	stubPrepareSource(t, source, entrypoint, "1.2.3")
	manifestPath := filepath.Join(source, packageMetadataFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error = %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal(manifest) error = %v", err)
	}
	manifest["target"] = "aarch64-apple-darwin"
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	err = prepareDaemonInstall(daemon, &DaemonSettings{})
	if err == nil || !strings.Contains(err.Error(), "does not match this platform or executable") {
		t.Fatalf("prepareDaemonInstall() error = %v", err)
	}
}

// TestStablePackageVersionLikeRust pins Rust prepare_install::stable_version:
// only a plain x.y.z release may follow the public updater.
func TestStablePackageVersionLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		version string
		want    bool
	}{
		{"1.2.3", true},
		{"0.0.1", true},
		{"1.2.3-dev", false},
		{"1.2.3+build", false},
		{"1.2", false},
		{"1.2.3.4", false},
		{"0.0.0", false},
		{"a.b.c", false},
	} {
		if got := stablePackageVersion(testCase.version); got != testCase.want {
			t.Errorf("stablePackageVersion(%q) = %v, want %v", testCase.version, got, testCase.want)
		}
	}
}
