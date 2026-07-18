//go:build windows

package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestStdioToUDSBridgesWindowsNamedPipe(t *testing.T) {
	pipeName := fmt.Sprintf(`\\.\pipe\codex-stdio-uds-%d-%d`, windows.GetCurrentProcessId(), time.Now().UnixNano())
	namePtr, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		t.Fatalf("UTF16PtrFromString error = %v", err)
	}
	handle, err := windows.CreateNamedPipe(
		namePtr,
		windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1,
		4096,
		4096,
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("CreateNamedPipe error = %v", err)
	}
	server := windows.Handle(handle)
	defer windows.CloseHandle(server)

	done := make(chan error, 1)
	go func() {
		if err := windows.ConnectNamedPipe(server, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
			done <- err
			return
		}
		file := os.NewFile(uintptr(server), pipeName)
		line, err := bufio.NewReader(file).ReadString('\n')
		if err != nil {
			done <- err
			return
		}
		if _, err := file.Write([]byte("reply:" + line)); err != nil {
			done <- err
			return
		}
		done <- file.Close()
	}()

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"stdio-to-uds", pipeName}, strings.NewReader("hello\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("stdio-to-uds returned error: %v", err)
	}
	if stdout.String() != "reply:hello\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if err := <-done; err != nil {
		t.Fatalf("server error = %v", err)
	}
}
