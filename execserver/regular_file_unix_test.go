//go:build !windows

package execserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestFileReadsRejectSymlinkLikeRust(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readFile(&FSReadFileParams{Path: link}); err == nil || !strings.Contains(err.Error(), "not a") {
		t.Fatalf("readFile(symlink) error = %v, want regular-file rejection", err)
	}
}
