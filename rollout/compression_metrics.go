package rollout

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
	"time"

	"codex_go/metrics"
)

// Metric names mirror Rust's rollout `compression::metrics` module.
const (
	rolloutCompressionFileCounter               = "codex.rollout_compression.file"
	rolloutCompressionRunCounter                = "codex.rollout_compression.run"
	rolloutCompressionScanCounter               = "codex.rollout_compression.scan"
	rolloutCompressionMaterializeCounter        = "codex.rollout_compression.materialize"
	rolloutCompressionTempCleanupCounter        = "codex.rollout_compression.temp_cleanup"
	rolloutCompressionFileDurationMetric        = "codex.rollout_compression.file.duration_ms"
	rolloutCompressionFileSourceBytes           = "codex.rollout_compression.file.source_bytes"
	rolloutCompressionFileCompressedBytes       = "codex.rollout_compression.file.compressed_bytes"
	rolloutCompressionFileRatioMetric           = "codex.rollout_compression.file.compression_ratio"
	rolloutCompressionRunDurationMetric         = "codex.rollout_compression.run.duration_ms"
	rolloutCompressionMaterializeDurationMetric = "codex.rollout_compression.materialize.duration_ms"
	// rolloutCompressionRatioBasisPoints keeps the ratio histogram integer-valued
	// while preserving sub-percent precision (Rust RATIO_BASIS_POINTS).
	rolloutCompressionRatioBasisPoints = 10_000
)

func rolloutCompressionFile(trigger RolloutCompressionTrigger, outcome string) {
	metrics.Counter(rolloutCompressionFileCounter, 1, map[string]string{
		"outcome": outcome,
		"trigger": trigger.tag(),
	})
}

// rolloutCompressionBoolTag renders Rust's "true"/"false" completion tags.
func rolloutCompressionBoolTag(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func rolloutCompressionFileDuration(trigger RolloutCompressionTrigger, outcome string, duration time.Duration) {
	metrics.RecordDuration(rolloutCompressionFileDurationMetric, duration, map[string]string{
		"outcome": outcome,
		"trigger": trigger.tag(),
	})
}

func rolloutCompressionBytes(name string, trigger RolloutCompressionTrigger, outcome string, bytes int64) {
	metrics.Histogram(name, saturatingInt(bytes), map[string]string{
		"outcome": outcome,
		"trigger": trigger.tag(),
	})
}

func rolloutCompressionSourceBytes(trigger RolloutCompressionTrigger, outcome string, bytes int64) {
	rolloutCompressionBytes(rolloutCompressionFileSourceBytes, trigger, outcome, bytes)
}

func rolloutCompressionCompressedBytes(trigger RolloutCompressionTrigger, outcome string, bytes int64) {
	rolloutCompressionBytes(rolloutCompressionFileCompressedBytes, trigger, outcome, bytes)
}

// rolloutCompressionRatio mirrors Rust's `compression_ratio`: the ratio is
// reported in basis points and skipped when the source is empty.
func rolloutCompressionRatio(trigger RolloutCompressionTrigger, outcome string, sourceBytes int64, compressedBytes int64) {
	if sourceBytes <= 0 {
		return
	}
	ratio := compressedBytes * rolloutCompressionRatioBasisPoints / sourceBytes
	metrics.Histogram(rolloutCompressionFileRatioMetric, saturatingInt(ratio), map[string]string{
		"outcome": outcome,
		"trigger": trigger.tag(),
	})
}

func rolloutCompressionMaterialize(outcome string) {
	metrics.Counter(rolloutCompressionMaterializeCounter, 1, map[string]string{"outcome": outcome})
}

func rolloutCompressionMaterializeDuration(outcome string, duration time.Duration) {
	metrics.RecordDuration(rolloutCompressionMaterializeDurationMetric, duration, map[string]string{"outcome": outcome})
}

func rolloutCompressionRun(trigger RolloutCompressionTrigger, status string) {
	metrics.Counter(rolloutCompressionRunCounter, 1, map[string]string{
		"status":  status,
		"trigger": trigger.tag(),
	})
}

func rolloutCompressionRunTags(trigger RolloutCompressionTrigger, status string, extra map[string]string) map[string]string {
	tags := map[string]string{"status": status, "trigger": trigger.tag()}
	for key, value := range extra {
		tags[key] = value
	}
	return tags
}

func rolloutCompressionRunDurationTags(tags map[string]string, duration time.Duration) {
	metrics.RecordDuration(rolloutCompressionRunDurationMetric, duration, tags)
}

func rolloutCompressionTempCleanup(trigger RolloutCompressionTrigger, outcome string) {
	metrics.Counter(rolloutCompressionTempCleanupCounter, 1, map[string]string{
		"outcome": outcome,
		"trigger": trigger.tag(),
	})
}

// rolloutCompressionFailure mirrors Rust's `error_metrics::FailureMetric`: the
// failure counters carry a static stage label and a bounded error kind, and the
// trigger tag only for the trigger-scoped families.
func rolloutCompressionFailure(counter string, outcomeKey string, trigger *RolloutCompressionTrigger, stage string, err error) {
	tags := map[string]string{
		outcomeKey:   "failed",
		"stage":      stage,
		"error_kind": rolloutCompressionErrorKind(err),
	}
	if trigger != nil {
		tags["trigger"] = trigger.tag()
	}
	metrics.Counter(counter, 1, tags)
}

// rolloutCompressionErrorKind mirrors Rust's `error_kind`: a fixed set of
// categories so error messages and paths never become metric tags.
func rolloutCompressionErrorKind(err error) string {
	switch {
	case err == nil:
		return "other"
	// The errno-specific checks come first: Go maps some Windows errnos (for
	// example ENOTDIR) onto the generic os.Err sentinels, so testing the
	// sentinels first would collapse distinct Rust categories.
	case errors.Is(err, syscall.ENOTDIR):
		return "not_a_directory"
	case errors.Is(err, syscall.EISDIR):
		return "is_a_directory"
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return "storage_full"
	case errors.Is(err, syscall.EROFS):
		return "read_only_filesystem"
	case errors.Is(err, fs.ErrNotExist):
		return "not_found"
	case errors.Is(err, fs.ErrPermission):
		return "permission_denied"
	case errors.Is(err, fs.ErrExist):
		return "already_exists"
	case errors.Is(err, fs.ErrInvalid):
		return "invalid_input"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timed_out"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, io.ErrShortWrite):
		return "write_zero"
	case errors.Is(err, errors.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, context.Canceled), errors.Is(err, syscall.EINTR):
		return "interrupted"
	default:
		return "other"
	}
}

func saturatingInt(value int64) int {
	if value > int64(int(^uint(0)>>1)) {
		return int(^uint(0) >> 1)
	}
	if value < -int64(int(^uint(0)>>1))-1 {
		return -int(int(^uint(0)>>1)) - 1
	}
	return int(value)
}
