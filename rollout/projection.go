package rollout

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

type ProjectionStepKind string

const (
	ProjectionLine                ProjectionStepKind = "line"
	ProjectionSkippedOrdinalRange ProjectionStepKind = "skipped_ordinal_range"
)

// ProjectionStep is one ordered update to apply while advancing a durable
// rollout projection checkpoint.
type ProjectionStep struct {
	Kind                ProjectionStepKind
	Line                *Line
	Ordinal             uint64
	StartByteOffset     uint64
	EndByteOffset       uint64
	EndOrdinalExclusive uint64
}

// ReadProjectionSteps reads only the newline-terminated suffix beginning at
// startOffset. Rejected lines remain pending until a later valid ordinal proves
// whether they consumed history.
func ReadProjectionSteps(path string, startOffset uint64, expectedOrdinal uint64) ([]ProjectionStep, uint64, error) {
	return ReadProjectionStepsValidated(path, startOffset, expectedOrdinal, nil)
}

// ReadProjectionStepsValidated applies validate after a line is structurally
// decoded. A validation failure has the same deferred semantics as an unknown
// rollout record.
func ReadProjectionStepsValidated(path string, startOffset uint64, expectedOrdinal uint64, validate func(*Line) error) ([]ProjectionStep, uint64, error) {
	return ReadProjectionStepsCancellable(path, startOffset, expectedOrdinal, validate, nil)
}

// ReadProjectionStepsCancellable mirrors Rust #53947 and #54639: the caller's
// cancellation predicate is threaded through the rollout read the same way
// `open_rollout_seekable_reader(path, is_cancelled)` and
// `read_projection_steps_sync(..., || stop.is_closed())` do. It is consulted
// before the file is opened, during decompression and the bounded read, and
// between history records, and it reports `ErrRolloutReadCancelled` instead of
// a short or empty result. Records are streamed from a reader frozen at the
// durable end captured before the pass (`file.take(byte_count)`), so the unread
// suffix is never materialized (Rust #54639).
func ReadProjectionStepsCancellable(path string, startOffset uint64, expectedOrdinal uint64, validate func(*Line) error, isCancelled func() bool) ([]ProjectionStep, uint64, error) {
	suffix, err := openProjectionSuffix(path, startOffset, isCancelled)
	if err != nil {
		return nil, startOffset, err
	}
	defer suffix.close()
	return readProjectionStepsStream(suffix.limited, startOffset, expectedOrdinal, validate, isCancelled)
}

// projectionSuffix is the frozen, bounded view of a rollout's unread suffix:
// the durable end captured before the pass, plus a reader limited to exactly
// that many bytes and the open representation it reads from.
type projectionSuffix struct {
	// file is the open durable representation; nil when the reader reads an
	// in-memory snapshot.
	file    *os.File
	limited *io.LimitedReader
	fileEnd uint64
}

func (s projectionSuffix) close() {
	if s.file != nil {
		_ = s.file.Close()
	}
}

// openProjectionSuffix mirrors Rust `open_rollout_seekable_reader` followed by
// `seek(SeekFrom::Start(start_offset))` and `take(byte_count)`: offsets address
// the decoded JSONL bytes, a compressed rollout is decoded once into a snapshot
// so a concurrent compression cannot invalidate the pass, and an already
// cancelled caller fails before the rollout is opened or resolved.
func openProjectionSuffix(path string, startOffset uint64, isCancelled func() bool) (projectionSuffix, error) {
	// Rust #53947 checks the predicate before opening the rollout, so a
	// cancelled caller cannot fall through to `os.ErrNotExist` or an empty read.
	if err := checkRolloutReadCancelled(isCancelled); err != nil {
		return projectionSuffix{}, err
	}
	existing, found := ExistingRolloutPath(path)
	if !found {
		if startOffset == 0 {
			return projectionSuffix{limited: &io.LimitedReader{R: bytes.NewReader(nil)}}, nil
		}
		return projectionSuffix{}, os.ErrNotExist
	}
	file, err := os.Open(existing)
	if err != nil {
		return projectionSuffix{}, err
	}
	if strings.HasSuffix(strings.ToLower(existing), ".zst") {
		// Both the compressed input and the decoded output are cancellable, like
		// Rust's `CancellableReader` around the decoder and the decoded snapshot.
		decoder, decodeErr := zstd.NewReader(&cancellableRolloutReader{inner: file, isCancelled: isCancelled})
		if decodeErr != nil {
			_ = file.Close()
			return projectionSuffix{}, decodeErr
		}
		decompressed, readErr := io.ReadAll(&cancellableRolloutReader{inner: decoder, isCancelled: isCancelled})
		decoder.Close()
		if readErr != nil {
			_ = file.Close()
			return projectionSuffix{}, readErr
		}
		fileEnd := uint64(len(decompressed))
		if fileEnd < startOffset {
			_ = file.Close()
			return projectionSuffix{}, errors.New("durable rollout shrank before projection")
		}
		// The snapshot is exact, so its bound is reached by every complete pass.
		return projectionSuffix{
			file:    file,
			limited: &io.LimitedReader{R: bytes.NewReader(decompressed[startOffset:]), N: int64(fileEnd - startOffset)},
			fileEnd: fileEnd,
		}, nil
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return projectionSuffix{}, err
	}
	fileEnd := uint64(info.Size())
	if fileEnd < startOffset {
		_ = file.Close()
		return projectionSuffix{}, errors.New("durable rollout shrank before projection")
	}
	if _, err := file.Seek(int64(startOffset), io.SeekStart); err != nil {
		_ = file.Close()
		return projectionSuffix{}, err
	}
	return projectionSuffix{
		file:    file,
		limited: &io.LimitedReader{R: &cancellableRolloutReader{inner: file, isCancelled: isCancelled}, N: int64(fileEnd - startOffset)},
		fileEnd: fileEnd,
	}, nil
}

// readProjectionSuffix returns the whole unread suffix and the durable end
// captured before the read. The lineage scan reads a prefix in one piece; the
// materialization path streams the suffix record by record instead (#54639).
func readProjectionSuffix(path string, startOffset uint64, isCancelled func() bool) ([]byte, uint64, error) {
	suffix, err := openProjectionSuffix(path, startOffset, isCancelled)
	if err != nil {
		return nil, startOffset, err
	}
	defer suffix.close()
	data, err := io.ReadAll(suffix.limited)
	if err != nil {
		return nil, startOffset, err
	}
	return data, suffix.fileEnd, nil
}

// readProjectionStepsData projects records already held in memory (the lineage
// prefix scan) through the same streaming loop the file path uses, so both share
// one implementation.
func readProjectionStepsData(data []byte, startOffset uint64, expectedOrdinal uint64, validate func(*Line) error, isCancelled func() bool) ([]ProjectionStep, uint64, error) {
	completeByteCount := bytes.LastIndexByte(data, '\n') + 1
	if completeByteCount == 0 {
		return nil, startOffset, nil
	}
	return readProjectionStepsStream(
		&io.LimitedReader{R: bytes.NewReader(data[:completeByteCount]), N: int64(completeByteCount)},
		startOffset,
		expectedOrdinal,
		validate,
		isCancelled,
	)
}

// readProjectionStepsStream mirrors Rust #54639's projection loop: records are
// read incrementally from a reader bounded to the durable end captured for this
// pass, the line buffer is reused across records, and a trailing record without
// its newline stays pending for the next pass. A source that stops short of the
// captured end means the rollout shrank during projection.
func readProjectionStepsStream(limited *io.LimitedReader, startOffset uint64, expectedOrdinal uint64, validate func(*Line) error, isCancelled func() bool) ([]ProjectionStep, uint64, error) {
	reader := bufio.NewReader(limited)
	var lineBytes []byte
	// A pass with no decoded record reports no steps, like the empty vec a missing
	// rollout produces (Rust returns `Ok((Vec::new(), 0))`).
	var steps []ProjectionStep
	nextOrdinal := expectedOrdinal
	nextOffset := startOffset
	lineStart := startOffset
	for {
		// Rust #53947 checks cancellation between history records, so a cancelled
		// caller stops projecting instead of scanning the rest of the rollout.
		if err := checkRolloutReadCancelled(isCancelled); err != nil {
			return nil, startOffset, err
		}
		lineBytes = lineBytes[:0]
		if _, err := readProjectionRecord(reader, &lineBytes); err != nil {
			return nil, startOffset, err
		}
		if len(lineBytes) == 0 || lineBytes[len(lineBytes)-1] != '\n' {
			// An empty read reached the captured end; a record whose newline is
			// not durable yet is only a prefix. Both stay pending.
			break
		}
		physicalLine := lineBytes
		lineEnd := lineStart + uint64(len(physicalLine))
		if len(bytes.TrimSpace(physicalLine)) == 0 {
			nextOffset = lineEnd
			lineStart = lineEnd
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(physicalLine, &raw); err != nil || raw == nil {
			// Rust #42369: skip malformed records while advancing the byte
			// checkpoint so later valid history still materializes.
			nextOffset = lineEnd
			lineStart = lineEnd
			continue
		}
		ordinal, hasOrdinal := projectionOrdinal(raw["ordinal"])
		line, recognized := projectionLine(physicalLine, raw)
		if !recognized {
			// Unknown records only advance the byte checkpoint; a writer that
			// could not decode this line may reuse its ordinal, so keep the
			// ordinal available for the next decoded line to resolve.
			nextOffset = lineEnd
			lineStart = lineEnd
			continue
		}
		if !hasOrdinal {
			nextOffset = lineEnd
			lineStart = lineEnd
			continue
		}
		if validate != nil && validate(line) != nil {
			// Rust #42369: an invalid record cannot contribute history, so skip
			// its ordinal range instead of stalling the projection.
			if ordinal == ^uint64(0) {
				return nil, startOffset, errors.New("rollout ordinal exceeds integer range")
			}
			steps = append(steps, ProjectionStep{
				Kind:                ProjectionSkippedOrdinalRange,
				Ordinal:             nextOrdinal,
				EndOrdinalExclusive: ordinal + 1,
			})
			nextOrdinal = ordinal + 1
			nextOffset = lineEnd
			lineStart = lineEnd
			continue
		}
		if ordinal < nextOrdinal {
			// Duplicate or regressed ordinal: skip the record and keep moving.
			nextOffset = lineEnd
			lineStart = lineEnd
			continue
		}
		skipped := ordinal - nextOrdinal
		if skipped > 0 {
			steps = append(steps, ProjectionStep{
				Kind:                ProjectionSkippedOrdinalRange,
				Ordinal:             nextOrdinal,
				EndOrdinalExclusive: ordinal,
			})
		}
		if ordinal == ^uint64(0) {
			return nil, startOffset, errors.New("rollout ordinal exceeds integer range")
		}
		line.Ordinal = &ordinal
		steps = append(steps, ProjectionStep{
			Kind:            ProjectionLine,
			Line:            line,
			Ordinal:         ordinal,
			StartByteOffset: lineStart,
			EndByteOffset:   lineEnd,
		})
		nextOrdinal = ordinal + 1
		nextOffset = lineEnd
		lineStart = lineEnd
	}
	// Rust #54639: this pass promised to read up to the captured end, so a source
	// that delivered less shrank during projection. The error is reported as an
	// unexpected EOF, never as a short read.
	if limited.N != 0 {
		return nil, startOffset, fmt.Errorf("durable rollout shrank during projection: %w", io.ErrUnexpectedEOF)
	}
	return steps, nextOffset, nil
}

// readProjectionRecord mirrors Rust's `BufRead::read_until(b'\n', &mut
// line_bytes)`: it appends through the next newline into the reused buffer,
// growing it only to the largest record, and reports how many bytes it appended.
// Reaching the end is not an error, so a final record without its newline stays
// pending for the next pass.
func readProjectionRecord(reader *bufio.Reader, line *[]byte) (int, error) {
	total := 0
	for {
		chunk, err := reader.ReadSlice('\n')
		*line = append(*line, chunk...)
		total += len(chunk)
		switch {
		case err == nil:
			return total, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return total, nil
		default:
			return total, err
		}
	}
}
func projectionOrdinal(raw json.RawMessage) (uint64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var ordinal uint64
	if err := json.Unmarshal(raw, &ordinal); err != nil {
		return 0, false
	}
	return ordinal, true
}

func projectionLine(data []byte, raw map[string]json.RawMessage) (*Line, bool) {
	var kind string
	if err := json.Unmarshal(raw["type"], &kind); err != nil || !knownProjectionLineType(kind) {
		return nil, false
	}
	var line Line
	if err := unmarshalLine(data, &line); err != nil {
		return nil, false
	}
	return &line, true
}

func knownProjectionLineType(kind string) bool {
	switch kind {
	case "session_meta", "response_item", "item", "event_msg", "inter_agent_communication",
		"inter_agent_communication_metadata", "compacted", "turn_context", "world_state",
		"security_risk_score", "realtime_item":
		return true
	default:
		return false
	}
}
