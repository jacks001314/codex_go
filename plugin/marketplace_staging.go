package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// Marketplace staging mirrors Rust #55689's `marketplace_staging` module. An
// active checkout holds an owner lock for its whole lifetime, so a cleanup pass
// in another process (or the same process) never deletes a directory a Git
// child is still writing. Staging directories are named per operating system
// because Windows and WSL can share a home without sharing lock namespaces.
const (
	marketplaceStagingDirName       = ".staging"
	marketplaceCoordinationLockName = ".staging.lock"
	marketplaceCleanupLockName      = ".staging-cleanup.lock"
	marketplaceOwnerLockName        = ".lock"
	marketplaceCheckoutDirName      = "checkout"
	maxMarketplaceStagingCleanup    = 4
	legacyMarketplaceAddMaxAge      = 24 * time.Hour
	legacyMarketplaceUpgradeMaxAge  = 10 * time.Minute
	legacyMarketplaceAddPrefix      = "marketplace-add-"
	legacyMarketplaceUpgradePrefix  = "marketplace-upgrade-"
)

// MarketplaceStagingDir owns one temporary marketplace checkout. Close releases
// the owner lock before removing the directory, which Windows requires.
type MarketplaceStagingDir struct {
	lock     *flock.Flock
	dir      string
	checkout string
}

// newMarketplaceStagingDir creates a staging directory below installRoot and
// holds its owner lock until Close. The checkout path is where the marketplace
// source is cloned; it does not exist until the clone creates it.
func newMarketplaceStagingDir(installRoot string) (*MarketplaceStagingDir, error) {
	stagingRoot := filepath.Join(installRoot, marketplaceStagingDirName)
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return nil, err
	}
	// The coordination lock covers publication of the owner lock, so a cleanup
	// pass racing with creation sees either no directory or a locked one.
	coordination := flock.New(filepath.Join(installRoot, marketplaceCoordinationLockName))
	if err := coordination.Lock(); err != nil {
		_ = coordination.Close()
		return nil, err
	}
	defer func() {
		_ = coordination.Unlock()
		_ = coordination.Close()
	}()
	dir, err := os.MkdirTemp(stagingRoot, marketplaceStagingDirPrefix())
	if err != nil {
		return nil, err
	}
	owner := flock.New(filepath.Join(dir, marketplaceOwnerLockName))
	if err := owner.Lock(); err != nil {
		_ = owner.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &MarketplaceStagingDir{
		lock:     owner,
		dir:      dir,
		checkout: filepath.Join(dir, marketplaceCheckoutDirName),
	}, nil
}

// Path is the directory the marketplace is cloned into.
func (d *MarketplaceStagingDir) Path() string {
	if d == nil {
		return ""
	}
	return d.checkout
}

// Close releases the owner lock and removes the staging directory. A checkout
// that was renamed to its destination leaves only the lock file behind.
func (d *MarketplaceStagingDir) Close() {
	if d == nil {
		return
	}
	if d.lock != nil {
		_ = d.lock.Unlock()
		_ = d.lock.Close()
		d.lock = nil
	}
	_ = os.RemoveAll(d.dir)
}

// marketplaceStagingDirPrefix keeps each platform in its own namespace.
func marketplaceStagingDirPrefix() string {
	return "marketplace-" + runtime.GOOS + "-"
}

// cleanupMarketplaceStaging reclaims abandoned marketplace checkouts below the
// install root. Cleanup is best effort so a leaked directory cannot block an
// install. It first reclaims staging directories left by older binaries, which
// published no owner lock, then lock-published directories from the current
// layout.
func cleanupMarketplaceStaging(installRoot string, now time.Time) {
	stagingRoot := filepath.Join(installRoot, marketplaceStagingDirName)
	cleanupLegacyMarketplaceStaging(stagingRoot, now)
	removeAbandonedMarketplaceStaging(installRoot, stagingRoot)
}

// cleanupLegacyMarketplaceStaging keeps the age-based reclamation for staging
// directories written by binaries that predate owner locks (Rust #55690).
func cleanupLegacyMarketplaceStaging(stagingRoot string, now time.Time) {
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return
	}
	abandoned := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !isAgedLegacyMarketplaceStagingEntry(entry, now) {
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

func isAgedLegacyMarketplaceStagingEntry(entry os.DirEntry, now time.Time) bool {
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

// removeAbandonedMarketplaceStaging claims a bounded batch of lock-published
// directories under the cleanup and coordination locks, then deletes the batch
// after dropping the coordination lock so new checkouts can start.
func removeAbandonedMarketplaceStaging(installRoot string, stagingRoot string) {
	info, err := os.Lstat(stagingRoot)
	if err != nil || !info.IsDir() {
		return
	}
	cleanup := flock.New(filepath.Join(installRoot, marketplaceCleanupLockName))
	locked, err := cleanup.TryLock()
	if err != nil || !locked {
		_ = cleanup.Close()
		return
	}
	defer func() {
		_ = cleanup.Unlock()
		_ = cleanup.Close()
	}()
	coordination := flock.New(filepath.Join(installRoot, marketplaceCoordinationLockName))
	locked, err = coordination.TryLock()
	if err != nil || !locked {
		_ = coordination.Close()
		return
	}
	prefix := marketplaceStagingDirPrefix()
	entries, err := os.ReadDir(stagingRoot)
	abandoned := make([]string, 0, maxMarketplaceStagingCleanup)
	if err == nil {
		for _, entry := range entries {
			if len(abandoned) == maxMarketplaceStagingCleanup {
				break
			}
			if isAbandonedLockedMarketplaceStagingEntry(stagingRoot, entry, prefix) {
				abandoned = append(abandoned, filepath.Join(stagingRoot, entry.Name()))
			}
		}
	}
	// Unique checkout names cannot acquire a new owner after publication, so
	// deletion may run without blocking new checkouts.
	_ = coordination.Unlock()
	_ = coordination.Close()
	for _, path := range abandoned {
		_ = os.RemoveAll(path)
	}
}

// isAbandonedLockedMarketplaceStagingEntry reports whether a directory has no
// live owner. A missing owner lock means the process crashed before publishing
// it; an acquirable lock means no process is using the checkout anymore.
func isAbandonedLockedMarketplaceStagingEntry(stagingRoot string, entry os.DirEntry, prefix string) bool {
	// DirectoryEntry.IsDir follows no symlink, so a symlink is never reclaimed.
	if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
		return false
	}
	ownerLockPath := filepath.Join(stagingRoot, entry.Name(), marketplaceOwnerLockName)
	info, err := os.Lstat(ownerLockPath)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	probe := flock.New(ownerLockPath)
	locked, err := probe.TryLock()
	if err != nil {
		_ = probe.Close()
		return false
	}
	if !locked {
		_ = probe.Close()
		return false
	}
	_ = probe.Unlock()
	_ = probe.Close()
	return true
}
