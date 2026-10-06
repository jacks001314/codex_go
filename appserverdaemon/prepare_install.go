package appserverdaemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"codex_go/install"
)

// Preparing a missing daemon package (Rust
// app-server-daemon/src/prepare_install.rs).
//
// A fresh managed launch installs a complete CLI package into the daemon's own
// releases directory before it starts a backend, so the app-server never runs
// out of the CLI's install location. Only the "missing package" mode is
// reached automatically; `daemon update --from-cli` drives Rust's explicit
// replacement path (InstallMode::Replace) through the same staging code, and
// the legacy-to-dedicated migration is separate (migration.go).

const (
	// installLockTimeout bounds coordination with the shell/PowerShell
	// installers that select a package at the same time.
	installLockTimeout = 30 * time.Second
	installLockRetry   = 50 * time.Millisecond
	// installLockFileName is created inside the package root.
	installLockFileName = "install.lock"
	// releasesDirName holds one directory per installed CLI package.
	releasesDirName = "releases"
	// autoUpdateVersionFileName records the release the updater follows.
	autoUpdateVersionFileName = "auto-update-version"
	// stagingDirPrefix names the temporary directory a package is staged in
	// before it is renamed into releases.
	stagingDirPrefix = ".staging."
)

// daemonPackageManifest is the part of codex-package.json the daemon validates.
type daemonPackageManifest struct {
	LayoutVersion int    `json:"layoutVersion"`
	Version       string `json:"version"`
	Target        string `json:"target"`
	Variant       string `json:"variant"`
	Entrypoint    string `json:"entrypoint"`
	ResourcesDir  string `json:"resourcesDir"`
	PathDir       string `json:"pathDir"`
}

// prepareDaemonInstallForTest and executableIdentityForPrepare exist so tests
// can pin the CLI package and the running executable the checks compare.
var (
	currentInstallContext = install.Current
	currentExecutablePath = os.Executable
)

// errDaemonInstallCancelled reports that the caller refused a replacement, which
// UpdateFromCLI turns into "nothing changed" like Rust's `Ok(None)`.
var errDaemonInstallCancelled = errors.New("daemon installation cancelled")

// DaemonInstallRequest describes the package a daemon installation would
// replace the selected one with, which is what the caller confirms
// (Rust prepare_install::InstallRequest).
type DaemonInstallRequest struct {
	Source           string
	Version          string
	Destination      string
	InstalledVersion *string
	RestartRequired  bool
}

// installConfirmer decides whether a replacement may proceed. A nil confirmer
// refuses, mirroring Rust's required confirmation.
type installConfirmer func(*DaemonInstallRequest) (bool, error)

type daemonInstallMode int

const (
	// daemonInstallMissing installs a package when none is selected yet.
	daemonInstallMissing daemonInstallMode = iota
	// daemonInstallReplace copies and pins the invoking CLI's package,
	// interrupting running work only after confirmation.
	daemonInstallReplace
)

// prepareDaemonInstall installs the CLI's own package into the daemon's
// releases directory when no managed package is selected yet. The caller must
// already hold the daemon operation lock (Rust prepare_install::prepare).
func prepareDaemonInstall(daemon *Daemon, settings *DaemonSettings) error {
	return prepareDaemonPackage(daemon, settings, daemonInstallMissing, nil)
}

// prepareDaemonPackage mirrors Rust prepare_install::prepare_from_package for
// both modes: a missing package is installed before the daemon starts, while a
// replacement is confirmed, serialized against the CLI lifecycle, and restarts
// whatever the caller found running.
func prepareDaemonPackage(daemon *Daemon, settings *DaemonSettings, mode daemonInstallMode, confirm installConfirmer) error {
	home := daemonCodexHome(daemon)
	if strings.TrimSpace(home) == "" {
		return ErrDaemonPathsRequired
	}
	managedBin := daemon.Paths.ManagedCodexBin
	previousRoot := PackageRoot(home)
	root := filepath.Join(home, "packages", daemonPackagesDirname)
	if !pathWithin(managedBin, previousRoot) {
		return errors.New("daemon package location changed; retry the command")
	}
	if mode == daemonInstallMissing {
		if previousRoot != root {
			// A pre-dedicated installation keeps serving from its own package root.
			return nil
		}
		if running, err := prepareRunningBackend(daemon, settings); err != nil {
			return err
		} else if running {
			return nil
		}
		if prepareSocketAnswers(daemon) {
			return nil
		}
		if pathExistsNoFollow(filepath.Join(root, "current")) || dedicatedStateArtifactsExist(home) {
			return ensureManagedCodexBin(managedBin)
		}
	} else if !pathExistsNoFollow(filepath.Join(previousRoot, "current")) {
		return errors.New("no daemon package is selected; run `codex app-server daemon start` first")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("failed to create daemon package root %s: %w", root, err)
	}
	if PackageRoot(home) != previousRoot {
		return errors.New("daemon package location changed; retry the command")
	}
	running, err := prepareRunningBackend(daemon, settings)
	if err != nil {
		return err
	}
	if !running && prepareSocketAnswers(daemon) {
		return errors.New("app server is running but is not managed by codex app-server daemon")
	}
	selected := ManagedCodexBin(home)
	previousRelease := canonicalPath(filepath.Join(previousRoot, "current"))
	if mode == daemonInstallMissing {
		if isRegularFile(selected) {
			return nil
		}
		if running || pathExistsNoFollow(filepath.Join(root, "current")) {
			return errors.New("the selected daemon package is incomplete; repair its installation before starting")
		}
	}
	source, err := prepareSourcePackage()
	if err != nil {
		return err
	}
	if pathWithin(canonicalPath(root), canonicalPath(source)) {
		return errors.New("CODEX_HOME must be outside the source CLI package")
	}
	manifestBytes, manifest, err := readDaemonPackageManifest(source)
	if err != nil {
		return err
	}
	if manifest.Target != platformTarget() || !isPlatformEntrypoint(manifest.Entrypoint) {
		return errors.New("the CLI package does not match this platform or executable")
	}
	if err := validateDaemonPackage(source); err != nil {
		return err
	}
	runningBin, err := currentExecutablePath()
	if err != nil {
		return fmt.Errorf("failed to resolve the running Codex executable: %w", err)
	}
	runningIdentity, err := install.ExecutableIdentityFromFile(runningBin)
	if err != nil {
		return fmt.Errorf("failed to read executable identity for %s: %w", runningBin, err)
	}
	if mode == daemonInstallReplace {
		request := &DaemonInstallRequest{
			Source:          source,
			Version:         manifest.Version,
			Destination:     root,
			RestartRequired: running,
		}
		if version := managedCodexVersionBestEffort(selected); version != nil {
			request.InstalledVersion = version
		}
		approved := false
		if confirm != nil {
			approved, err = confirm(request)
			if err != nil {
				return err
			}
		}
		if !approved {
			return errDaemonInstallCancelled
		}
	} else {
		daemon.Diagnostic("Installing daemon from CLI version %s into %s...", manifest.Version, root)
	}
	if mode == daemonInstallReplace {
		// Confirmation must not block lifecycle commands, so the replacement
		// re-acquires the operation lock and re-reads the settings that a
		// concurrent command may have changed (Rust prepare_from_package).
		operationLock, err := acquireExclusiveFileLock(daemon.Paths.OperationLockFile, OperationLockTimeout, OperationLockRetry, "daemon operation lock")
		if err != nil {
			return err
		}
		defer operationLock.Close()
		settings, err = daemon.LoadSettings()
		if err != nil {
			return err
		}
	}
	lock, err := acquireExclusiveFileLock(filepath.Join(root, installLockFileName), installLockTimeout, installLockRetry, "daemon installer lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	if PackageRoot(home) != previousRoot {
		return errors.New("daemon package location changed; retry the command")
	}
	if mode == daemonInstallMissing && isRegularFile(ManagedCodexBin(home)) {
		return nil
	}
	if canonicalPath(filepath.Join(previousRoot, "current")) != previousRelease {
		return errors.New("daemon selection changed while awaiting confirmation; retry the command")
	}
	currentRunning, err := prepareRunningBackend(daemon, settings)
	if err != nil {
		return err
	}
	if currentRunning != running {
		return errors.New("daemon running state changed while awaiting confirmation; retry the command")
	}
	running = currentRunning
	stable := stablePackageVersion(manifest.Version)
	releases := filepath.Join(root, releasesDirName)
	if err := os.MkdirAll(releases, 0o755); err != nil {
		return fmt.Errorf("failed to create daemon releases directory %s: %w", releases, err)
	}
	stage, err := os.MkdirTemp(releases, stagingDirPrefix)
	if err != nil {
		return fmt.Errorf("failed to stage the daemon package in %s: %w", releases, err)
	}
	staged := false
	defer func() {
		if !staged {
			_ = os.RemoveAll(stage)
		}
	}()
	digest, err := copyPackageTree(source, stage)
	if err != nil {
		return err
	}
	if err := validateDaemonPackage(stage); err != nil {
		return err
	}
	stagedExe := filepath.Join(stage, filepath.FromSlash(manifest.Entrypoint))
	if err := ensurePackageUnchanged(source, stage, manifestBytes, runningIdentity); err != nil {
		return err
	}
	binaryVersion := managedCodexVersionBestEffort(stagedExe)
	if stable && (binaryVersion == nil || *binaryVersion != manifest.Version) {
		return errors.New("the CLI package version does not match its executable")
	}
	name := fmt.Sprintf("local-%s-%s", digest, manifest.Target)
	if stable && mode == daemonInstallMissing {
		name = fmt.Sprintf("%s-%s", manifest.Version, manifest.Target)
	}
	release := filepath.Join(releases, name)
	if pathExistsNoFollow(release) {
		existing, err := packageTreeDigest(release)
		if err != nil {
			return err
		}
		info, err := os.Lstat(release)
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 || existing != digest {
			return errors.New("an existing daemon release has different contents; refusing to overwrite it")
		}
	} else {
		if err := linkUnixPackageAlias(stage); err != nil {
			return err
		}
		if err := publishDaemonRelease(stage, release); err != nil {
			return err
		}
		staged = true
	}
	followsLatest := mode == daemonInstallMissing && stable && followsStandaloneLatest(home, source)
	if PackageRoot(home) != previousRoot || (!running && prepareSocketAnswers(daemon)) {
		return errors.New("daemon package location or socket ownership changed while preparing its package; retry the command")
	}
	if runtime.GOOS == "windows" {
		if running {
			if err := ensureDetachedLaunch(filepath.Join(release, filepath.FromSlash(manifest.Entrypoint))); err != nil {
				return err
			}
		}
		if err := validateDaemonSelection(root); err != nil {
			return err
		}
	}
	if mode == daemonInstallReplace {
		// A replacement may interrupt running work, so the updater and the
		// backend stop only after the package is staged and validated. A failed
		// stop restores the updater for the still-selected package.
		if err := stopDaemonUpdater(daemon, settings); err != nil {
			return err
		}
		if PackageRoot(home) != previousRoot || canonicalPath(filepath.Join(previousRoot, "current")) != previousRelease {
			return errors.New("daemon selection changed while preparing its package; retry the command")
		}
		if running {
			if err := stopPIDBackend(NewPIDBackend(daemon.BackendPaths(settings)), settings.ShutdownGraceSecondsValue()); err != nil {
				if restoreErr := startDaemonUpdater(daemon, settings); restoreErr != nil {
					daemon.Diagnostic("warning: failed to restore the daemon updater after replacement failed: %v", restoreErr)
				}
				return err
			}
		}
	}
	if err := writeAutoUpdateMarker(filepath.Join(root, autoUpdateVersionFileName), name, followsLatest); err != nil {
		return err
	}
	if err := selectDaemonRelease(root, release); err != nil {
		return err
	}
	if running {
		daemon.refreshInstallation()
		if _, err := startPIDBackend(NewPIDBackend(daemon.BackendPaths(settings))); err != nil {
			return fmt.Errorf("%w: daemon package selected but could not start; retry with `codex app-server daemon start`", err)
		}
		if err := waitForAppServerReady(daemon); err != nil {
			return err
		}
	}
	return nil
}

// prepareSourcePackage returns the package directory of the CLI this process
// runs from, or the error Rust reports when the CLI is not a packaged install.
func prepareSourcePackage() (string, error) {
	context := currentInstallContext()
	if context == nil || context.PackageLayout == nil || strings.TrimSpace(context.PackageLayout.PackageDir) == "" {
		return "", errors.New("this CLI has no complete local package; install a packaged Codex CLI or use the standalone installer")
	}
	return context.PackageLayout.PackageDir, nil
}

func prepareRunningBackend(daemon *Daemon, settings *DaemonSettings) (bool, error) {
	return pidBackendIsStartingOrRunning(NewPIDBackend(daemon.BackendPaths(settings)))
}

func daemonCodexHome(daemon *Daemon) string {
	if daemon == nil || daemon.Paths == nil {
		return ""
	}
	return strings.TrimSpace(daemon.Paths.CodexHome)
}

func prepareSocketAnswers(daemon *Daemon) bool {
	socketPath := daemonSocketPath(daemon)
	if socketPath == "" {
		return false
	}
	_, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout)
	return err == nil
}

// dedicatedStateArtifactsExist reports whether a dedicated daemon already
// wrote its pid records or logs, which means the package it selected is the one
// the next launch must keep using (Rust prepare_from_package).
func dedicatedStateArtifactsExist(home string) bool {
	state := filepath.Join(home, StateDirName)
	for _, name := range []string{PIDFileName, daemonStderrLogName, UpdatePIDFileName, daemonUpdaterStderrLogName} {
		if pathExistsNoFollow(filepath.Join(state, name)) {
			return true
		}
	}
	return false
}

func readDaemonPackageManifest(root string) ([]byte, *daemonPackageManifest, error) {
	path := filepath.Join(root, packageMetadataFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read CLI package metadata %s: %w", path, err)
	}
	var manifest daemonPackageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, nil, fmt.Errorf("failed to parse CLI package metadata %s: %w", path, err)
	}
	return data, &manifest, nil
}

// packageMetadataFileName is the manifest name bundled with a Codex runtime
// package (Rust PACKAGE_METADATA_FILENAME).
const packageMetadataFileName = "codex-package.json"

// validateDaemonPackage mirrors Rust prepare_install::validate_package: the
// manifest must name an existing entrypoint, the packaged command shims the
// managed app-server resolves from its own package must be present, and the
// package must carry the `rg` search backend in its `codex-path` directory.
// Rust hardcodes the `codex-resources`/`codex-path` directory names and
// additionally requires `codex-resources/bwrap` on Linux; a locally built
// codex-go package keeps its helpers beside the entrypoint inside `bin/`, so the
// required directories are read from the manifest, and bubblewrap still comes
// from PATH (sandbox's system-bwrap check).
func validateDaemonPackage(root string) error {
	_, manifest, err := readDaemonPackageManifest(root)
	if err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Entrypoint) == "" {
		return fmt.Errorf("local Codex package %s names no entrypoint; reinstall the CLI or use the standalone installer", packageMetadataFileName)
	}
	for _, name := range requiredPackageArtifacts(manifest, runtime.GOOS) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if !isRegularFile(path) {
			return fmt.Errorf("local Codex package is missing %s; reinstall the CLI or use the standalone installer", name)
		}
		if runtime.GOOS == "windows" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("local Codex package file %s is not executable; reinstall the CLI or use the standalone installer", name)
		}
	}
	return nil
}

// requiredPackageArtifacts lists the files a complete package for goos must
// carry, which the managed app-server resolves from its own package: the
// entrypoint, the code-mode host beside it, the search backend in `pathDir`, and
// the platform sandbox helpers (Rust prepare_install::validate_package).
func requiredPackageArtifacts(manifest *daemonPackageManifest, goos string) []string {
	entrypoint := filepath.FromSlash(manifest.Entrypoint)
	names := []string{
		filepath.ToSlash(entrypoint),
		filepath.ToSlash(filepath.Join(filepath.Dir(entrypoint), codeModeHostExecutableNameFor(goos))),
		filepath.ToSlash(filepath.Join(packagePathDir(manifest), defaultRGCommandNameFor(goos))),
	}
	switch goos {
	case "windows":
		resources := filepath.ToSlash(filepath.FromSlash(strings.TrimSpace(manifest.ResourcesDir)))
		if resources == "." || resources == "" {
			resources = "codex-resources"
		}
		names = append(names,
			filepath.ToSlash(filepath.Join(resources, "codex-command-runner.exe")),
			filepath.ToSlash(filepath.Join(resources, "codex-windows-sandbox-setup.exe")),
		)
	case "linux":
		// Rust requires the bundled sandbox launcher on Linux; without it the CLI
		// can only sandbox where a system bwrap happens to be installed.
		resources := filepath.ToSlash(filepath.FromSlash(strings.TrimSpace(manifest.ResourcesDir)))
		if resources == "." || resources == "" {
			resources = "codex-resources"
		}
		names = append(names, filepath.ToSlash(filepath.Join(resources, "bwrap")))
	}
	return names
}

func codeModeHostExecutableName() string {
	return codeModeHostExecutableNameFor(runtime.GOOS)
}

func codeModeHostExecutableNameFor(goos string) string {
	if goos == "windows" {
		return "codex-code-mode-host.exe"
	}
	return "codex-code-mode-host"
}

// packagePathDir is the directory whose bundled command shims the CLI puts on
// PATH, defaulting to Rust's `codex-path` when the manifest omits it.
func packagePathDir(manifest *daemonPackageManifest) string {
	dir := filepath.FromSlash(strings.TrimSpace(manifest.PathDir))
	if strings.TrimSpace(dir) == "" || dir == "." {
		return "codex-path"
	}
	return dir
}

// defaultRGCommandName is the bundled search backend's file name.
func defaultRGCommandName() string {
	return defaultRGCommandNameFor(runtime.GOOS)
}

func defaultRGCommandNameFor(goos string) string {
	if goos == "windows" {
		return "rg.exe"
	}
	return "rg"
}

// ensurePackageUnchanged reproduces Rust's staged-install rechecks: the source
// package must still hash to the staged copy, its manifest must be byte
// identical, and the staged executable must be the one this process runs.
func ensurePackageUnchanged(source, stage string, manifestBytes []byte, runningIdentity install.ExecutableIdentity) error {
	sourceDigest, err := packageTreeDigest(source)
	if err != nil {
		return err
	}
	stagedDigest, err := packageTreeDigest(stage)
	if err != nil {
		return err
	}
	stagedManifest, err := os.ReadFile(filepath.Join(stage, packageMetadataFileName))
	if err != nil {
		return fmt.Errorf("failed to read staged CLI package metadata: %w", err)
	}
	_, manifest, err := readDaemonPackageManifest(stage)
	if err != nil {
		return err
	}
	stagedIdentity, err := install.ExecutableIdentityFromFile(filepath.Join(stage, filepath.FromSlash(manifest.Entrypoint)))
	if err != nil {
		return fmt.Errorf("failed to read staged executable identity: %w", err)
	}
	if sourceDigest != stagedDigest ||
		string(stagedManifest) != string(manifestBytes) ||
		stagedIdentity != runningIdentity {
		return errors.New("the CLI package changed while preparing the daemon or differs from the running executable")
	}
	return nil
}

// followsStandaloneLatest mirrors Rust's follows_latest: the selected release
// follows the published channel when the legacy standalone package is not this
// CLI's own directory, or when its marker already named it.
func followsStandaloneLatest(home, source string) bool {
	legacy := filepath.Join(home, "packages", legacyPackagesDirname)
	if canonicalPath(filepath.Join(legacy, "current")) != canonicalPath(source) {
		return true
	}
	selected, err := os.ReadFile(filepath.Join(legacy, autoUpdateVersionFileName))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(selected)) == filepath.Base(source)
}

func writeAutoUpdateMarker(path, name string, followsLatest bool) error {
	if !followsLatest {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to clear %s: %w", path, err)
		}
		return nil
	}
	if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// stablePackageVersion reports whether version is a stable "x.y.z" release,
// which is the only channel the public updater may follow (Rust
// prepare_install::stable_version).
func stablePackageVersion(version string) bool {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return strings.TrimSpace(version) != "0.0.0"
}

func platformEntrypoint() string {
	if runtime.GOOS == "windows" {
		return "bin/codex.exe"
	}
	return "bin/codex"
}

// isPlatformEntrypoint reports whether a manifest entrypoint names this
// platform's executable. Rust requires the packaged `bin/codex(.exe)` spelling;
// a locally built package keeps its executable at the package root instead
// (recognized by install.PackageLayoutFromExe and Rust's WinGet layout), so the
// decision is the executable name rather than the directory it sits in.
func isPlatformEntrypoint(entrypoint string) bool {
	entrypoint = filepath.ToSlash(strings.TrimSpace(entrypoint))
	return entrypoint == platformEntrypoint() || entrypoint == filepath.Base(platformEntrypoint())
}

// platformTarget mirrors Rust prepare_install::platform_target.
func platformTarget() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "aarch64-apple-darwin"
	case "darwin/amd64":
		return "x86_64-apple-darwin"
	case "linux/arm64":
		return "aarch64-unknown-linux-musl"
	case "linux/amd64":
		return "x86_64-unknown-linux-musl"
	case "windows/arm64":
		return "aarch64-pc-windows-msvc"
	default:
		return "x86_64-pc-windows-msvc"
	}
}

// copyPackageTree copies a CLI package into destination and returns the digest
// of the copied bytes. Relative file links are materialized; the Unix
// installer's `codex` alias and escaping links are rejected or skipped exactly
// like Rust's package_tree.
func copyPackageTree(root string, destination string) (string, error) {
	return walkPackageTree(root, destination)
}

// packageTreeDigest hashes a package without copying it.
func packageTreeDigest(root string) (string, error) {
	return walkPackageTree(root, "")
}

func walkPackageTree(root string, destination string) (string, error) {
	canonicalRoot := canonicalPath(root)
	if canonicalRoot == "" {
		return "", fmt.Errorf("failed to resolve package directory %s", root)
	}
	paths := []string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to read package %s: %w", root, err)
	}
	hasher := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		if relative == "." {
			continue
		}
		if isUnixPackageAlias(path, relative) {
			continue
		}
		if !pathWithin(canonicalPath(path), canonicalRoot) {
			return "", errors.New("package link escapes its root")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		hasher.Write([]byte(relative))
		hasher.Write([]byte{0})
		if info.IsDir() {
			if info.Mode()&fs.ModeSymlink != 0 {
				return "", errors.New("package contains a directory link")
			}
			hasher.Write([]byte("directory"))
			if destination != "" {
				if err := os.MkdirAll(filepath.Join(destination, relative), info.Mode().Perm()); err != nil {
					return "", err
				}
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("package contains an unsupported file")
		}
		hasher.Write([]byte("file"))
		fileHasher := sha256.New()
		source, err := os.Open(path)
		if err != nil {
			return "", err
		}
		var writers []io.Writer
		writers = append(writers, fileHasher)
		var target *os.File
		if destination != "" {
			targetPath := filepath.Join(destination, relative)
			target, err = os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
			if err != nil {
				_ = source.Close()
				return "", err
			}
			writers = append(writers, target)
		}
		_, copyErr := io.Copy(io.MultiWriter(writers...), source)
		closeErr := source.Close()
		if target != nil {
			if err := target.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		hasher.Write(fileHasher.Sum(nil))
		if runtime.GOOS != "windows" {
			hasher.Write([]byte{byte(info.Mode().Perm() & 0o777)})
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// isUnixPackageAlias reports whether path is the `codex` -> `bin/codex` alias
// the Unix installer adds outside the package layout.
func isUnixPackageAlias(path, relative string) bool {
	if runtime.GOOS == "windows" || relative != "codex" {
		return false
	}
	link, err := os.Readlink(path)
	if err != nil {
		return false
	}
	return filepath.ToSlash(link) == "bin/codex"
}

// linkUnixPackageAlias adds the installer's `codex` alias when the staged
// package has no top-level launcher of its own.
func linkUnixPackageAlias(stage string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	alias := filepath.Join(stage, "codex")
	if pathExistsNoFollow(alias) {
		return nil
	}
	if err := os.Symlink("bin/codex", alias); err != nil {
		return fmt.Errorf("failed to link the packaged Codex launcher: %w", err)
	}
	return nil
}

// canonicalPath resolves a path the way Rust's canonicalize does, returning ""
// when the path does not exist. On Windows this follows the installer's
// junctions as well as ordinary symlinks.
func canonicalPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	resolved, err := resolveFinalPath(path)
	if err != nil {
		return ""
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return ""
	}
	return filepath.Clean(absolute)
}
