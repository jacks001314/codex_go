package rollout

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"codex_go/metrics"
)

// Rust's open_rollout_line_reader retry budget: if the requested path
// disappears during a representation transition the resolution is retried so
// callers do not need to know which representation is on disk.
const (
	rolloutLineReaderMaxNotFoundRetries = 3
	rolloutLineReaderRetryDelay         = 50 * time.Millisecond
)

// RolloutReadMetric names mirror Rust's `compression::read_metrics` module.
const (
	rolloutReadCounter        = "codex.rollout_compression.read"
	rolloutReadDurationMetric = "codex.rollout_compression.read.io_duration_ms"
	rolloutReadFormatUnknown  = "unknown"
	rolloutReadFormatPlain    = "plain"
	rolloutReadFormatZstd     = "zstd"
	rolloutReadOutcomeEOF     = "eof"
	rolloutReadOutcomePartial = "partial"
	rolloutReadOutcomeFailed  = "failed"
	rolloutReadStageNone      = "none"
	rolloutReadErrorKindNone  = "none"
	// Rust #47565 adds the bounded `reason` and `read_progress` labels beside
	// the stage and error kind. `none` is Rust's placeholder for the eof and
	// partial outcomes.
	rolloutReadReasonNone        = "none"
	rolloutReadReasonStreamError = "stream_error"
	rolloutReadReasonOSError     = "os_error"
	// Go-faithful zstd reason families (see rolloutReadZstdReason in
	// compression_metrics.go). Rust's `zstd_unsupported_frame` has no
	// counterpart in github.com/klauspost/compress and is never emitted.
	rolloutReadReasonZstdInvalidFrame  = "zstd_invalid_frame"
	rolloutReadReasonZstdCorruptBlock  = "zstd_corrupt_block"
	rolloutReadReasonZstdChecksum      = "zstd_checksum"
	rolloutReadReasonZstdResourceLimit = "zstd_resource_limit"
	rolloutReadReasonZstdDictionary    = "zstd_dictionary"
	rolloutReadProgressNone            = "none"
	rolloutReadProgressBeforeFirstLine = "before_first_line"
	rolloutReadProgressAfterFirstLine  = "after_first_line"
)

// rolloutReadFailure mirrors Rust's `ReadFailure` snapshot: the bounded
// `stage`/`error_kind` labels plus the #47565 `reason` and `read_progress`
// labels, all captured at the first failure.
type rolloutReadFailure struct {
	stage    string
	kind     string
	reason   string
	progress string
}

// RolloutLineReader mirrors Rust's `RolloutLineReader`: a line-oriented reader
// that transparently handles plain `.jsonl` and `.jsonl.zst` rollouts. One
// `codex.rollout_compression.read` observation is recorded per reader when it is
// released, including failed opens and partial reads.
type RolloutLineReader struct {
	reader  *bufio.Reader
	closers []func() error

	format     string
	reachedEOF bool
	// readAnyLine mirrors Rust's `ReadMetrics::read_any_line`: it records
	// whether a completed line was returned before the first failure, which
	// becomes the #47565 `read_progress` label.
	readAnyLine bool
	duration    time.Duration
	failure     *rolloutReadFailure

	closeOnce sync.Once
}

// OpenRolloutLineReader resolves the rollout's current representation and opens
// a line reader, retrying a not-found resolution within Rust's retry budget. The
// caller must Close the reader; the read metric is recorded on Close.
func OpenRolloutLineReader(path string) (*RolloutLineReader, error) {
	startedAt := time.Now()
	var (
		reader *RolloutLineReader
		err    error
	)
	for attempt := 0; attempt < rolloutLineReaderMaxNotFoundRetries; attempt++ {
		reader, err = openRolloutLineReaderOnce(path)
		if err == nil {
			reader.duration = time.Since(startedAt)
			return reader, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(rolloutLineReaderRetryDelay)
	}
	if err == nil {
		err = os.ErrNotExist
	}
	// Rust records the failed open through the metrics object's drop, with the
	// bounded error kind and the "open" stage.
	// Rust classifies the failed open as `ReadFailureSource::Stream` (the
	// `TaskJoin` arm only exists for the tokio blocking hand-off, which Go has
	// no counterpart for). The representation is not resolved yet, so the
	// format stays `unknown` and the error is classified as a stream/OS
	// failure, never as a zstd frame problem.
	failed := &RolloutLineReader{
		format:   rolloutReadFormatUnknown,
		duration: time.Since(startedAt),
		failure: &rolloutReadFailure{
			stage:    "open",
			kind:     rolloutCompressionErrorKind(err),
			reason:   rolloutReadFailureReason(rolloutReadFormatUnknown, err),
			progress: rolloutReadProgressBeforeFirstLine,
		},
	}
	failed.Close()
	return nil, err
}

func openRolloutLineReaderOnce(path string) (*RolloutLineReader, error) {
	resolved := strings.TrimSpace(path)
	if existing, ok := ExistingRolloutPath(path); ok {
		resolved = existing
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(resolved), ".zst") {
		decoder, decodeErr := zstd.NewReader(file)
		if decodeErr != nil {
			_ = file.Close()
			return nil, decodeErr
		}
		return &RolloutLineReader{
			reader: bufio.NewReader(decoder),
			closers: []func() error{
				func() error { decoder.Close(); return nil },
				file.Close,
			},
			format: rolloutReadFormatZstd,
		}, nil
	}
	return &RolloutLineReader{
		reader:  bufio.NewReader(file),
		closers: []func() error{file.Close},
		format:  rolloutReadFormatPlain,
	}, nil
}

// ReadLine returns the next raw line (newline included). It reports io.EOF once
// the reader reaches the end of the rollout.
func (r *RolloutLineReader) ReadLine() ([]byte, error) {
	if r == nil || r.reader == nil {
		return nil, io.EOF
	}
	startedAt := time.Now()
	raw, err := r.reader.ReadBytes('\n')
	r.duration += time.Since(startedAt)
	if err != nil {
		if errors.Is(err, io.EOF) {
			r.reachedEOF = true
			if len(raw) == 0 {
				return nil, io.EOF
			}
			// A final line without a trailing newline is still a completed line
			// (Rust sees `Ok(Some(line))` before the following `Ok(None)`).
			r.readAnyLine = true
			return raw, nil
		}
		// Partial data delivered together with an error is not a completed line:
		// Rust's reader surfaces the error and keeps `read_any_line` as-is.
		r.fail("read", err)
		if len(raw) == 0 {
			return nil, err
		}
		return raw, nil
	}
	r.readAnyLine = true
	return raw, nil
}

// fail records the first failure, mirroring Rust's `ReadMetrics::failed`
// (`get_or_insert`). Go's reader is synchronous, so the `ReadFailureSource`
// only ever carries the stream error; the tokio-only `reader_busy` and
// `task_join` reasons have no Go counterpart and are never emitted.
func (r *RolloutLineReader) fail(stage string, err error) {
	if r.failure != nil {
		return
	}
	r.failure = &rolloutReadFailure{
		stage:    stage,
		kind:     rolloutCompressionErrorKind(err),
		reason:   rolloutReadFailureReason(r.format, err),
		progress: rolloutReadProgress(r.readAnyLine),
	}
}

// rolloutReadProgress mirrors Rust's `read_progress` label.
func rolloutReadProgress(readAnyLine bool) string {
	if readAnyLine {
		return rolloutReadProgressAfterFirstLine
	}
	return rolloutReadProgressBeforeFirstLine
}

// Close releases the reader and records its read metric once.
func (r *RolloutLineReader) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	r.closeOnce.Do(func() {
		for _, closer := range r.closers {
			if closer == nil {
				continue
			}
			if err := closer(); firstErr == nil && err != nil {
				firstErr = err
			}
		}
		r.closers = nil
		r.recordReadMetric()
	})
	return firstErr
}

// recordReadMetric mirrors Rust's `Drop for ReadMetrics`: a failed read is
// reported with its stage and error kind, a reader that reached EOF as `eof`,
// and an abandoned reader as `partial`.
func (r *RolloutLineReader) recordReadMetric() {
	format := strings.TrimSpace(r.format)
	if format == "" {
		format = rolloutReadFormatUnknown
	}
	outcome := rolloutReadOutcomePartial
	stage := rolloutReadStageNone
	errorKind := rolloutReadErrorKindNone
	reason := rolloutReadReasonNone
	readProgress := rolloutReadProgressNone
	switch {
	case r.failure != nil:
		outcome = rolloutReadOutcomeFailed
		stage = r.failure.stage
		errorKind = r.failure.kind
		reason = r.failure.reason
		readProgress = r.failure.progress
	case r.reachedEOF:
		outcome = rolloutReadOutcomeEOF
	}
	tags := map[string]string{
		"format":        format,
		"outcome":       outcome,
		"stage":         stage,
		"error_kind":    errorKind,
		"reason":        reason,
		"read_progress": readProgress,
	}
	metrics.Counter(rolloutReadCounter, 1, tags)
	metrics.RecordDuration(rolloutReadDurationMetric, r.duration, tags)
}
