package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/updates_cache.rs.

const VersionFilename = "version.json"

type UpdatesCacheEntry struct {
	Version string
	Seen    bool
}

type VersionInfo struct {
	LatestVersion    string    `json:"latest_version"`
	LastCheckedAt    time.Time `json:"last_checked_at"`
	DismissedVersion *string   `json:"dismissed_version,omitempty"`
}

func VersionFilePath(codexHome string) string {
	return filepath.Join(strings.TrimSpace(codexHome), VersionFilename)
}

func ReadVersionInfo(versionFile string) (VersionInfo, error) {
	contents, err := os.ReadFile(versionFile)
	if err != nil {
		return VersionInfo{}, err
	}
	var info VersionInfo
	if err := json.Unmarshal(contents, &info); err != nil {
		return VersionInfo{}, err
	}
	return info, nil
}

func WriteVersionInfo(versionFile string, info VersionInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(versionFile), 0o700); err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(versionFile, data, 0o600)
}

func DismissVersion(codexHome string, version string) error {
	version = strings.TrimSpace(version)
	versionFile := VersionFilePath(codexHome)
	info, err := ReadVersionInfo(versionFile)
	if err != nil {
		info = VersionInfo{
			LatestVersion: version,
			LastCheckedAt: time.Unix(0, 0).UTC(),
		}
	}
	info.DismissedVersion = stringPtr(version)
	return WriteVersionInfo(versionFile, info)
}

func stringPtr(value string) *string {
	return &value
}
