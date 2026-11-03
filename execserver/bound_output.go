package execserver

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BoundOutput is the last-message destination opened before sandboxed tools
// run. The handle keeps the original file bound, and every write rechecks that
// the path still names that file, so a tool cannot redirect the final write by
// replacing the path (Rust #58974, exec-server BoundOutput).
type BoundOutput struct {
	file *os.File
	path string
}

// OpenBoundOutput opens (creating if needed) the final-message file without
// truncating it. The open fails before a turn when the path is unsafe or cannot
// be verified.
func OpenBoundOutput(path string) (*BoundOutput, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("output must name a file")
	}
	leaf := filepath.Base(path)
	if leaf == "" || leaf == "." || leaf == ".." {
		return nil, fmt.Errorf("output must name a file")
	}
	file, err := openRegularFileForBoundWrite(path)
	if err != nil {
		return nil, err
	}
	bound := &BoundOutput{file: file, path: path}
	if info, statErr := file.Stat(); statErr != nil {
		_ = file.Close()
		return nil, statErr
	} else if info.Mode().IsRegular() {
		if err := rejectMultipleLinks(file); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	if err := bound.verifyPath(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return bound, nil
}

// Write replaces the bound file's contents after checking that its path still
// names the opened file.
func (b *BoundOutput) Write(contents []byte) error {
	if b == nil || b.file == nil {
		return os.ErrInvalid
	}
	if err := b.verifyPath(); err != nil {
		return err
	}
	info, err := b.file.Stat()
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		if err := rejectMultipleLinks(b.file); err != nil {
			return err
		}
		if err := b.file.Truncate(0); err != nil {
			return err
		}
		if _, err := b.file.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	if _, err := b.file.Write(contents); err != nil {
		return err
	}
	return b.verifyPath()
}

// Close releases the bound handle.
func (b *BoundOutput) Close() error {
	if b == nil || b.file == nil {
		return nil
	}
	file := b.file
	b.file = nil
	return file.Close()
}

func (b *BoundOutput) verifyPath() error {
	if b == nil || b.file == nil {
		return os.ErrInvalid
	}
	pathInfo, err := os.Lstat(b.path)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output path %q is a symlink", b.path)
	}
	openedInfo, err := b.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(pathInfo, openedInfo) {
		return fmt.Errorf("output path %q no longer names the bound file", b.path)
	}
	return nil
}
