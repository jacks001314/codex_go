package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	worldWritableAuditSampleLimit = 20
	maxAuditItemsPerDir           = 1000
	maxAuditCheckedLimit          = 50000
)

var auditSkipDirSuffixes = []string{
	"/windows/installer",
	"/windows/registration",
	"/programdata",
}

type WorldWritableAuditRequest struct {
	CodexHome   string
	CWD         string
	Env         map[string]string
	Permissions *ResolvedWindowsSandboxPermissions
	LogsBaseDir string
}

type WorldWritableAuditResult struct {
	SamplePaths []string
	ExtraCount  int
	FailedScan  bool
	// FlaggedCount is the total number of directories flagged by the scan
	// (Rust apply_world_writable_scan_and_denies_for_permissions -> usize,
	// #40983). It feeds the world-writable-scan telemetry histogram.
	FlaggedCount int
}

func worldWritableAuditResult(paths []string, failedScan bool) *WorldWritableAuditResult {
	result := &WorldWritableAuditResult{FailedScan: failedScan, FlaggedCount: len(paths)}
	if len(paths) <= worldWritableAuditSampleLimit {
		result.SamplePaths = cloneStrings(paths)
		return result
	}
	result.SamplePaths = cloneStrings(paths[:worldWritableAuditSampleLimit])
	result.ExtraCount = len(paths) - worldWritableAuditSampleLimit
	return result
}

func gatherAuditCandidates(cwd string, env map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	uniquePushCanonicalPath(seen, &out, cwd)
	for _, key := range []string{"TEMP", "TMP"} {
		value := strings.TrimSpace(env[key])
		if value == "" {
			value = strings.TrimSpace(os.Getenv(key))
		}
		uniquePushCanonicalPath(seen, &out, value)
	}
	uniquePushCanonicalPath(seen, &out, os.Getenv("USERPROFILE"))
	uniquePushCanonicalPath(seen, &out, os.Getenv("PUBLIC"))
	pathValue := env["PATH"]
	if strings.TrimSpace(pathValue) == "" {
		pathValue = os.Getenv("PATH")
	}
	for _, part := range filepath.SplitList(pathValue) {
		uniquePushCanonicalPath(seen, &out, part)
	}
	uniquePushCanonicalPath(seen, &out, `C:\`)
	uniquePushCanonicalPath(seen, &out, `C:\Windows`)
	return out
}

func uniquePushCanonicalPath(seen map[string]bool, out *[]string, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	canonical, err := CanonicalizePath(path)
	if err != nil {
		return
	}
	if _, err := os.Stat(canonical); err != nil {
		return
	}
	key := CanonicalPathKey(canonical)
	if seen[key] {
		return
	}
	seen[key] = true
	*out = append(*out, canonical)
}

func shouldSkipAuditDir(path string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	for _, suffix := range auditSkipDirSuffixes {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
