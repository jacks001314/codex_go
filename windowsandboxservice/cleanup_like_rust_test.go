//go:build windows

package windowsandboxservice

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// fakeCleanupFinisher records the pinned home the lifecycle handed to native
// cleanup and runs the desktop closure in its place.
type fakeCleanupFinisher struct {
	home     *string
	reports  []string
	nativeOK bool
}

func (finisher *fakeCleanupFinisher) Finish(codexHome *string, report func(string), cleanUpDesktop func() error) error {
	finisher.home = codexHome
	finisher.nativeOK = true
	if report != nil {
		report("fake native cleanup")
		finisher.reports = append(finisher.reports, "fake native cleanup")
	}
	if cleanUpDesktop != nil {
		return cleanUpDesktop()
	}
	return nil
}

// useCleanupSeams replaces the destructive orchestration seams with recording
// stand-ins so the test never touches real accounts or the event log's effects.
func useCleanupSeams(t *testing.T) {
	t.Helper()
	previousRemoveAll := cleanupRemoveAll
	previousRemoveEmptyDir := cleanupRemoveEmptyDir
	previousPinAncestors := cleanupPinAncestors
	previousWithOwner := cleanupWithOwner
	previousRevoke := cleanupRevokeSandboxACE
	previousPackages := runtimeRegisteredPackages
	previousFullName := runtimeProcessPackageFullName
	t.Cleanup(func() {
		cleanupRemoveAll = previousRemoveAll
		cleanupRemoveEmptyDir = previousRemoveEmptyDir
		cleanupPinAncestors = previousPinAncestors
		cleanupWithOwner = previousWithOwner
		cleanupRevokeSandboxACE = previousRevoke
		runtimeRegisteredPackages = previousPackages
		runtimeProcessPackageFullName = previousFullName
	})
	cleanupWithOwner = func(_ windows.Token, operation func() error) error { return operation() }
	cleanupPinAncestors = func(_ string, handles *[]windows.Handle) error {
		handle, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			return err
		}
		*handles = append(*handles, handle)
		return nil
	}
	cleanupRevokeSandboxACE = func(string) error { return nil }
	runtimeProcessPackageFullName = func() (string, bool, error) {
		return cleanupTestCurrentPackage, true, nil
	}
	runtimeRegisteredPackages = func(string, string) ([]string, error) { return nil, nil }
}

const (
	cleanupTestCurrentPackage   = "OpenAI.Codex_2p2nqsd0c76g0_1.0.0.0_x64__abc"
	cleanupTestSuccessorPackage = "OpenAI.Codex_2p2nqsd0c76g0_1.1.0.0_x64__abc"
)

// newCleanupTestInstallation pins the home like register_authenticated_user and
// keeps a real handle as the home guard so releaseGuard closes it exactly once.
func newCleanupTestInstallation(
	t *testing.T,
	home string,
	desktop *windowssandbox.DesktopInstallation,
) *lifecycleInstallation {
	t.Helper()
	var handles []windows.Handle
	var guard windows.Handle
	if home != "" {
		if err := pinExistingAncestors(home, &handles); err != nil {
			t.Fatalf("pin home %s: %v", home, err)
		}
		created, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			t.Fatalf("create home guard: %v", err)
		}
		guard = created
	}
	installation := &lifecycleInstallation{
		record: &windowssandbox.InstallationRecord{
			CodexHome:           home,
			DesktopInstallation: desktop,
		},
		codexHome: home,
		handles:   handles,
		guard:     guard,
	}
	t.Cleanup(func() {
		closeHandles(installation.handles)
		if installation.guard != 0 {
			_ = windows.CloseHandle(installation.guard)
		}
	})
	return installation
}

func writeCleanUninstallPreference(t *testing.T, home string, enabled bool) {
	t.Helper()
	value := "false"
	if enabled {
		value = "true"
	}
	if err := os.WriteFile(
		filepath.Join(home, "config.toml"),
		[]byte("[desktop]\nuninstall_codex_home = "+value+"\n"),
		0o600,
	); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}

func TestCleanUpResourcesRemovesCreatedHomeAndRuntimeCacheLikeRust(t *testing.T) {
	useCleanupSeams(t)
	home := t.TempDir()
	cache := t.TempDir()
	writeCleanUninstallPreference(t, home, true)
	if err := os.MkdirAll(filepath.Join(cache, "codex-runtimes", "x"), 0o700); err != nil {
		t.Fatalf("create runtime cache: %v", err)
	}
	lifecycle := &Lifecycle{
		installation: newCleanupTestInstallation(t, home, &windowssandbox.DesktopInstallation{
			CreatedCodexHome: true,
			CacheHome:        cache,
		}),
	}
	finisher := &fakeCleanupFinisher{}
	if err := lifecycle.CleanUpResources(finisher, nil); err != nil {
		t.Fatalf("CleanUpResources() error = %v", err)
	}
	if finisher.home == nil || *finisher.home != home {
		t.Fatalf("native cleanup home = %v, want %q", finisher.home, home)
	}
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("desktop-created home still exists: %v", err)
	}
	if _, err := os.Stat(cache); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty cache home still exists: %v", err)
	}
	if lifecycle.installation.guardPresent() {
		t.Fatal("home guard was not released before removal")
	}
}

func TestCleanUpResourcesRevokesSandboxACEsAndPreservesARetainedHomeLikeRust(t *testing.T) {
	useCleanupSeams(t)
	home := t.TempDir()
	writeCleanUninstallPreference(t, home, false)
	revoked := []string{}
	cleanupRevokeSandboxACE = func(path string) error {
		revoked = append(revoked, path)
		return nil
	}
	lifecycle := &Lifecycle{
		installation: newCleanupTestInstallation(t, home, &windowssandbox.DesktopInstallation{
			CreatedCodexHome: true,
		}),
	}
	if err := lifecycle.CleanUpResources(&fakeCleanupFinisher{}, nil); err != nil {
		t.Fatalf("CleanUpResources() error = %v", err)
	}
	if len(revoked) != 1 || revoked[0] != home {
		t.Fatalf("revoked ACL paths = %v, want [%q]", revoked, home)
	}
	// Retained data keeps its home; only an empty root is pruned.
	if _, err := os.Stat(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("retained home contents were removed: %v", err)
	}
}

func TestCleanUpResourcesPrunesAnEmptyRetainedHomeLikeRust(t *testing.T) {
	useCleanupSeams(t)
	home := t.TempDir()
	lifecycle := &Lifecycle{
		installation: newCleanupTestInstallation(t, home, &windowssandbox.DesktopInstallation{
			CreatedCodexHome: true,
		}),
	}
	if err := lifecycle.CleanUpResources(&fakeCleanupFinisher{}, nil); err != nil {
		t.Fatalf("CleanUpResources() error = %v", err)
	}
	// The retained home was empty, so best-effort pruning removes it only after
	// native cleanup finished.
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty retained home still exists: %v", err)
	}
}

func TestCleanUpResourcesKeepsDesktopDataWhenOwnerReinstalledLikeRust(t *testing.T) {
	useCleanupSeams(t)
	home := t.TempDir()
	cache := t.TempDir()
	writeCleanUninstallPreference(t, home, true)
	if err := os.MkdirAll(filepath.Join(cache, "codex-runtimes", "x"), 0o700); err != nil {
		t.Fatalf("create runtime cache: %v", err)
	}
	runtimeRegisteredPackages = func(string, string) ([]string, error) {
		return []string{cleanupTestSuccessorPackage}, nil
	}
	revoked := []string{}
	cleanupRevokeSandboxACE = func(path string) error {
		revoked = append(revoked, path)
		return nil
	}
	installation := newCleanupTestInstallation(t, home, &windowssandbox.DesktopInstallation{
		CreatedCodexHome: true,
		CacheHome:        cache,
	})
	record := &windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: home,
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts: []windowssandbox.RuntimeAccountRegistration{{
				Account: windowssandbox.SandboxRuntimeAccountOffline,
				UserSID: "S-1-5-21-1-2-3-1001",
			}},
		},
	}
	lifecycle := &Lifecycle{installation: installation}
	if err := lifecycle.CleanUpResources(&fakeCleanupFinisher{}, record); err != nil {
		t.Fatalf("CleanUpResources() error = %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("reinstalled owner's home was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "codex-runtimes")); err != nil {
		t.Fatalf("reinstalled owner's runtime cache was removed: %v", err)
	}
	if len(revoked) != 0 {
		t.Fatalf("revoked ACL paths = %v, want none", revoked)
	}
}

func TestCleanUpResourcesRemovesOwnerDataOnlyAfterUninstallAndWithoutRetentionLikeRust(t *testing.T) {
	useCleanupSeams(t)
	home := t.TempDir()
	writeCleanUninstallPreference(t, home, true)
	runtimeRegisteredPackages = func(userSID string, _ string) ([]string, error) {
		if userSID == "S-1-5-21-1-2-3-1000" {
			return []string{cleanupTestCurrentPackage}, nil
		}
		return nil, nil
	}
	installation := newCleanupTestInstallation(t, home, &windowssandbox.DesktopInstallation{
		CreatedCodexHome: true,
	})
	record := &windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: home,
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts: []windowssandbox.RuntimeAccountRegistration{{
				Account: windowssandbox.SandboxRuntimeAccountOffline,
				UserSID: "S-1-5-21-1-2-3-1001",
			}},
		},
	}
	lifecycle := &Lifecycle{installation: installation}
	lifecycle.uninstalling.Store(true)
	if err := lifecycle.CleanUpResources(&fakeCleanupFinisher{}, record); err != nil {
		t.Fatalf("CleanUpResources() error = %v", err)
	}
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("uninstalled owner's home still exists: %v", err)
	}
}

func TestCleanUpResourcesSkipsDesktopDirectoriesWithoutADesktopRecordLikeRust(t *testing.T) {
	useCleanupSeams(t)
	home := t.TempDir()
	writeCleanUninstallPreference(t, home, true)
	revoked := []string{}
	cleanupRevokeSandboxACE = func(path string) error {
		revoked = append(revoked, path)
		return nil
	}
	lifecycle := &Lifecycle{installation: newCleanupTestInstallation(t, home, nil)}
	if err := lifecycle.CleanUpResources(&fakeCleanupFinisher{}, nil); err != nil {
		t.Fatalf("CleanUpResources() error = %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("home without a desktop record was removed: %v", err)
	}
	if len(revoked) != 0 {
		t.Fatalf("revoked ACL paths = %v, want none", revoked)
	}
}

func TestCleanUpResourcesReportsDesktopDirectoryFailuresLikeRust(t *testing.T) {
	useCleanupSeams(t)
	home := t.TempDir()
	writeCleanUninstallPreference(t, home, true)
	cleanupRemoveAll = func(path string) error { return errors.New("sharing violation") }
	lifecycle := &Lifecycle{
		installation: newCleanupTestInstallation(t, home, &windowssandbox.DesktopInstallation{
			CreatedCodexHome: true,
		}),
	}
	err := lifecycle.CleanUpResources(&fakeCleanupFinisher{}, nil)
	if err == nil {
		t.Fatal("CleanUpResources() error = nil, want the desktop failure")
	}
	want := "remove desktop directories: remove desktop-created codex home: failed, sharing violation"
	if err.Error() != "remove packaged Windows sandbox resources: "+want {
		t.Fatalf("CleanUpResources() error = %q", err.Error())
	}
}
