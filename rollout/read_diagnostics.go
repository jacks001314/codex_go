package rollout

import (
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

// diagnosticHeaderBytes matches Rust's `read_diagnostics::Source` buffer: the
// zstd frame header is at most 18 bytes (magic, descriptor, window, dictionary
// id, and an 8-byte frame content size).
const diagnosticHeaderBytes = 18

// compressionReadFailure mirrors Rust's `FailureDetails`: byte counts captured
// when the first compressed-stream failure is observed. Optional fields stay
// unset when the corresponding metadata cannot be determined.
type compressionReadFailure struct {
	compressedBytes        *int64
	firstFrameContentBytes *uint64
	decodedBytes           uint64
	fileChanged            *bool
}

// diagnosticSource captures the leading frame header bytes and the metadata
// seen at open, so a later failure can be attributed without keeping paths or
// contents in the log record.
type diagnosticSource struct {
	file           *os.File
	openedMetadata os.FileInfo
	header         [diagnosticHeaderBytes]byte
	headerLen      int
}

func (source *diagnosticSource) Read(buffer []byte) (int, error) {
	n, err := source.file.Read(buffer)
	captured := n
	if remaining := len(source.header) - source.headerLen; captured > remaining {
		captured = remaining
	}
	if captured > 0 {
		copy(source.header[source.headerLen:source.headerLen+captured], buffer[:captured])
		source.headerLen += captured
	}
	return n, err
}

// diagnosticZstdDecoder mirrors Rust's `DiagnosticDecoder` (upstream #55545):
// it decodes a compressed rollout and records the first failure's diagnostics.
type diagnosticZstdDecoder struct {
	decoder      *zstd.Decoder
	source       *diagnosticSource
	decodedBytes uint64
	failure      *compressionReadFailure
}

// newDiagnosticZstdDecoder opens the zstd decoder over file, retaining the
// metadata observed at open. A decoder construction failure is returned
// unchanged, as in Rust's `DiagnosticDecoder::new`.
func newDiagnosticZstdDecoder(file *os.File) (*diagnosticZstdDecoder, error) {
	source := &diagnosticSource{file: file}
	if info, err := file.Stat(); err == nil {
		source.openedMetadata = info
	}
	decoder, err := zstd.NewReader(source)
	if err != nil {
		return nil, err
	}
	return &diagnosticZstdDecoder{decoder: decoder, source: source}, nil
}

func (decoder *diagnosticZstdDecoder) Read(buffer []byte) (int, error) {
	n, err := decoder.decoder.Read(buffer)
	if n > 0 {
		decoder.decodedBytes += uint64(n)
	}
	if err != nil && !errors.Is(err, io.EOF) && decoder.failure == nil {
		decoder.recordFailure(err)
	}
	return n, err
}

func (decoder *diagnosticZstdDecoder) Close() {
	decoder.decoder.Close()
}

// recordFailure captures Rust's `FailureDetails` and logs them at most once per
// process per minute. The record intentionally omits paths and contents.
func (decoder *diagnosticZstdDecoder) recordFailure(err error) {
	details := &compressionReadFailure{decodedBytes: decoder.decodedBytes}
	info, statErr := decoder.source.file.Stat()
	if statErr == nil {
		size := info.Size()
		details.compressedBytes = &size
	}
	if contentBytes, declared, ok := firstFrameContentSize(decoder.source.header[:decoder.source.headerLen]); ok && declared {
		details.firstFrameContentBytes = &contentBytes
	}
	if statErr == nil && decoder.source.openedMetadata != nil {
		changed := decoder.source.openedMetadata.Size() != info.Size() ||
			!decoder.source.openedMetadata.ModTime().Equal(info.ModTime())
		details.fileChanged = &changed
	}
	if compressionReadDiagnosticAllowed(time.Now()) {
		slog.Warn("compressed rollout read failed",
			"error_kind", rolloutCompressionErrorKind(err),
			"compressed_bytes", optionalInt64(details.compressedBytes),
			"first_frame_content_bytes", optionalUint64(details.firstFrameContentBytes),
			"decoded_bytes", details.decodedBytes,
			"file_changed", optionalBool(details.fileChanged),
		)
	}
	decoder.failure = details
}

var (
	compressionReadDiagnosticMu   sync.Mutex
	compressionReadDiagnosticLast time.Time
)

// compressionReadDiagnosticAllowed mirrors Rust's process-wide `LAST_REPORT`
// mutex: the first failure is reported, later failures wait for a minute.
func compressionReadDiagnosticAllowed(now time.Time) bool {
	compressionReadDiagnosticMu.Lock()
	defer compressionReadDiagnosticMu.Unlock()
	if !compressionReadDiagnosticLast.IsZero() && now.Sub(compressionReadDiagnosticLast) < time.Minute {
		return false
	}
	compressionReadDiagnosticLast = now
	return true
}

// resetCompressionReadDiagnosticsForTest restores the rate limiter so tests can
// assert both the reported and suppressed cases.
func resetCompressionReadDiagnosticsForTest() {
	compressionReadDiagnosticMu.Lock()
	defer compressionReadDiagnosticMu.Unlock()
	compressionReadDiagnosticLast = time.Time{}
}

func optionalInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalUint64(value *uint64) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

// firstFrameContentSize mirrors `zstd_safe::get_frame_content_size` for the
// captured prefix. ok is false until a complete frame header is captured;
// declared is false when the frame carries no content-size field. Go streams
// rollouts through the klauspost encoder, which does not advertise a size, so
// declared is false for Go-written archives but true for Rust-written ones
// (`set_pledged_src_size`).
func firstFrameContentSize(header []byte) (size uint64, declared bool, ok bool) {
	if len(header) < 5 {
		return 0, false, false
	}
	if header[0] != 0x28 || header[1] != 0xb5 || header[2] != 0x2f || header[3] != 0xfd {
		return 0, false, false
	}
	descriptor := header[4]
	contentSizeFlag := descriptor >> 6
	singleSegment := descriptor&0x20 != 0
	// RFC 8878 2.1.1: Dictionary_ID_flag occupies bits 1-0, bit 2 is the
	// content checksum flag, and bit 3 is reserved.
	dictionaryIDBytes := [4]int{0, 1, 2, 4}[descriptor&0x03]
	contentSizeBytes := 0
	switch contentSizeFlag {
	case 0:
		if singleSegment {
			contentSizeBytes = 1
		}
	case 1:
		contentSizeBytes = 2
	case 2:
		contentSizeBytes = 4
	case 3:
		contentSizeBytes = 8
	}
	offset := 5
	if !singleSegment {
		offset++
	}
	offset += dictionaryIDBytes
	if contentSizeBytes == 0 {
		return 0, false, true
	}
	if len(header) < offset+contentSizeBytes {
		return 0, false, false
	}
	field := header[offset : offset+contentSizeBytes]
	switch contentSizeBytes {
	case 1:
		return uint64(field[0]), true, true
	case 2:
		return uint64(binary.LittleEndian.Uint16(field)) + 256, true, true
	case 4:
		return uint64(binary.LittleEndian.Uint32(field)), true, true
	default:
		return binary.LittleEndian.Uint64(field), true, true
	}
}
