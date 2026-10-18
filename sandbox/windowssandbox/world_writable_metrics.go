package windowssandbox

import "codex_go/metrics"

const worldWritableScanFlaggedDirectoriesMetric = "codex.windows_sandbox.world_writable_scan_flagged_directories"

// recordWorldWritableScanResult mirrors Rust
// record_world_writable_scan_result (#40983): successful scans report the
// number of flagged directories, while failed scans report zero with an error
// result tag.
func recordWorldWritableScanResult(result *WorldWritableAuditResult, scanErr error) {
	flaggedCount := 0
	resultTag := "success"
	if scanErr != nil {
		resultTag = "error"
	} else if result != nil {
		flaggedCount = result.FlaggedCount
	}
	metrics.Histogram(worldWritableScanFlaggedDirectoriesMetric, flaggedCount, map[string]string{
		"result": resultTag,
	})
}
