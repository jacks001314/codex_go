package appserverdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrepareDaemonInstallLeavesALegacySelectionAloneLikeRust ports Rust
// `legacy_selection_is_not_migrated_or_refreshed` (#45558): a pre-dedicated
// standalone installation keeps serving from its own package root, so seeding a
// daemon must neither create a dedicated root nor move the selection.
func TestPrepareDaemonInstallLeavesALegacySelectionAloneLikeRust(t *testing.T) {
	home, legacyRelease := legacyStandaloneHome(t)
	source := filepath.Join(t.TempDir(), "cli-package")
	entrypoint := writeTestPackage(t, source, "1.2.3", true)
	stubPrepareSource(t, source, entrypoint, "1.2.3")

	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	if err := prepareDaemonInstall(daemon, &DaemonSettings{}); err != nil {
		t.Fatalf("prepareDaemonInstall() error = %v", err)
	}

	legacyCurrent := filepath.Join(home, "packages", legacyPackagesDirname, "current")
	if got, want := canonicalPath(legacyCurrent), canonicalPath(legacyRelease); got == "" || got != want {
		t.Fatalf("legacy current = %q, want %q", got, want)
	}
	dedicated := filepath.Join(home, "packages", daemonPackagesDirname)
	if _, err := os.Lstat(dedicated); !os.IsNotExist(err) {
		t.Fatalf("dedicated package root appeared at %s: %v", dedicated, err)
	}
}

// TestPrepareDaemonInstallPreservesTheStandaloneUpdateChannelLikeRust ports
// Rust `standalone_seed_preserves_explicit_pin_or_latest_channel` (#45558): the
// copied release only follows the public update channel when the CLI itself was
// the standalone package the channel already named.
func TestPrepareDaemonInstallPreservesTheStandaloneUpdateChannelLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		followsLatest bool
	}{
		{name: "explicit pin", followsLatest: false},
		{name: "latest channel", followsLatest: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := privateDaemonTestHome(t)
			standalone := filepath.Join(home, "packages", legacyPackagesDirname)
			releaseName := "1.2.3-" + platformTarget()
			release := filepath.Join(standalone, releasesDirName, releaseName)
			entrypoint := writeTestPackage(t, release, "1.2.3", true)
			if err := selectDaemonRelease(standalone, release); err != nil {
				t.Fatalf("selectDaemonRelease(standalone) error = %v", err)
			}
			if testCase.followsLatest {
				if err := os.WriteFile(filepath.Join(standalone, autoUpdateVersionFileName), []byte(releaseName), 0o600); err != nil {
					t.Fatalf("WriteFile(standalone marker) error = %v", err)
				}
			}
			stubPrepareSource(t, release, entrypoint, "1.2.3")

			daemon := NewDaemonForCodexHome(home, "codex-go-test")
			if err := prepareDaemonInstall(daemon, &DaemonSettings{}); err != nil {
				t.Fatalf("prepareDaemonInstall() error = %v", err)
			}

			root := filepath.Join(home, "packages", daemonPackagesDirname)
			selected := canonicalPath(filepath.Join(root, "current"))
			if selected == "" {
				t.Fatalf("dedicated selection %s was not published", filepath.Join(root, "current"))
			}
			markerPath := filepath.Join(root, autoUpdateVersionFileName)
			marker, err := os.ReadFile(markerPath)
			if testCase.followsLatest {
				if err != nil {
					t.Fatalf("ReadFile(auto-update-version) error = %v", err)
				}
				if got, want := string(marker), filepath.Base(selected); got != want {
					t.Fatalf("auto-update-version = %q, want %q", got, want)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("pinned install wrote auto-update-version %q (err = %v)", string(marker), err)
			}
			if got, want := canonicalPath(filepath.Join(standalone, "current")), canonicalPath(release); got == "" || got != want {
				t.Fatalf("standalone current = %q, want the untouched %q", got, want)
			}
		})
	}
}

// TestPrepareDaemonInstallRefusesABrokenSelectionLikeRust ports Rust
// `broken_selection_is_not_a_missing_installation` (#45558): a `current`
// selection whose release disappeared must be reported for repair instead of
// being treated as an absent installation and replaced.
func TestPrepareDaemonInstallRefusesABrokenSelectionLikeRust(t *testing.T) {
	home := privateDaemonTestHome(t)
	root := filepath.Join(home, "packages", daemonPackagesDirname)
	release := filepath.Join(root, releasesDirName, "missing-release")
	if err := os.MkdirAll(release, 0o755); err != nil {
		t.Fatalf("MkdirAll(release) error = %v", err)
	}
	if err := selectDaemonRelease(root, release); err != nil {
		t.Fatalf("selectDaemonRelease() error = %v", err)
	}
	if err := os.RemoveAll(release); err != nil {
		t.Fatalf("RemoveAll(release) error = %v", err)
	}
	source := filepath.Join(t.TempDir(), "cli-package")
	entrypoint := writeTestPackage(t, source, "1.2.3", true)
	stubPrepareSource(t, source, entrypoint, "1.2.3")

	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	err := prepareDaemonInstall(daemon, &DaemonSettings{})
	if err == nil || !strings.Contains(err.Error(), "repair the existing installation") {
		t.Fatalf("prepareDaemonInstall() error = %v", err)
	}
	current := filepath.Join(root, "current")
	if !pathExistsNoFollow(current) {
		t.Fatalf("broken selection %s was removed", current)
	}
	entries, readErr := os.ReadDir(filepath.Join(root, releasesDirName))
	if readErr != nil {
		t.Fatalf("ReadDir(releases) error = %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("a release was staged for a broken selection: %v", entries)
	}
}
