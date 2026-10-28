package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestCleanupMarketplaceStagingReclaimsAgedLegacyDirectories(t *testing.T) {
	installRoot := t.TempDir()
	stagingRoot := filepath.Join(installRoot, marketplaceStagingDirName)
	legacyAdd := filepath.Join(stagingRoot, "marketplace-add-legacy")
	legacyUpgrade := filepath.Join(stagingRoot, "marketplace-upgrade-legacy")
	unrelated := filepath.Join(stagingRoot, "marketplace-plugin-source-legacy")
	unrelatedFile := filepath.Join(stagingRoot, "marketplace-add-file")
	for _, path := range []string{legacyAdd, legacyUpgrade, unrelated} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create staging directory %s: %v", path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(legacyAdd, "payload"), []byte("old add"), 0o600); err != nil {
		t.Fatalf("write legacy add payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "payload"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write unrelated payload: %v", err)
	}
	if err := os.WriteFile(unrelatedFile, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}

	// Base synthetic ages on the filesystem clock, which can differ from the
	// wall clock.
	info, err := os.Stat(legacyAdd)
	if err != nil {
		t.Fatalf("stat legacy add: %v", err)
	}
	now := info.ModTime()
	for _, tc := range []struct {
		age                  time.Duration
		wantAdd, wantUpgrade bool
	}{
		{age: 0, wantAdd: true, wantUpgrade: true},
		{age: legacyMarketplaceUpgradeMaxAge + time.Second, wantAdd: true},
		{age: legacyMarketplaceAddMaxAge + time.Second},
	} {
		cleanupMarketplaceStaging(installRoot, now.Add(tc.age))
		if got := pathExists(legacyAdd); got != tc.wantAdd {
			t.Fatalf("legacy add exists=%t after age %s, want %t", got, tc.age, tc.wantAdd)
		}
		if got := pathExists(legacyUpgrade); got != tc.wantUpgrade {
			t.Fatalf("legacy upgrade exists=%t after age %s, want %t", got, tc.age, tc.wantUpgrade)
		}
	}
	if !pathExists(unrelated) {
		t.Fatal("unrelated staging directory was removed")
	}
	if data, err := os.ReadFile(filepath.Join(unrelated, "payload")); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated payload changed: %q, %v", data, err)
	}
	if !pathExists(unrelatedFile) {
		t.Fatal("non-directory staging entry was removed")
	}
}

func TestCleanupMarketplaceStagingLimitsEachPass(t *testing.T) {
	installRoot := t.TempDir()
	stagingRoot := filepath.Join(installRoot, marketplaceStagingDirName)
	count := maxMarketplaceStagingCleanup + 2
	for index := 0; index < count; index++ {
		path := filepath.Join(stagingRoot, "marketplace-add-"+string(rune('a'+index)))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create staging directory %s: %v", path, err)
		}
	}
	info, err := os.Stat(stagingRoot)
	if err != nil {
		t.Fatalf("stat staging root: %v", err)
	}
	cleanupMarketplaceStaging(installRoot, info.ModTime().Add(legacyMarketplaceAddMaxAge+time.Second))
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		t.Fatalf("read staging root: %v", err)
	}
	if len(entries) != count-maxMarketplaceStagingCleanup {
		t.Fatalf("staging entries after cleanup = %d, want %d", len(entries), count-maxMarketplaceStagingCleanup)
	}
}

func TestCleanupMarketplaceStagingSkipsSymlinks(t *testing.T) {
	installRoot := t.TempDir()
	stagingRoot := filepath.Join(installRoot, marketplaceStagingDirName)
	outside := t.TempDir()
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		t.Fatalf("create staging root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "payload"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write outside payload: %v", err)
	}
	alias := filepath.Join(stagingRoot, "marketplace-upgrade-link")
	if err := os.Symlink(outside, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cleanupMarketplaceStaging(installRoot, time.Now().Add(legacyMarketplaceAddMaxAge+time.Second))
	if data, err := os.ReadFile(filepath.Join(outside, "payload")); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target payload changed: %q, %v", data, err)
	}
	if !pathExists(alias) {
		t.Fatal("symlink staging entry was removed")
	}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Mirrors Rust #55689's staging guard: the checkout lives below a directory
// whose owner lock is held until Close, so a concurrent cleanup pass preserves
// an active checkout and Close removes the directory.
func TestMarketplaceStagingGuardOwnsCheckoutAndCleansUp(t *testing.T) {
	installRoot := t.TempDir()
	staged, err := newMarketplaceStagingDir(installRoot)
	if err != nil {
		t.Fatalf("newMarketplaceStagingDir: %v", err)
	}
	dir := filepath.Dir(staged.Path())
	if base := filepath.Base(dir); !strings.HasPrefix(base, marketplaceStagingDirPrefix()) {
		t.Fatalf("staging directory name = %q, want prefix %q", base, marketplaceStagingDirPrefix())
	}
	if _, err := os.Stat(filepath.Join(dir, marketplaceOwnerLockName)); err != nil {
		t.Fatalf("owner lock missing: %v", err)
	}
	if err := os.MkdirAll(staged.Path(), 0o755); err != nil {
		t.Fatalf("create checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staged.Path(), "payload"), []byte("clone"), 0o600); err != nil {
		t.Fatalf("write checkout payload: %v", err)
	}

	cleanupMarketplaceStaging(installRoot, time.Now())
	if data, err := os.ReadFile(filepath.Join(staged.Path(), "payload")); err != nil || string(data) != "clone" {
		t.Fatalf("active checkout was reclaimed: %q, %v", data, err)
	}

	staged.Close()
	if pathExists(dir) {
		t.Fatal("the staging directory survived Close")
	}
}

// Mirrors Rust #55689's ownership rules: a directory without an owner lock (a
// crash before publication) or with a released lock is abandoned, a locked
// checkout is preserved, and other platforms' or unrelated entries are never
// reclaimed.
func TestCleanupMarketplaceStagingReclaimsOnlyUnlockedOwnedDirectories(t *testing.T) {
	installRoot := t.TempDir()
	stagingRoot := filepath.Join(installRoot, marketplaceStagingDirName)
	crashDir := filepath.Join(stagingRoot, marketplaceStagingDirPrefix()+"crash")
	unlockedDir := filepath.Join(stagingRoot, marketplaceStagingDirPrefix()+"unlocked")
	heldDir := filepath.Join(stagingRoot, marketplaceStagingDirPrefix()+"held")
	foreignDir := filepath.Join(stagingRoot, "marketplace-otheros-live")
	unrelatedDir := filepath.Join(stagingRoot, "not-a-checkout")
	for _, path := range []string{crashDir, unlockedDir, heldDir, foreignDir, unrelatedDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create staging entry %s: %v", path, err)
		}
	}
	unlockedLock := filepath.Join(unlockedDir, marketplaceOwnerLockName)
	if err := os.WriteFile(unlockedLock, nil, 0o600); err != nil {
		t.Fatalf("write unlocked owner lock: %v", err)
	}
	heldLockPath := filepath.Join(heldDir, marketplaceOwnerLockName)
	if err := os.WriteFile(heldLockPath, nil, 0o600); err != nil {
		t.Fatalf("write held owner lock: %v", err)
	}
	held := flock.New(heldLockPath)
	locked, err := held.TryLock()
	if err != nil || !locked {
		t.Fatalf("hold owner lock: locked=%v err=%v", locked, err)
	}

	cleanupMarketplaceStaging(installRoot, time.Now())
	for _, path := range []string{crashDir, unlockedDir} {
		if pathExists(path) {
			t.Fatalf("abandoned staging directory %s was not reclaimed", path)
		}
	}
	for _, path := range []string{heldDir, foreignDir, unrelatedDir} {
		if !pathExists(path) {
			t.Fatalf("staging directory %s was reclaimed while it must be preserved", path)
		}
	}

	if err := held.Unlock(); err != nil {
		t.Fatalf("release owner lock: %v", err)
	}
	_ = held.Close()
	cleanupMarketplaceStaging(installRoot, time.Now())
	if pathExists(heldDir) {
		t.Fatal("a released checkout was not reclaimed")
	}
}

// Mirrors Rust #55689's bounded batch and symlink rules.
func TestCleanupMarketplaceStagingBoundsBatchAndSkipsSymlinks(t *testing.T) {
	installRoot := t.TempDir()
	stagingRoot := filepath.Join(installRoot, marketplaceStagingDirName)
	count := maxMarketplaceStagingCleanup + 2
	for index := 0; index < count; index++ {
		path := filepath.Join(stagingRoot, marketplaceStagingDirPrefix()+string(rune('a'+index)))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create staging directory %s: %v", path, err)
		}
	}
	cleanupMarketplaceStaging(installRoot, time.Now())
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		t.Fatalf("read staging root: %v", err)
	}
	if len(entries) != count-maxMarketplaceStagingCleanup {
		t.Fatalf("staging entries after cleanup = %d, want %d", len(entries), count-maxMarketplaceStagingCleanup)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "payload"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write outside payload: %v", err)
	}
	alias := filepath.Join(stagingRoot, marketplaceStagingDirPrefix()+"link")
	if err := os.Symlink(outside, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cleanupMarketplaceStaging(installRoot, time.Now())
	if data, err := os.ReadFile(filepath.Join(outside, "payload")); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target payload changed: %q, %v", data, err)
	}
	if !pathExists(alias) {
		t.Fatal("symlink staging entry was removed")
	}
}

// Mirrors Rust #55689's cleanup-lock contention rule: a concurrent cleanup pass
// leaves the batch for the lock holder.
func TestCleanupMarketplaceStagingRespectsCleanupLock(t *testing.T) {
	installRoot := t.TempDir()
	stagingRoot := filepath.Join(installRoot, marketplaceStagingDirName)
	abandoned := filepath.Join(stagingRoot, marketplaceStagingDirPrefix()+"waiting")
	if err := os.MkdirAll(abandoned, 0o755); err != nil {
		t.Fatalf("create staging directory: %v", err)
	}
	lock := flock.New(filepath.Join(installRoot, marketplaceCleanupLockName))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		t.Fatalf("hold cleanup lock: locked=%v err=%v", locked, err)
	}
	cleanupMarketplaceStaging(installRoot, time.Now())
	if !pathExists(abandoned) {
		t.Fatal("cleanup ran while another pass held the cleanup lock")
	}
	if err := lock.Unlock(); err != nil {
		t.Fatalf("release cleanup lock: %v", err)
	}
	_ = lock.Close()
	cleanupMarketplaceStaging(installRoot, time.Now())
	if pathExists(abandoned) {
		t.Fatal("abandoned staging directory was not reclaimed after the lock was released")
	}
}
