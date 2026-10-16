package appserverdaemon

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"codex_go/install"
)

// Daemon release pruning (Rust app-server-daemon/src/release_pruning.rs, #53827).
//
// A managed daemon keeps every release it ever selected inside
// `<home>/packages/app-server-daemon/releases`, so retiring the ones nothing can
// still run is what bounds disk usage. Rust only deletes a release when all of
// the following hold, and this port keeps the same ordering and fail-closed
// choices:
//
//   - the root is the dedicated package directory, every inspected entry lives on
//     that directory's own mount, and no migration is in progress
//     (release_pruning.rs:140-149);
//   - `selection-history` was written for the identity of the `current` link that
//     is published now (release_pruning.rs:162-168);
//   - the release is neither the current nor the previous selection, does not
//     contain the calling executable, is not the package a running server or
//     updater uses, carries a `codex-package.json` naming a managed release, and
//     every entry inside it belongs to the same mount
//     (release_pruning.rs:190-242);
//   - the release is retired (renamed under `.retired.`) before it is deleted, so
//     a partial failure is reported once and never retried
//     (release_pruning.rs:243-248).
//
// Both Rust call sites treat a failure as "skip pruning" and log it, never as an
// install failure (`lib.rs` restart path, `prepare_install.rs::prepare_from_package`).
const (
	// selectionHistoryFileName records the current and previous daemon selections
	// bound to the identity of the `current` link (Rust "selection-history").
	selectionHistoryFileName = "selection-history"
	// retiredReleasePrefix names the directory a release is renamed into before it
	// is deleted (Rust `.retired.`).
	retiredReleasePrefix = ".retired."
	// migrationCurrentName marks an in-progress legacy-to-dedicated migration.
	migrationCurrentName = ".migration-current"
)

var (
	errInvalidDaemonPackageDirectory = errors.New("invalid daemon package directory")
	errStaleDaemonSelection          = errors.New("invalid or stale daemon selection")
	errUnknownRunningPackage         = errors.New("unknown running package")
)

// pruneRetiredReleaseDeleteHook forces a deletion failure in tests. Production
// leaves it nil.
var pruneRetiredReleaseDeleteHook func(string) error

// releaseSelection is the parsed `selection-history` file: the file name of the
// current selection, the file name of the previous one (empty when unknown), and
// the identity of the `current` link this history was written for.
type releaseSelection struct {
	name     string
	previous string
	identity string
	raw      string
}

// recordDaemonSelection mirrors Rust release_pruning::record_selection
// (release_pruning.rs:76-91): record the selection that is published now and the
// one it replaced, bound to the identity of the `current` link. Callers hold the
// installer lock and treat a failure as a warning.
func recordDaemonSelection(root, previousRelease string) error {
	current := filepath.Join(root, "current")
	selected, err := resolveSelectionTarget(current)
	if err != nil {
		return err
	}
	// Only the file name of the previous selection is kept. A previous selection
	// that came from a shared install is conservatively retained under its own
	// name, which is what Rust records too.
	previous := ""
	if trimmed := strings.TrimSpace(previousRelease); trimmed != "" {
		previous = filepath.Base(trimmed)
	}
	name := filepath.Base(selected)
	identity, err := selectionIdentity(current)
	if err != nil {
		return err
	}
	body := name + "\n" + previous + "\n" + identity + "\n"
	file, err := os.CreateTemp(root, ".selection-history-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}
	if _, err := file.WriteString(body); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(temporary, filepath.Join(root, selectionHistoryFileName)); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// readDaemonSelection parses the recorded selection. The raw text is kept so the
// prune path can compare it byte for byte with what the current link implies.
func readDaemonSelection(root string) (*releaseSelection, error) {
	data, err := os.ReadFile(filepath.Join(root, selectionHistoryFileName))
	if err != nil {
		return nil, err
	}
	raw := string(data)
	lines := strings.Split(raw, "\n")
	if len(lines) < 3 {
		return nil, errors.New("missing previous selection")
	}
	return &releaseSelection{
		name:     strings.TrimSuffix(lines[0], "\r"),
		previous: strings.TrimSuffix(lines[1], "\r"),
		identity: strings.TrimSuffix(lines[2], "\r"),
		raw:      raw,
	}, nil
}

// pruneDaemonReleases mirrors Rust release_pruning::prune
// (release_pruning.rs:134-251).
func pruneDaemonReleases(daemon *Daemon, root string, settings *DaemonSettings) error {
	home := daemonCodexHome(daemon)
	if strings.TrimSpace(home) == "" {
		return errors.New("missing Codex home")
	}
	releases := filepath.Join(root, releasesDirName)
	mount, err := ownedMount(filepath.Join(home, "packages"))
	if err != nil {
		return err
	}
	// Require a real dedicated root, never a shared alias or an in-progress migration.
	if filepath.Clean(root) != filepath.Clean(filepath.Join(home, "packages", daemonPackagesDirname)) {
		return errInvalidDaemonPackageDirectory
	}
	if id, err := ownedMount(root); err != nil || id != mount {
		return errInvalidDaemonPackageDirectory
	}
	if id, err := ownedMount(releases); err != nil || id != mount {
		return errInvalidDaemonPackageDirectory
	}
	if pathExistsNoFollow(filepath.Join(root, migrationCurrentName)) {
		return errInvalidDaemonPackageDirectory
	}
	canonicalReleases, err := resolveReleasePath(releases)
	if err != nil {
		return err
	}
	current := filepath.Join(root, "current")
	selected, err := resolveSelectionTarget(current)
	if err != nil {
		return err
	}
	selection, err := readDaemonSelection(root)
	if err != nil {
		return err
	}
	identity, err := selectionIdentity(current)
	if err != nil {
		return err
	}
	name := filepath.Base(selected)
	if filepath.Dir(selected) != canonicalReleases ||
		selection.raw != name+"\n"+selection.previous+"\n"+identity+"\n" ||
		(selection.previous != "" && filepath.Base(selection.previous) != selection.previous) {
		return errStaleDaemonSelection
	}
	paths := daemon.BackendPaths(settings)
	server := NewPIDBackend(paths)
	updater := NewPIDUpdateLoopBackend(paths)
	serverRunning, err := server.IsStartingOrRunning()
	if err != nil {
		return err
	}
	if !serverRunning {
		if socket := daemonSocketPath(daemon); socket != "" {
			if _, err := probeAppServerVersionOnSocket(socket, ControlSocketProbeTimeout); err == nil {
				return errors.New("unmanaged daemon")
			}
		}
	}
	// Rust identifies each worker's package by the path digest of its running
	// executable (`managed_install::path_digest`). Go's persisted identity carries
	// only the byte digest, so this check stays conservative: a release whose
	// entrypoint hashes to a running image is retained even when it is a different
	// directory. Nothing in use is ever deleted; at worst a duplicate survives.
	var runningDigests []string
	for _, worker := range []*PIDBackend{server, updater} {
		running, err := worker.IsStartingOrRunning()
		if err != nil {
			return err
		}
		if !running {
			continue
		}
		workerIdentity, err := worker.RunningExecutableIdentity()
		if err != nil {
			return err
		}
		if workerIdentity == nil || strings.TrimSpace(workerIdentity.Digest) == "" {
			return errUnknownRunningPackage
		}
		runningDigests = append(runningDigests, workerIdentity.Digest)
	}
	caller, err := os.Executable()
	if err != nil {
		return err
	}
	caller = canonicalPath(caller)
	entries, err := os.ReadDir(canonicalReleases)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(canonicalReleases, entry.Name())
		if path == selected || entry.Name() == selection.previous || pathWithin(caller, path) {
			continue
		}
		if id, err := ownedMount(path); err != nil || id != mount {
			continue
		}
		manifest, ok := readReleaseManifest(path)
		if !ok {
			continue
		}
		// Accept only managed release names, never arbitrary or legacy folders.
		if !managedReleaseName(entry.Name(), manifest) {
			continue
		}
		entrypoint, err := resolveReleasePath(filepath.Join(path, filepath.FromSlash(manifest.Entrypoint)))
		if err != nil {
			continue
		}
		if digest, err := install.ExecutableIdentityFromFile(entrypoint); err == nil &&
			containsString(runningDigests, digest.Digest) {
			continue
		}
		owned, err := releaseOwnedByMount(path, mount)
		if err != nil {
			return err
		}
		if !owned {
			continue
		}
		if err := retireRelease(canonicalReleases, path); err != nil {
			return err
		}
	}
	return nil
}

// pruneDaemonReleasesBestEffort runs pruning without failing the caller, which is
// how both Rust call sites treat it (a debug-level "skipped daemon release pruning").
func pruneDaemonReleasesBestEffort(daemon *Daemon, root string, settings *DaemonSettings) {
	if err := pruneDaemonReleases(daemon, root, settings); err != nil {
		daemon.Diagnostic("warning: skipped daemon release pruning: %v", err)
	}
}

// retireRelease renames a release out of `releases` and only then deletes it, so a
// partial deletion is reported once and never retried (release_pruning.rs:243-248).
func retireRelease(releases, path string) error {
	retired, err := os.MkdirTemp(releases, retiredReleasePrefix)
	if err != nil {
		return err
	}
	target := filepath.Join(retired, "release")
	if err := os.Rename(path, target); err != nil {
		_ = os.RemoveAll(retired)
		return err
	}
	if pruneRetiredReleaseDeleteHook != nil {
		if err := pruneRetiredReleaseDeleteHook(target); err != nil {
			return err
		}
	}
	return os.RemoveAll(retired)
}

// releaseOwnedByMount reports whether every entry of a release belongs to mount.
// Anything else (a bind mount, a volume mount point, an unreadable child) makes
// the release unrecognized, and unrecognized packages are never deleted.
func releaseOwnedByMount(path, mount string) (bool, error) {
	pending := []string{path}
	for len(pending) > 0 {
		child := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		// The Unix installer adds this alias; it never traverses outside the release.
		if runtime.GOOS != "windows" && filepath.Base(child) == "codex" &&
			filepath.Dir(child) == path {
			if target, err := os.Readlink(child); err == nil && filepath.ToSlash(target) == "bin/codex" {
				continue
			}
		}
		id, err := ownedMount(child)
		if err != nil || id != mount {
			return false, nil
		}
		info, err := os.Lstat(child)
		if err != nil {
			return false, err
		}
		if !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(child)
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			pending = append(pending, filepath.Join(child, entry.Name()))
		}
	}
	return true, nil
}

// readReleaseManifest reads the part of `codex-package.json` a release must carry
// to be recognized (Rust release_pruning::Manifest).
func readReleaseManifest(path string) (*daemonPackageManifest, bool) {
	file, err := os.Open(filepath.Join(path, packageMetadataFileName))
	if err != nil {
		return nil, false
	}
	defer file.Close()
	var manifest daemonPackageManifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return nil, false
	}
	return &manifest, true
}

// managedReleaseName reports whether a release directory name is one the installer
// creates: `<version>-<target>` or `local-<digest>-<target>` with a hex digest.
// Rust additionally requires the `bin/codex` entrypoint (release_pruning.rs:206-218).
func managedReleaseName(name string, manifest *daemonPackageManifest) bool {
	if manifest == nil {
		return false
	}
	if manifest.Entrypoint != "bin/codex" && manifest.Entrypoint != "bin/codex.exe" {
		return false
	}
	if manifest.Target == "" {
		return false
	}
	if manifest.Version != "" && name == manifest.Version+"-"+manifest.Target {
		return true
	}
	if !strings.HasPrefix(name, "local-") || !strings.HasSuffix(name, "-"+manifest.Target) {
		return false
	}
	return isHexDigest(strings.TrimSuffix(strings.TrimPrefix(name, "local-"), "-"+manifest.Target))
}

func isHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// resolveReleasePath canonicalizes a path the way Rust's `Path::canonicalize`
// does, which is what makes the recorded selection and the published `current`
// link comparable.
func resolveReleasePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// resolveSelectionTarget returns the release directory the published `current`
// link points at. Rust canonicalizes the link, which follows the reparse point on
// Windows; Go's filepath.EvalSymlinks does not follow directory junctions, so the
// link is read first and only then canonicalized.
func resolveSelectionTarget(current string) (string, error) {
	if target, err := os.Readlink(current); err == nil && strings.TrimSpace(target) != "" {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		return resolveReleasePath(target)
	}
	return resolveReleasePath(current)
}
