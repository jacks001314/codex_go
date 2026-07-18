package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// Rust parity: codex-rs/tui/src/update_versions.rs.

type UpdateVersions struct {
	Current string
	Latest  string
}

func IsNewerVersion(latest string, current string) *bool {
	latestVersion, latestOK := parsePlainVersion(latest)
	currentVersion, currentOK := parsePlainVersion(current)
	if !latestOK || !currentOK {
		return nil
	}
	value := compareVersionTriplet(latestVersion, currentVersion) > 0
	return &value
}

func ExtractVersionFromLatestTag(latestTagName string) (string, error) {
	version, ok := strings.CutPrefix(strings.TrimSpace(latestTagName), "rust-v")
	if !ok || strings.TrimSpace(version) == "" {
		return "", fmt.Errorf("failed to parse latest tag name %q", latestTagName)
	}
	return strings.TrimSpace(version), nil
}

func IsSourceBuildVersion(version string) bool {
	parsed, ok := parsePlainVersion(version)
	return ok && parsed == [3]uint64{}
}

func parsePlainVersion(value string) ([3]uint64, bool) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 {
		return [3]uint64{}, false
	}
	var parsed [3]uint64
	for i, part := range parts {
		if part == "" || strings.ContainsAny(part, "-+ ") {
			return [3]uint64{}, false
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return [3]uint64{}, false
		}
		parsed[i] = n
	}
	return parsed, true
}

func compareVersionTriplet(a [3]uint64, b [3]uint64) int {
	for i := 0; i < 3; i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}
