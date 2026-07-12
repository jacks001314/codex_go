//go:build windows

package execserver

import (
	"fmt"
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
	readWant := fmt.Sprintf("path `%s` is not a file", readPath)

	readDone := make(chan error, 1)
	go func() {
		_, readErr := readFile(&FSReadFileParams{Path: readPath})
		readDone <- readErr
	}()
	select {
	case readErr := <-readDone:
		if readErr == nil || readErr.Error() != readWant {
			t.Fatalf("readFile(named pipe) error = %v, want %q", readErr, readWant)
		}
	case <-time.After(time.Second):
		t.Fatal("readFile(named pipe) hung")
	}

	streamPath := createPipe("codex-fs-stream-")
	streamWant := fmt.Sprintf("path `%s` is not a file", streamPath)
	openDone := make(chan error, 1)
	go func() {
		_, openErr := NewServer().openFile(&FSOpenParams{HandleID: "pipe", Path: streamPath})
		openDone <- openErr
	}()
	select {
	case openErr := <-openDone:
		if openErr == nil || openErr.Error() != streamWant {
			t.Fatalf("openFile(named pipe) error = %v, want %q", openErr, streamWant)
		}
	case <-time.After(time.Second):
		t.Fatal("openFile(named pipe) hung")
	}
}
