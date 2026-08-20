//go:build windows

package execserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

func TestFileReadsRejectNamedPipesLikeRust(t *testing.T) {
	createPipe := func(prefix string) string {
		path := `\\.\pipe\` + prefix + uuid.NewString()
		pathPtr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatalf("UTF16PtrFromString() error = %v", err)
		}
		handle, err := windows.CreateNamedPipe(
			pathPtr,
			windows.PIPE_ACCESS_DUPLEX,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			1,
			4096,
			4096,
			0,
			nil,
		)
		if err != nil {
			t.Fatalf("CreateNamedPipe() error = %v", err)
		}
		t.Cleanup(func() { _ = windows.CloseHandle(handle) })
		return path
	}
	readPath := createPipe("codex-fs-read-")

	readDone := make(chan error, 1)
	go func() {
		_, readErr := readFile(&FSReadFileParams{Path: readPath})
		readDone <- readErr
	}()
	select {
	case readErr := <-readDone:
		if readErr == nil {
			t.Fatal("readFile(named pipe) succeeded, want rejection")
		}
	case <-time.After(time.Second):
		t.Fatal("readFile(named pipe) hung")
	}

	streamPath := createPipe("codex-fs-stream-")
	openDone := make(chan error, 1)
	go func() {
		_, openErr := NewServer().openFile(&FSOpenParams{HandleID: "pipe", Path: streamPath})
		openDone <- openErr
	}()
	select {
	case openErr := <-openDone:
		if openErr == nil {
			t.Fatal("openFile(named pipe) succeeded, want rejection")
		}
	case <-time.After(time.Second):
		t.Fatal("openFile(named pipe) hung")
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
