package rollout

import (
	"errors"
	"io"
)

// ErrRolloutReadCancelled mirrors Rust #53947
// (`open_rollout_seekable_reader` / `CancellableReader` in
// rollout/src/seekable_reader.rs): the calling task went away and the read
// stopped. Cancellation is reported as an error, never as EOF, so a partial
// read can never be mistaken for a complete history.
var ErrRolloutReadCancelled = errors.New("rollout read cancelled")

// checkRolloutReadCancelled reports the caller's cancellation as the same error
// the Rust reader uses (`io::Error::other("rollout read cancelled")`).
func checkRolloutReadCancelled(isCancelled func() bool) error {
	if isCancelled != nil && isCancelled() {
		return ErrRolloutReadCancelled
	}
	return nil
}

// cancellableRolloutReader mirrors Rust's `CancellableReader`: every read
// consults the caller's cancellation predicate, so a decompression that runs
// long after the task went away stops instead of finishing. Reads are bounded
// to 64 KiB like the Rust wrapper, which keeps the cancellation check frequent
// even when the caller asks for a large buffer.
type cancellableRolloutReader struct {
	inner       io.Reader
	isCancelled func() bool
}

func (r *cancellableRolloutReader) Read(buffer []byte) (int, error) {
	if err := checkRolloutReadCancelled(r.isCancelled); err != nil {
		return 0, err
	}
	limit := len(buffer)
	if limit > 64*1024 {
		limit = 64 * 1024
	}
	read, err := r.inner.Read(buffer[:limit])
	if err != nil {
		return read, err
	}
	return read, checkRolloutReadCancelled(r.isCancelled)
}
