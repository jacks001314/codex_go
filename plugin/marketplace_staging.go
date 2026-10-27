package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Legacy marketplace staging thresholds mirror Rust #55690. Older binaries
// publish no owner lock, so their checkouts are reclaimed by age instead.
const (
	legacyMarketplaceAddMaxAge = 24 * time.Hour
	// Upgrades allow 30 seconds per Git command; leave ample room for every
	// phase before treating a directory as abandoned.
	legacyMarketplaceUpgradeMaxAge = 10 * time.Minute
	legacyMarketplaceAddPrefix     = "marketplace-add-"
	legacyMarketplaceUpgradePrefix = "marketplace-upgrade-"
	maxMarketplaceStagingCleanup   = 4
)

// cleanupMarketplaceStaging reclaims abandoned marketplace checkouts below the
// staging root. Cleanup is best effort so a leaked directory cannot block an
// install.
func cleanupMarketplaceStaging(stagingRoot string, now time.Time) {
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return
	}
	abandoned := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !isAbandonedMarketplaceStagingEntry(entry, now) {
			continue
		}
		abandoned = append(abandoned, filepath.Join(stagingRoot, entry.Name()))
		if len(abandoned) == maxMarketplaceStagingCleanup {
			break
		}
	}
	for _, path := range abandoned {
		_ = os.RemoveAll(path)
	}
}

func isAbandonedMarketplaceStagingEntry(entry os.DirEntry, now time.Time) bool {
	// Symlinks and regular files are never reclaimed.
	if !entry.IsDir() {
		return false
	}
	var maxAge time.Duration
	switch {
	case strings.HasPrefix(entry.Name(), legacyMarketplaceAddPrefix):
		maxAge = legacyMarketplaceAddMaxAge
	case strings.HasPrefix(entry.Name(), legacyMarketplaceUpgradePrefix):
		maxAge = legacyMarketplaceUpgradeMaxAge
	default:
		return false
	}
	info, err := entry.Info()
	if err != nil {
		return false
	}
	age := now.Sub(info.ModTime())
	if age < 0 {
		age = 0
	}
	return age >= maxAge
}
