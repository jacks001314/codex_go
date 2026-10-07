package rollout

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"

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

// rolloutReadFailureReason mirrors Rust's bounded `reason` label (upstream
// #47565). Rust's `ReadFailureSource` distinguishes `reader_busy` and
// `task_join`, but both are tokio-only: Go's rollout reader is synchronous
// (`rollout/line_reader.go`), so there is no busy slot to contend for and no
// blocking task to join. Go therefore has **no counterpart for those two
// reasons** and never emits them; every failure is classified from the stream
// error itself:
//
//   - a real OS errno (Rust's `error.raw_os_error().is_some()`) -> "os_error"
//   - a zstd decoder error on a `.zst` reader -> the zstd_* families that
//     github.com/klauspost/compress actually reports (see
//     rolloutReadZstdReason)
//   - anything else -> "stream_error", Rust's fallback arm
func rolloutReadFailureReason(format string, err error) string {
	if err == nil {
		return rolloutReadReasonStreamError
	}
	if rolloutReadErrorHasOSErrno(err) {
		return rolloutReadReasonOSError
	}
	if format == rolloutReadFormatZstd {
		if reason := rolloutReadZstdReason(err); reason != "" {
			return reason
		}
	}
	return rolloutReadReasonStreamError
}

// rolloutReadErrorHasOSErrno mirrors Rust's `error.raw_os_error().is_some()`:
// Go wraps a failed syscall in `*os.PathError`/`*os.SyscallError` around a
// `syscall.Errno`, so `errors.As` is the equivalent probe.
func rolloutReadErrorHasOSErrno(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno != 0
	}
	return false
}

// rolloutReadZstdReason maps the zstd failure families that
// github.com/klauspost/compress/zstd @ v1.18.6 (the version pinned in go.mod)
// actually reports for a corrupt or truncated `.jsonl.zst` stream. Rust's own
// zstd crate returns different messages, so this table is derived from the Go
// decoder's **error identities**, never from Rust's strings:
//
//   - zstd.ErrMagicMismatch / ErrWindowSizeTooSmall / ErrReservedBlockType
//     -> "zstd_invalid_frame" (the frame header cannot be understood)
//   - zstd.ErrBlockTooSmall / ErrUnexpectedBlockSize / ErrCompressedSizeTooBig
//     -> "zstd_corrupt_block"
//   - zstd.ErrCRCMismatch -> "zstd_checksum"
//   - zstd.ErrDecoderSizeExceeded / ErrWindowSizeExceeded / ErrFrameSizeExceeded
//     / ErrFrameSizeMismatch -> "zstd_resource_limit"
//   - zstd.ErrUnknownDictionary -> "zstd_dictionary"
//
// Rust's `zstd_unsupported_frame` has **no Go counterpart**: this decoder has
// no "unsupported frame" error, so it is never emitted. A stream that simply
// ends early surfaces `io.ErrUnexpectedEOF` from the decoder and falls through
// to "stream_error" (Rust's own fallback arm), matching Rust's message table
// which also has no entry for an unexpected end of stream.
func rolloutReadZstdReason(err error) string {
	switch {
	case errors.Is(err, zstd.ErrMagicMismatch),
		errors.Is(err, zstd.ErrWindowSizeTooSmall),
		errors.Is(err, zstd.ErrReservedBlockType):
		return rolloutReadReasonZstdInvalidFrame
	case errors.Is(err, zstd.ErrBlockTooSmall),
		errors.Is(err, zstd.ErrUnexpectedBlockSize),
		errors.Is(err, zstd.ErrCompressedSizeTooBig):
		return rolloutReadReasonZstdCorruptBlock
	case errors.Is(err, zstd.ErrCRCMismatch):
		return rolloutReadReasonZstdChecksum
	case errors.Is(err, zstd.ErrDecoderSizeExceeded),
		errors.Is(err, zstd.ErrWindowSizeExceeded),
		errors.Is(err, zstd.ErrFrameSizeExceeded),
		errors.Is(err, zstd.ErrFrameSizeMismatch):
		return rolloutReadReasonZstdResourceLimit
	case errors.Is(err, zstd.ErrUnknownDictionary):
		return rolloutReadReasonZstdDictionary
	default:
		return ""
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
