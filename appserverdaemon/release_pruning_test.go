package appserverdaemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pruneReleaseLayout prepares a dedicated daemon package directory the way a
// managed install leaves it: `<home>/packages/app-server-daemon/releases` with the
// pid records and socket outside the package root.
func pruneReleaseLayout(t *testing.T) (home, root string, daemon *Daemon, settings *DaemonSettings) {
	t.Helper()
	home = t.TempDir()
	root = filepath.Join(home, "packages", daemonPackagesDirname)
	if err := os.MkdirAll(filepath.Join(root, releasesDirName), 0o755); err != nil {
		t.Fatalf("MkdirAll(releases) error = %v", err)
	}
	return home, root, &Daemon{Paths: PathsForCodexHome(home)}, &DaemonSettings{}
}

// writePruneRelease writes a managed release the way the installer names it.
func writePruneRelease(t *testing.T, root, version string) string {
	t.Helper()
	release := filepath.Join(root, releasesDirName, version+"-"+platformTarget())
	writeTestPackage(t, release, version, false)
	return release
}

func retiredReleaseDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, releasesDirName))
	if err != nil {
		t.Fatalf("ReadDir(releases) error = %v", err)
	}
	retired := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), retiredReleasePrefix) {
			retired = append(retired, entry.Name())
		}
	}
	return retired
}

// Mirrors Rust #53827 release_pruning::prune (release_pruning.rs:134-251): a
// release nothing can run any more is retired, while the selected release and the
// previous selection the daemon may still roll back to are kept.
func TestPruneDaemonReleasesRetiresUnusedReleasesLikeRust(t *testing.T) {
	_, root, daemon, settings := pruneReleaseLayout(t)
	unused := writePruneRelease(t, root, "0.9.0")
	previousName := "0.9.1-" + platformTarget()
	writePruneRelease(t, root, "0.9.1")
	selected := writePruneRelease(t, root, "1.0.0")
	if err := selectDaemonRelease(root, selected); err != nil {
		t.Fatalf("selectDaemonRelease() error = %v", err)
	}
	if err := recordDaemonSelection(root, filepath.Join(root, releasesDirName, previousName)); err != nil {
		t.Fatalf("recordDaemonSelection() error = %v", err)
	}

	if err := pruneDaemonReleases(daemon, root, settings); err != nil {
		t.Fatalf("pruneDaemonReleases() error = %v", err)
	}
	if pathExistsNoFollow(unused) {
		t.Fatalf("unused release %s survived pruning", unused)
	}
	if !pathExistsNoFollow(selected) {
		t.Fatalf("selected release %s was pruned", selected)
	}
	if !pathExistsNoFollow(filepath.Join(root, releasesDirName, previousName)) {
		t.Fatalf("previous selection %s was pruned", previousName)
	}
	if !isRegularFile(ManagedCodexBin(daemon.Paths.CodexHome)) {
		t.Fatalf("managed binary disappeared with pruning")
	}
	if retired := retiredReleaseDirs(t, root); len(retired) != 0 {
		t.Fatalf("retired directories left behind: %v", retired)
	}
}

// The reverse direction of the same guarantee: everything that may still be in
// use stays, and an unrecognized package is never deleted
// (release_pruning.rs:190-242).
func TestPruneDaemonReleasesKeepsInUseReleasesLikeRust(t *testing.T) {
	_, root, daemon, settings := pruneReleaseLayout(t)
	previous := writePruneRelease(t, root, "0.9.1")
	selected := writePruneRelease(t, root, "1.0.0")
	// A package the installer would not have created: its directory name is not a
	// managed release name even though it carries a manifest.
	unmanagedName := "local-not-a-hex-digest-" + platformTarget()
	unmanaged := writePruneRelease(t, root, "2.0.0")
	if err := os.Rename(unmanaged, filepath.Join(root, releasesDirName, unmanagedName)); err != nil {
		t.Fatalf("Rename(unmanaged) error = %v", err)
	}
	// A directory with no manifest at all (a legacy or hand-placed folder).
	legacy := filepath.Join(root, releasesDirName, "legacy-folder")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatalf("MkdirAll(legacy) error = %v", err)
	}
	if err := selectDaemonRelease(root, selected); err != nil {
		t.Fatalf("selectDaemonRelease() error = %v", err)
	}
	if err := recordDaemonSelection(root, previous); err != nil {
		t.Fatalf("recordDaemonSelection() error = %v", err)
	}

	if err := pruneDaemonReleases(daemon, root, settings); err != nil {
		t.Fatalf("pruneDaemonReleases() error = %v", err)
	}
	for _, keep := range []string{
		"selected " + selected,
		"previous " + previous,
		"unmanaged " + filepath.Join(root, releasesDirName, unmanagedName),
		"legacy " + legacy,
	} {
		label, path, _ := strings.Cut(keep, " ")
		if !pathExistsNoFollow(path) {
			t.Fatalf("pruning removed the %s package %s", label, path)
		}
	}
	if _, err := os.Stat(filepath.Join(root, releasesDirName, unmanagedName)); err != nil {
		t.Fatalf("unmanaged package is not readable after pruning: %v", err)
	}
}

// A deletion that only partially succeeds is reported once and never retried: the
// release is already retired out of `releases` before it is deleted
// (release_pruning.rs:243-248), and both call sites treat the failure as "skip
// pruning" rather than an install failure.
func TestPruneDaemonReleasesReportsPartialDeletionLikeRust(t *testing.T) {
	_, root, daemon, settings := pruneReleaseLayout(t)
	first := writePruneRelease(t, root, "0.9.0")
	second := writePruneRelease(t, root, "0.9.1")
	selected := writePruneRelease(t, root, "1.0.0")
	if err := selectDaemonRelease(root, selected); err != nil {
		t.Fatalf("selectDaemonRelease() error = %v", err)
	}
	if err := recordDaemonSelection(root, ""); err != nil {
		t.Fatalf("recordDaemonSelection() error = %v", err)
	}
	previousHook := pruneRetiredReleaseDeleteHook
	pruneRetiredReleaseDeleteHook = func(string) error { return errors.New("injected deletion failure") }
	t.Cleanup(func() { pruneRetiredReleaseDeleteHook = previousHook })

	err := pruneDaemonReleases(daemon, root, settings)
	if err == nil || !strings.Contains(err.Error(), "injected deletion failure") {
		t.Fatalf("pruneDaemonReleases() error = %v, want the injected deletion failure", err)
	}
	if pathExistsNoFollow(first) {
		t.Fatalf("a retired release is still reachable under releases")
	}
	if !pathExistsNoFollow(second) {
		t.Fatalf("the run did not stop at the failing release; %s disappeared", second)
	}
	retired := retiredReleaseDirs(t, root)
	if len(retired) != 1 {
		t.Fatalf("retired directories = %v, want the one failed retirement", retired)
	}
	partial := filepath.Join(root, releasesDirName, retired[0], "release")
	if !isRegularFile(filepath.Join(partial, packageMetadataFileName)) {
		t.Fatalf("the partially deleted tree is not reproducible under %s", partial)
	}

	// The failure is not fatal for a caller, and a later run removes what is left
	// without touching the already retired tree.
	pruneDaemonReleasesBestEffort(daemon, root, settings)
	pruneRetiredReleaseDeleteHook = nil
	if err := pruneDaemonReleases(daemon, root, settings); err != nil {
		t.Fatalf("second pruneDaemonReleases() error = %v", err)
	}
	if pathExistsNoFollow(second) {
		t.Fatalf("the remaining unused release %s survived the second run", second)
	}
	if !pathExistsNoFollow(partial) {
		t.Fatalf("the failed retirement was retried and deleted")
	}
}
