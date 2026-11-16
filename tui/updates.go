package tui

import (
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/updates.rs.

const (
	HomebrewCaskAPIURL = "https://formulae.brew.sh/api/cask/codex.json"
	LatestReleaseURL   = "https://api.github.com/repos/jacks001314/codex_go/releases/latest"
	UpdateCacheTTL     = 20 * time.Hour
)

type UpdateCheckConfig struct {
	CheckForUpdateOnStartup bool
	CurrentVersion          string
	CodexHome               string
	Now                     time.Time
}

func UpdateAvailable(versions UpdateVersions) bool {
	if IsSourceBuildVersion(versions.Current) {
		return false
	}
	newer := IsNewerVersion(versions.Latest, versions.Current)
	return newer != nil && *newer
}

func VersionInfoNeedsRefresh(info *VersionInfo, now time.Time) bool {
	if info == nil {
		return true
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return info.LastCheckedAt.Before(now.Add(-UpdateCacheTTL))
}

// VersionInfoNeedsVersionRefresh reports whether the cache was last refreshed by
// a different CLI version. A binary installed after that check cannot have seen
// the releases published since, so the startup flow re-checks once instead of
// waiting out UpdateCacheTTL. An empty current version, as in tests or unknown
// builds, never triggers the re-check.
func VersionInfoNeedsVersionRefresh(info *VersionInfo, currentVersion string) bool {
	currentVersion = strings.TrimSpace(currentVersion)
	if info == nil || currentVersion == "" {
		return false
	}
	return strings.TrimSpace(info.CheckedByVersion) != currentVersion
}

// VersionInfoBehindCurrent reports whether the cached latest version trails the
// running CLI, which means the cache was filled before this build replaced the
// previous one. A fresh cache that already reports a newer release than this
// build stays authoritative; only a cache that cannot know about this build's
// release line is re-checked.
func VersionInfoBehindCurrent(info *VersionInfo, currentVersion string) bool {
	if info == nil {
		return false
	}
	behind := IsNewerVersion(currentVersion, info.LatestVersion)
	return behind != nil && *behind
}

func GetUpgradeVersionFromInfo(info *VersionInfo, currentVersion string) (string, bool) {
	if info == nil || !UpdateAvailable(UpdateVersions{Current: currentVersion, Latest: info.LatestVersion}) {
		return "", false
	}
	return strings.TrimSpace(info.LatestVersion), true
}

func GetUpgradeVersionForPopupFromInfo(info *VersionInfo, currentVersion string) (string, bool) {
	latest, ok := GetUpgradeVersionFromInfo(info, currentVersion)
	if !ok {
		return "", false
	}
	if info.DismissedVersion != nil && strings.TrimSpace(*info.DismissedVersion) == latest {
		return "", false
	}
	return latest, true
}

func ShouldCheckForUpgrade(config UpdateCheckConfig, cached *VersionInfo) bool {
	if !config.CheckForUpdateOnStartup || IsSourceBuildVersion(config.CurrentVersion) {
		return false
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return VersionInfoNeedsRefresh(cached, now)
}

func GetUpgradeVersion(config UpdateCheckConfig) (string, bool, error) {
	if !config.CheckForUpdateOnStartup || IsSourceBuildVersion(config.CurrentVersion) {
		return "", false, nil
	}
	info, err := ReadVersionInfo(VersionFilePath(config.CodexHome))
	if err != nil {
		return "", false, err
	}
	latest, ok := GetUpgradeVersionFromInfo(&info, config.CurrentVersion)
	return latest, ok, nil
}

func GetUpgradeVersionForPopup(config UpdateCheckConfig) (string, bool, error) {
	if !config.CheckForUpdateOnStartup || IsSourceBuildVersion(config.CurrentVersion) {
		return "", false, nil
	}
	info, err := ReadVersionInfo(VersionFilePath(config.CodexHome))
	if err != nil {
		return "", false, err
	}
	latest, ok := GetUpgradeVersionForPopupFromInfo(&info, config.CurrentVersion)
	return latest, ok, nil
}
