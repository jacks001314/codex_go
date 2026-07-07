package elevated

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	RunnerSpawnReadyTimeout             = 15 * time.Second
	RunnerPipeConnectTimeout            = 15 * time.Second
	RunnerSpawnReadyPollInterval        = 50 * time.Millisecond
	errorLogonFailure            uint32 = 1326
	errorNoSuchLogonSession      uint32 = 1312
	errorNotFound                uint32 = 1168
)

type RunnerLogonError struct {
	Code uint32
}

func (e *RunnerLogonError) Error() string {
	if e == nil {
		return "CreateProcessWithLogonW failed"
	}
	return fmt.Sprintf("CreateProcessWithLogonW failed: %d", e.Code)
}

type RunnerStartupError struct {
	Payload ErrorPayload
}

func (e *RunnerStartupError) Error() string {
	if e == nil {
		return "runner startup failed"
	}
	message := fmt.Sprintf("runner failed during %s: %s", e.Payload.Stage, e.Payload.Message)
	if e.Payload.WindowsErrorCode != nil {
		message += fmt.Sprintf(" (Windows error %d)", *e.Payload.WindowsErrorCode)
	}
	return message
}

type RunnerClient struct {
	PipeName string
	Handle   uintptr
	file     *os.File
}

type RunnerTransport struct {
	PipeWrite io.WriteCloser
	PipeRead  io.ReadCloser
}

type SandboxCredentials struct {
	Username string
	Password string
	Domain   string
}

func ConnectRunner(pipeName string) (*RunnerClient, error) {
	return connectRunner(pipeName)
}

func (c *RunnerClient) Close() error {
	return closeRunnerClient(c)
}

func (t *RunnerTransport) SendSpawnRequest(request SpawnRequest) error {
	if t == nil || t.PipeWrite == nil {
		return fmt.Errorf("runner write pipe is nil")
	}
	return WriteFrame(t.PipeWrite, &FramedMessage{
		Version: IPCProtocolVersion,
		Message: Message{SpawnRequest: &request},
	})
}

func (t *RunnerTransport) ReadSpawnReady() error {
	if t == nil || t.PipeRead == nil {
		return fmt.Errorf("runner read pipe is nil")
	}
	msg, err := readFrameWithTimeout(t.PipeRead, RunnerSpawnReadyTimeout)
	if err != nil {
		return err
	}
	if msg == nil {
		return fmt.Errorf("runner pipe closed before spawn_ready")
	}
	switch {
	case msg.Message.SpawnReady != nil:
		return nil
	case msg.Message.Error != nil:
		return &RunnerStartupError{Payload: *msg.Message.Error}
	default:
		return fmt.Errorf("expected spawn_ready from runner, got %s", msg.Message.Type())
	}
}

func (t *RunnerTransport) IntoFiles() (*os.File, *os.File) {
	if t == nil {
		return nil, nil
	}
	write, _ := t.PipeWrite.(*os.File)
	read, _ := t.PipeRead.(*os.File)
	t.PipeWrite = nil
	t.PipeRead = nil
	return write, read
}

func (t *RunnerTransport) Close() error {
	if t == nil {
		return nil
	}
	var firstErr error
	if t.PipeWrite != nil {
		if err := t.PipeWrite.Close(); err != nil {
			firstErr = err
		}
		t.PipeWrite = nil
	}
	if t.PipeRead != nil {
		if err := t.PipeRead.Close(); firstErr == nil && err != nil {
			firstErr = err
		}
		t.PipeRead = nil
	}
	return firstErr
}

func IsRefreshableSandboxCredsError(err error, command []string) bool {
	var logonErr *RunnerLogonError
	if errors.As(err, &logonErr) {
		return isRefreshableWindowsError(logonErr.Code)
	}
	var startupErr *RunnerStartupError
	if errors.As(err, &startupErr) {
		code := startupErr.Payload.WindowsErrorCode
		if startupErr.Payload.Stage != ErrorStageSpawnChild || code == nil || !isRefreshableWindowsError(*code) {
			return false
		}
		return *code != errorNoSuchLogonSession || !commandTargetsWindowsApps(command)
	}
	return false
}

func RetryRunnerSpawnOnce[T any](
	creds SandboxCredentials,
	command []string,
	spawn func(SandboxCredentials) (T, error),
	refresh func() (SandboxCredentials, error),
) (T, error) {
	result, err := spawn(creds)
	if err == nil {
		return result, nil
	}
	if !IsRefreshableSandboxCredsError(err, command) {
		var zero T
		return zero, err
	}
	refreshed, refreshErr := refresh()
	if refreshErr != nil {
		var zero T
		return zero, refreshErr
	}
	return spawn(refreshed)
}

func SpawnRunnerTransport(codexHome string, cwd string, creds *SandboxCredentials, currentExe string, request SpawnRequest) (*RunnerTransport, error) {
	return spawnRunnerTransport(codexHome, cwd, creds, currentExe, request)
}

func readFrameWithTimeout(reader io.Reader, timeout time.Duration) (*FramedMessage, error) {
	type result struct {
		msg *FramedMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := ReadFrame(reader)
		ch <- result{msg: msg, err: err}
	}()
	select {
	case res := <-ch:
		return res.msg, res.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timed out after %dms waiting for runner spawn_ready", timeout.Milliseconds())
	}
}

func isRefreshableWindowsError(code uint32) bool {
	return code == errorLogonFailure || code == errorNoSuchLogonSession
}

func commandTargetsWindowsApps(command []string) bool {
	if len(command) == 0 {
		return false
	}
	parts := strings.FieldsFunc(command[0], func(r rune) bool {
		return r == '\\' || r == '/'
	})
	for _, part := range parts {
		if strings.EqualFold(part, "WindowsApps") {
			return true
		}
	}
	return false
}
