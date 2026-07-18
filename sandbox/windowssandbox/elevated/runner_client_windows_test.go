//go:build windows

package elevated

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestConnectRunnerOpensNamedPipe(t *testing.T) {
	name, _ := PipePair()
	username, err := CurrentUsername()
	if err != nil {
		t.Fatalf("CurrentUsername() error = %v", err)
	}
	pipe, err := CreateNamedPipe(name, PipeAccessDuplex, username)
	if err != nil {
		t.Fatalf("CreateNamedPipe() error = %v", err)
	}
	defer pipe.Close()

	clientCh := make(chan *RunnerClient, 1)
	errCh := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		client, err := ConnectRunner(name)
		if err != nil {
			errCh <- err
			return
		}
		clientCh <- client
	}()

	if err := ConnectPipe(pipe, windows.GetCurrentProcessId()); err != nil {
		t.Fatalf("ConnectPipe() error = %v", err)
	}
	select {
	case err := <-errCh:
		t.Fatalf("ConnectRunner() error = %v", err)
	case client := <-clientCh:
		if client.PipeName != name || client.Handle == 0 {
			t.Fatalf("client = %#v", client)
		}
		if err := client.Close(); err != nil {
			t.Fatalf("client.Close() error = %v", err)
		}
	}
}
