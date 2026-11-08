package conpty

import (
	"io"
	"sync"
)

// InputNormalizer is the stateful normalizer for bytes written to a Windows
// pseudoconsole, ported from Rust's `WindowsTtyInputNormalizer`
// (codex-rs/utils/pty/src/windows_input.rs).
//
// ConPTY accepts UTF-8 input, but an Enter key is a carriage return on Windows.
// Line feeds become carriage returns, and an existing CRLF is collapsed --
// including when the two bytes arrive in separate writes -- so each requested
// newline submits exactly one line. Backspace is encoded as DEL, which ConPTY
// translates to VK_BACK. Everything else, including UTF-8 text and terminal
// control characters, passes through unchanged.
type InputNormalizer struct {
	previousWasCR bool
}

// Normalize returns the bytes to write for one client chunk. The receiver keeps
// the CRLF state between calls, so callers must not share it between sessions.
func (n *InputNormalizer) Normalize(data []byte) []byte {
	normalized := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case '\b':
			normalized = append(normalized, 0x7f)
		case '\n':
			if !n.previousWasCR {
				normalized = append(normalized, '\r')
			}
		default:
			normalized = append(normalized, b)
		}
		n.previousWasCR = b == '\r'
	}
	return normalized
}

// NewInputWriteCloser wraps a pseudoconsole input handle so callers can forward
// client bytes unchanged: the wrapper applies InputNormalizer to every write.
// It is safe for concurrent writes.
func NewInputWriteCloser(inner io.WriteCloser) io.WriteCloser {
	return &normalizingWriter{inner: inner}
}

type normalizingWriter struct {
	mu         sync.Mutex
	normalizer InputNormalizer
	inner      io.WriteCloser
}

func (w *normalizingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inner == nil {
		return 0, io.ErrClosedPipe
	}
	normalized := w.normalizer.Normalize(data)
	if len(normalized) == 0 {
		return len(data), nil
	}
	if _, err := w.inner.Write(normalized); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *normalizingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inner == nil {
		return nil
	}
	err := w.inner.Close()
	w.inner = nil
	return err
}
