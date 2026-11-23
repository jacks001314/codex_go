package tui

import (
	"errors"
	"io"
	"os"
)

// MaxPastedImageBytes bounds the copy of a pasted image file (Rust #62687
// clipboard_paste::MAX_PASTED_IMAGE_BYTES). Pasted paths are copied while the
// client can still read them, so a moved or deleted original cannot break the
// attachment before submission. It is a variable so tests can lower the limit
// without writing a gigabyte file.
var MaxPastedImageBytes int64 = 1024 * 1024 * 1024

// ErrPastedImageTooLarge matches Rust's paste error for an image over the
// input limit.
var ErrPastedImageTooLarge = errors.New("image exceeds the 1 GiB input limit")

// PersistPastedImage copies source into a private temporary file and returns
// the path, mirroring Rust clipboard_paste::persist_pasted_image (#62687). The
// caller owns the file; clipboard PNGs and pasted image paths share it.
func PersistPastedImage(source io.Reader, suffix string) (string, error) {
	if source == nil {
		return "", errors.New("pasted image source is nil")
	}
	tmp, err := os.CreateTemp("", "codex-clipboard-*"+suffix)
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	written, copyErr := io.Copy(tmp, io.LimitReader(source, MaxPastedImageBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	if written > MaxPastedImageBytes {
		_ = os.Remove(path)
		return "", ErrPastedImageTooLarge
	}
	return path, nil
}
