package execserver

import (
	"context"
	"io"
)

// FileReadChunkSource yields the next block of a streamed read. It reports
// ok=false at the end of the stream; every yielded block is nonempty and at most
// FileReadChunkSize bytes, mirroring the bounded block stream Rust's
// `ExecutorFileSystem::read_file_stream` hands its caller
// (codex-rs/file-system/src/lib.rs:658, upstream #54674 `e7de1f4440`). A non-nil
// error ends the stream.
type FileReadChunkSource func(ctx context.Context) (block []byte, ok bool, err error)

// FileReadStreamSource pairs a pull-based read source with the release hook for
// the handle it opened. Close is safe after the source is drained and after a
// failure, and reports nil on a second call.
type FileReadStreamSource struct {
	Next  FileReadChunkSource
	Close func() error
}

// OpenLocalFileReadStream opens a local regular file for a bounded,
// block-at-a-time read. It resolves the path through the same regular-file open
// the `fs/readFile` handler uses (`openRegularFileForRead`: O_NOFOLLOW on Unix,
// reparse-point preservation on Windows, and regular-file validation), so the
// in-process environment keeps Rust's symlink and file-type rules
// (codex-rs/exec-server/src/local_file_system.rs:170, upstream #54674).
func OpenLocalFileReadStream(path string) (*FileReadStreamSource, error) {
	file, err := openRegularFileForRead(path)
	if err != nil {
		return nil, err
	}
	closed := false
	next := func(ctx context.Context) ([]byte, bool, error) {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		buffer := make([]byte, fileReadChunkSize)
		read, readErr := file.Read(buffer)
		if read > 0 {
			return buffer[:read], true, nil
		}
		if readErr == nil || io.EOF == readErr {
			return nil, false, nil
		}
		return nil, false, readErr
	}
	closeStream := func() error {
		if closed {
			return nil
		}
		closed = true
		return file.Close()
	}
	return &FileReadStreamSource{Next: next, Close: closeStream}, nil
}
