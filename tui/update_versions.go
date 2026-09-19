package tui

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
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
	version, ok := strings.CutPrefix(strings.TrimSpace(latestTagName), "go-v")
	if !ok || strings.TrimSpace(version) == "" {
		return "", fmt.Errorf("failed to parse latest tag name %q", latestTagName)
	}
	return strings.TrimSpace(version), nil
}

func IsSourceBuildVersion(version string) bool {
	parsed, ok := parsePlainVersion(version)
	return ok && parsed == [3]uint64{}
}

// ServerVersionNoticeKind distinguishes a connected service that is behind the
// client from one running a different version line (Rust #46673).
type ServerVersionNoticeKind string

const (
	// ServerVersionNoticeOlder reports a released server that trails the client.
	ServerVersionNoticeOlder ServerVersionNoticeKind = "older"
	// ServerVersionNoticeDifferent reports a version mismatch: a local build, a
	// different release line, or a server carrying build metadata.
	ServerVersionNoticeDifferent ServerVersionNoticeKind = "different"
)

// ServerVersionNoticeKindFor mirrors Rust's `server_version_notice_kind`
// (#46673). A stable client compares release precedence against a released
// server (which may itself be a prerelease); a prerelease client only orders
// versions within its own release line and reports another line as different; a
// local client (`0.0.0` or build metadata) compares identity, so a differing
// build is reported instead of ordered. Malformed or unknown versions produce no
// notice.
func ServerVersionNoticeKindFor(client string, server string) (ServerVersionNoticeKind, bool) {
	clientVersion, ok := parseSemanticVersion(client)
	if !ok {
		return "", false
	}
	serverVersion, ok := parseSemanticVersion(server)
	if !ok {
		return "", false
	}
	clientIsLocal := clientVersion.release == [3]uint64{} || clientVersion.build != ""
	if clientIsLocal || (clientVersion.pre != "" && clientVersion.release != serverVersion.release) {
		if clientVersion.identity == serverVersion.identity {
			return "", false
		}
		return ServerVersionNoticeDifferent, true
	}
	if serverVersion.build == "" && serverVersion.release != [3]uint64{} &&
		compareSemanticPrecedence(clientVersion, serverVersion) > 0 {
		return ServerVersionNoticeOlder, true
	}
	return "", false
}

// parsedSemanticVersion is the subset of a semantic version the notice policy
// compares.
type parsedSemanticVersion struct {
	release [3]uint64
	pre     string
	build   string
	// identity is the full version text; semantic versioning admits no
	// alternative spellings, so text equality matches Rust's struct equality.
	identity string
}

// parseSemanticVersion validates a semantic version the way Rust's `semver` crate
// does and extracts its parts. x/mod/semver requires the `v` prefix that Rust
// rejects, so a leading `v` (and any surrounding whitespace) is refused before
// validation.
func parseSemanticVersion(value string) (parsedSemanticVersion, bool) {
	if value == "" || strings.HasPrefix(value, "v") {
		return parsedSemanticVersion{}, false
	}
	if !semver.IsValid("v" + value) {
		return parsedSemanticVersion{}, false
	}
	core := value
	build := ""
	if index := strings.IndexByte(core, '+'); index >= 0 {
		build = core[index+1:]
		core = core[:index]
	}
	number := core
	pre := ""
	if index := strings.IndexByte(core, '-'); index >= 0 {
		pre = core[index+1:]
		number = core[:index]
	}
	parts := strings.Split(number, ".")
	if len(parts) != 3 {
		return parsedSemanticVersion{}, false
	}
	var release [3]uint64
	for i, part := range parts {
		parsed, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return parsedSemanticVersion{}, false
		}
		release[i] = parsed
	}
	return parsedSemanticVersion{release: release, pre: pre, build: build, identity: value}, true
}

// compareSemanticPrecedence compares two parsed versions by semantic-version
// precedence; build metadata is not part of precedence.
func compareSemanticPrecedence(a parsedSemanticVersion, b parsedSemanticVersion) int {
	return semver.Compare("v"+precedenceVersionText(a), "v"+precedenceVersionText(b))
}

func precedenceVersionText(version parsedSemanticVersion) string {
	number := fmt.Sprintf("%d.%d.%d", version.release[0], version.release[1], version.release[2])
	if version.pre != "" {
		return number + "-" + version.pre
	}
	return number
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
