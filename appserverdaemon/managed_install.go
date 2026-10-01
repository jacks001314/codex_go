package appserverdaemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Managed package resolution (Rust app-server-daemon/src/managed_install.rs).
//
// A daemon owns the packages it launches: the app-server it starts lives in a
// fixed releases directory under CODEX_HOME, never at wherever the CLI that
// requested the launch happens to be installed. The pre-dedicated
// "standalone" package is only reused while the pid records or logs it wrote
// still exist, so neither an old CLI nor a new one mistakes the other's
// installation for its own backend.
const (
	// daemonPackagesDirname is the package directory a dedicated daemon owns.
	daemonPackagesDirname = "app-server-daemon"
	// legacyPackagesDirname is the package directory of a pre-dedicated CLI.
	legacyPackagesDirname = "standalone"
	// LegacyPIDFileName and LegacyUpdatePIDFileName name the pid records of a
	// CLI that reused the pre-dedicated standalone package instead.
	LegacyPIDFileName       = "app-server.pid"
	LegacyUpdatePIDFileName = "app-server-updater.pid"
	// The stderr logs prove which package root a previous launch used.
	daemonStderrLogName        = "daemon.stderr.log"
	daemonUpdaterStderrLogName = "daemon-updater.stderr.log"
	legacyStderrLogName        = "app-server.stderr.log"
	legacyUpdaterStderrLogName = "app-server-updater.stderr.log"
)

// PackageRoot returns the managed package root of codexHome (Rust
// managed_install::package_root). A dedicated package wins as soon as it
// publishes a selection, and otherwise only the artifacts of an earlier launch
// decide between the dedicated and the legacy root.
func PackageRoot(codexHome string) string {
	dedicated := filepath.Join(codexHome, "packages", daemonPackagesDirname)
	if pathExistsNoFollow(filepath.Join(dedicated, "current")) {
		return dedicated
	}
	state := filepath.Join(codexHome, StateDirName)
	candidates := []struct {
		pkg       string
		artifacts []string
	}{
		{
			daemonPackagesDirname,
			[]string{PIDFileName, daemonStderrLogName, UpdatePIDFileName, daemonUpdaterStderrLogName},
		},
		{
			legacyPackagesDirname,
			[]string{LegacyPIDFileName, legacyStderrLogName, LegacyUpdatePIDFileName, legacyUpdaterStderrLogName},
		},
	}
	for _, candidate := range candidates {
		for _, name := range candidate.artifacts {
			if pathExistsNoFollow(filepath.Join(state, name)) {
				return filepath.Join(codexHome, "packages", candidate.pkg)
			}
		}
	}
	return dedicated
}

// ManagedCodexBin returns the app-server executable a managed daemon launches
// (Rust managed_install::managed_codex_bin). Both packaged and legacy layouts
// resolve without requiring a valid installation, so callers can report the
// path a launch would have used.
func ManagedCodexBin(codexHome string) string {
	root := PackageRoot(codexHome)
	current := filepath.Join(root, "current")
	packaged := filepath.Join(current, "bin", managedCodexFileName())
	legacy := filepath.Join(current, managedCodexFileName())
	if isRegularFile(packaged) ||
		(!isRegularFile(legacy) && (runtime.GOOS == "windows" || filepath.Base(root) == daemonPackagesDirname)) {
		return packaged
	}
	return legacy
}

// legacyPackageSelection reports whether a managed launch reuses the
// pre-dedicated standalone package, which also selects the legacy pid records
// and updater (Rust Daemon::from_environment).
func legacyPackageSelection(codexHome string) bool {
	if strings.TrimSpace(codexHome) == "" {
		return false
	}
	return pathWithin(ManagedCodexBin(codexHome), filepath.Join(codexHome, "packages", legacyPackagesDirname))
}

// refreshInstallation re-resolves the managed paths after a package was
// installed. The published layout decides both the managed executable and which
// pid records this launch owns, so a fresh install can change both
// (Rust Daemon::current_installation).
func (d *Daemon) refreshInstallation() {
	if d == nil || d.Paths == nil || strings.TrimSpace(d.Paths.CodexHome) == "" {
		return
	}
	d.Paths = PathsForCodexHome(d.Paths.CodexHome)
}

func managedCodexFileName() string {
	if runtime.GOOS == "windows" {
		return "codex.exe"
	}
	return "codex"
}

// isRegularFile reports whether path is an existing regular file (Rust
// Path::is_file).
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// pathExistsNoFollow reports whether path exists without following a final
// symlink (Rust Path::symlink_metadata).
func pathExistsNoFollow(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

// pathWithin reports whether path is root itself or inside root (Rust
// Path::starts_with, which compares whole components).
func pathWithin(path, root string) bool {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
