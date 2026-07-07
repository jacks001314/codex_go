//go:build windows

package elevated

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCreateNamedPipeAndConnectCurrentUser(t *testing.T) {
	name, _ := PipePair()
	username, err := CurrentUsername()
	if err != nil {
		t.Fatalf("CurrentUsername() error = %v", err)
	}
	pipe, err := CreateNamedPipe(name, PipeAccessInbound, username)
	if err != nil {
		t.Fatalf("CreateNamedPipe() error = %v", err)
	}
	defer pipe.Close()

	errCh := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		namePtr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			errCh <- err
			return
		}
		handle, err := windows.CreateFile(namePtr, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			errCh <- err
			return
		}
		defer windows.CloseHandle(handle)
		errCh <- nil
	}()

	if err := ConnectPipe(pipe, windows.GetCurrentProcessId()); err != nil {
		t.Fatalf("ConnectPipe() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("client CreateFile() error = %v", err)
	}
}
