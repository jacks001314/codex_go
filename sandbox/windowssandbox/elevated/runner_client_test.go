package elevated

import (
	"bytes"
	"errors"
	"fmt"
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

// Mirrors Rust logon_service_already_running_retries_once_without_refresh
// (#49325, upstream 26dd19ef47): the retry reuses the original credentials and
// never refreshes them.
func TestRetryRunnerSpawnOnceRetriesServiceAlreadyRunningWithoutRefreshLikeRust(t *testing.T) {
	creds := SandboxCredentials{Username: "sandbox-user", Password: "test-only"}
	attempts := 0
	got, err := RetryRunnerSpawnOnce(creds, nil, func(attempt SandboxCredentials) (int, error) {
		attempts++
		if attempt != creds {
			t.Fatalf("attempt %d credentials = %#v, want %#v", attempts, attempt, creds)
		}
		if attempts == 1 {
			return 0, fmt.Errorf("runner launch failed: %w", &RunnerLogonError{Code: errorServiceAlreadyRunning})
		}
		return 42, nil
	}, func() (SandboxCredentials, error) {
		t.Fatalf("1056 must not refresh credentials")
		return SandboxCredentials{}, nil
	})
	if err != nil || got != 42 || attempts != 2 {
		t.Fatalf("RetryRunnerSpawnOnce(1056) = (%d, %v, attempts %d), want (42, nil, 2)", got, err, attempts)
	}
}

// Mirrors Rust logon_service_already_running_stops_after_two_failures.
func TestRetryRunnerSpawnOnceServiceAlreadyRunningStopsAfterTwoFailuresLikeRust(t *testing.T) {
	creds := SandboxCredentials{Username: "sandbox-user", Password: "test-only"}
	attempts := 0
	_, err := RetryRunnerSpawnOnce(creds, nil, func(SandboxCredentials) (struct{}, error) {
		attempts++
		return struct{}{}, &RunnerLogonError{Code: errorServiceAlreadyRunning}
	}, func() (SandboxCredentials, error) {
		t.Fatalf("1056 must not refresh credentials")
		return SandboxCredentials{}, nil
	})
	var logonErr *RunnerLogonError
	if attempts != 2 || !errors.As(err, &logonErr) || logonErr.Code != errorServiceAlreadyRunning {
		t.Fatalf("RetryRunnerSpawnOnce(1056 twice) = (attempts %d, %v), want (2, RunnerLogonError 1056)", attempts, err)
	}
}

// Mirrors Rust child_service_already_running_does_not_replay_command: the same
// code coming from child startup is not eligible for the retry.
func TestRetryRunnerSpawnOnceChildServiceAlreadyRunningDoesNotReplayLikeRust(t *testing.T) {
	attempts := 0
	_, err := RetryRunnerSpawnOnce(SandboxCredentials{Username: "sandbox-user", Password: "test-only"}, nil, func(SandboxCredentials) (struct{}, error) {
		attempts++
		return struct{}{}, &RunnerStartupError{Payload: ErrorPayload{
			Message:          "child startup failed",
			Stage:            ErrorStageSpawnChild,
			WindowsErrorCode: uint32Ptr(errorServiceAlreadyRunning),
		}}
	}, func() (SandboxCredentials, error) {
		t.Fatalf("1056 must not refresh credentials")
		return SandboxCredentials{}, nil
	})
	var startupErr *RunnerStartupError
	if attempts != 1 || !errors.As(err, &startupErr) || startupErr.Payload.WindowsErrorCode == nil ||
		*startupErr.Payload.WindowsErrorCode != errorServiceAlreadyRunning {
		t.Fatalf("child 1056 = (attempts %d, %v), want (1, RunnerStartupError 1056)", attempts, err)
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
