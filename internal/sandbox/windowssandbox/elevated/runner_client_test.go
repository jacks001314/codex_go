package elevated

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRefreshableSandboxCredsErrorRecognizesCredentialAndChildStartFailures(t *testing.T) {
	for _, tc := range []struct {
		code uint32
		want bool
	}{
		{errorLogonFailure, true},
		{errorNoSuchLogonSession, true},
		{errorNotFound, false},
	} {
		err := &RunnerLogonError{Code: tc.code}
		if got := IsRefreshableSandboxCredsError(err, nil); got != tc.want {
			t.Fatalf("IsRefreshableSandboxCredsError(logon %d) = %v, want %v", tc.code, got, tc.want)
		}
	}

	for _, tc := range []struct {
		stage ErrorStage
		code  uint32
		want  bool
	}{
		{ErrorStageSpawnChild, errorNoSuchLogonSession, true},
		{ErrorStageSpawnChild, errorNotFound, false},
		{ErrorStageReadSpawnRequest, errorNoSuchLogonSession, false},
	} {
		err := &RunnerStartupError{Payload: ErrorPayload{
			Message:          "runner startup failed",
			Stage:            tc.stage,
			WindowsErrorCode: &tc.code,
		}}
		if got := IsRefreshableSandboxCredsError(err, []string{"cmd.exe"}); got != tc.want {
			t.Fatalf("IsRefreshableSandboxCredsError(%s/%d) = %v, want %v", tc.stage, tc.code, got, tc.want)
		}
	}

	err := &RunnerStartupError{Payload: ErrorPayload{
		Message:          "runner startup failed",
		Stage:            ErrorStageSpawnChild,
		WindowsErrorCode: uint32Ptr(errorNoSuchLogonSession),
	}}
	command := []string{`C:\Users\user\AppData\Local\Microsoft\WindowsApps\pwsh.exe`}
	if IsRefreshableSandboxCredsError(err, command) {
		t.Fatalf("WindowsApps no-such-logon-session should not be refreshable")
	}
}

func TestRetryRunnerSpawnOnceRefreshesOnlyRefreshableErrors(t *testing.T) {
	initial := SandboxCredentials{Username: "old"}
	refreshed := SandboxCredentials{Username: "new"}
	attempts := 0
	got, err := RetryRunnerSpawnOnce(initial, []string{"cmd.exe"}, func(creds SandboxCredentials) (string, error) {
		attempts++
		if creds.Username == "old" {
			return "", &RunnerLogonError{Code: errorLogonFailure}
		}
		return creds.Username, nil
	}, func() (SandboxCredentials, error) {
		return refreshed, nil
	})
	if err != nil || got != "new" || attempts != 2 {
		t.Fatalf("RetryRunnerSpawnOnce refreshable = (%q, %v, attempts %d)", got, err, attempts)
	}

	sentinel := errors.New("not refreshable")
	_, err = RetryRunnerSpawnOnce(initial, []string{"cmd.exe"}, func(creds SandboxCredentials) (string, error) {
		return "", sentinel
	}, func() (SandboxCredentials, error) {
		t.Fatalf("refresh should not be called")
		return refreshed, nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RetryRunnerSpawnOnce non-refreshable error = %v", err)
	}
}

func TestRunnerTransportSendAndReadSpawnReady(t *testing.T) {
	var write bytes.Buffer
	transport := &RunnerTransport{PipeWrite: nopWriteCloser{Writer: &write}}
	request := SpawnRequest{Command: []string{"cmd.exe"}, CWD: `C:\repo`}
	if err := transport.SendSpawnRequest(request); err != nil {
		t.Fatalf("SendSpawnRequest() error = %v", err)
	}
	frame, err := ReadFrame(bytes.NewReader(write.Bytes()))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if frame.Message.SpawnRequest == nil || frame.Message.SpawnRequest.Command[0] != "cmd.exe" {
		t.Fatalf("spawn request frame = %#v", frame.Message.SpawnRequest)
	}

	var read bytes.Buffer
	if err := WriteFrame(&read, &FramedMessage{Version: IPCProtocolVersion, Message: Message{SpawnReady: &SpawnReady{ProcessID: 42}}}); err != nil {
		t.Fatalf("WriteFrame(spawn_ready) error = %v", err)
	}
	transport.PipeRead = nopReadCloser{Reader: &read}
	if err := transport.ReadSpawnReady(); err != nil {
		t.Fatalf("ReadSpawnReady() error = %v", err)
	}
}

func TestRunnerTransportReadSpawnReadyReturnsStartupError(t *testing.T) {
	var read bytes.Buffer
	code := uint32(1312)
	if err := WriteFrame(&read, &FramedMessage{Version: IPCProtocolVersion, Message: Message{Error: &ErrorPayload{
		Message:          "spawn failed",
		Stage:            ErrorStageSpawnChild,
		WindowsErrorCode: &code,
	}}}); err != nil {
		t.Fatalf("WriteFrame(error) error = %v", err)
	}
	transport := &RunnerTransport{PipeRead: nopReadCloser{Reader: &read}}
	err := transport.ReadSpawnReady()
	var startupErr *RunnerStartupError
	if !errors.As(err, &startupErr) || !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("ReadSpawnReady() error = %v, want RunnerStartupError", err)
	}
}

type nopWriteCloser struct {
	*bytes.Buffer
	Writer interface {
		Write([]byte) (int, error)
	}
}

func (w nopWriteCloser) Write(p []byte) (int, error) {
	return w.Writer.Write(p)
}

func (w nopWriteCloser) Close() error {
	return nil
}

type nopReadCloser struct {
	*bytes.Buffer
	Reader interface {
		Read([]byte) (int, error)
	}
}

func (r nopReadCloser) Read(p []byte) (int, error) {
	return r.Reader.Read(p)
}

func (r nopReadCloser) Close() error {
	return nil
}

func uint32Ptr(value uint32) *uint32 {
	return &value
}
