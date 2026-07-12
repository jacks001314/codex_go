//go:build !windows

package execserver

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileReadsRejectFIFONonBlockingLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "named-pipe")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v", err)
	}
	want := fmt.Sprintf("path `%s` is not a file", path)

	readDone := make(chan error, 1)
	go func() {
		_, err := readFile(&FSReadFileParams{Path: path})
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err == nil || err.Error() != want {
			t.Fatalf("readFile(FIFO) error = %v, want %q", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("readFile(FIFO) waited for a writer")
	}

	openDone := make(chan error, 1)
	go func() {
		_, err := NewServer().openFile(&FSOpenParams{HandleID: "fifo", Path: path})
		openDone <- err
	}()
	select {
	case err := <-openDone:
		if err == nil || err.Error() != want {
			t.Fatalf("openFile(FIFO) error = %v, want %q", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("openFile(FIFO) waited for a writer")
	}
}
