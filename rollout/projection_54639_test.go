package rollout

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// TestReadProjectionStepsPreservesTrailingPartialLineBoundariesLikeRust mirrors
// Rust #54639's extended `catch_up_preserves_trailing_partial_line_boundaries`
// (thread-store/src/local/thread_history_materialization_tests.rs): 64 KiB of
// padding per record forces the streaming reader past any single read buffer, the
// projected record keeps its durable byte positions, and a record whose newline
// is not durable yet stays pending until a later pass projects it.
func TestReadProjectionStepsPreservesTrailingPartialLineBoundariesLikeRust(t *testing.T) {
	padding := strings.Repeat(" ", 64*1024)
	completeLine := strings.TrimSuffix(projectionLineJSON(1, "turn-1"), "\n") + padding
	partialLine := strings.TrimSuffix(projectionLineJSON(2, "turn-1"), "\n") + padding
	completeSuffix := completeLine + "\n"
	path := writeProjectionFixture(t, completeSuffix+partialLine)

	// The session metadata line already advanced the checkpoint to ordinal 1.
	steps, nextOffset, err := ReadProjectionSteps(path, 0, 1)
	if err != nil {
		t.Fatalf("ReadProjectionSteps() error = %v", err)
	}
	if len(steps) != 1 || steps[0].Kind != ProjectionLine || steps[0].Ordinal != 1 {
		t.Fatalf("steps = %#v", steps)
	}
	if steps[0].StartByteOffset != 0 || steps[0].EndByteOffset != uint64(len(completeSuffix)) {
		t.Fatalf("record byte range = [%d,%d), want [0,%d)",
			steps[0].StartByteOffset, steps[0].EndByteOffset, len(completeSuffix))
	}
	if nextOffset != uint64(len(completeSuffix)) {
		t.Fatalf("next offset = %d, want the padded partial record to stay pending (%d)",
			nextOffset, len(completeSuffix))
	}

	// The newline becomes durable: the pending record is projected with the byte
	// positions it occupies in the completed rollout.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	steps, nextOffset, err = ReadProjectionSteps(path, nextOffset, 2)
	if err != nil {
		t.Fatalf("ReadProjectionSteps() error = %v", err)
	}
	if len(steps) != 1 || steps[0].Kind != ProjectionLine || steps[0].Ordinal != 2 {
		t.Fatalf("steps = %#v", steps)
	}
	if steps[0].StartByteOffset != uint64(len(completeSuffix)) || steps[0].EndByteOffset != uint64(info.Size()) {
		t.Fatalf("completed record byte range = [%d,%d), want [%d,%d)",
			steps[0].StartByteOffset, steps[0].EndByteOffset, len(completeSuffix), info.Size())
	}
	if nextOffset != uint64(info.Size()) {
		t.Fatalf("next offset = %d, want %d", nextOffset, info.Size())
	}
}

// TestReadProjectionStepsReportsShrinkDuringProjectionLikeRust pins the error
// Rust #54639 added: the pass is bounded to the durable end captured before it,
// so a source that stops short of that end shrank during projection and is
// reported as an unexpected EOF instead of a short read.
func TestReadProjectionStepsReportsShrinkDuringProjectionLikeRust(t *testing.T) {
	frozen := []byte(projectionLineJSON(0, "turn-0") + projectionLineJSON(1, "turn-1"))

	// The trailing newline is not durable, so the source cannot deliver the
	// captured byte count.
	shrunk := &io.LimitedReader{R: bytes.NewReader(frozen[:len(frozen)-1]), N: int64(len(frozen))}
	steps, nextOffset, err := readProjectionStepsStream(shrunk, 0, 0, nil, nil)
	if err == nil {
		t.Fatal("shrunk projection error = nil, want the unexpected-EOF error")
	}
	if !strings.Contains(err.Error(), "durable rollout shrank during projection") {
		t.Fatalf("shrunk projection error = %q, want the Rust message", err)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("shrunk projection error = %v, want it to wrap io.ErrUnexpectedEOF", err)
	}
	if steps != nil || nextOffset != 0 {
		t.Fatalf("shrunk projection = steps %#v offset %d, want none", steps, nextOffset)
	}

	// Control: the same records with their durable end project normally.
	steps, nextOffset, err = readProjectionStepsStream(
		&io.LimitedReader{R: bytes.NewReader(frozen), N: int64(len(frozen))}, 0, 0, nil, nil)
	if err != nil || len(steps) != 2 || nextOffset != uint64(len(frozen)) {
		t.Fatalf("intact projection = steps %d offset %d err %v, want both records", len(steps), nextOffset, err)
	}
}
