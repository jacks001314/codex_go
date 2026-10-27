package plugin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupMarketplaceStagingReclaimsAgedLegacyDirectories(t *testing.T) {
	stagingRoot := filepath.Join(t.TempDir(), ".staging")
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
		cleanupMarketplaceStaging(stagingRoot, now.Add(tc.age))
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
	stagingRoot := filepath.Join(t.TempDir(), ".staging")
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
	cleanupMarketplaceStaging(stagingRoot, info.ModTime().Add(legacyMarketplaceAddMaxAge+time.Second))
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		t.Fatalf("read staging root: %v", err)
	}
	if len(entries) != count-maxMarketplaceStagingCleanup {
		t.Fatalf("staging entries after cleanup = %d, want %d", len(entries), count-maxMarketplaceStagingCleanup)
	}
}

func TestCleanupMarketplaceStagingSkipsSymlinks(t *testing.T) {
	stagingRoot := filepath.Join(t.TempDir(), ".staging")
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
	cleanupMarketplaceStaging(stagingRoot, time.Now().Add(legacyMarketplaceAddMaxAge+time.Second))
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
