package appserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcessServiceSpawnEmitsExitNotification(t *testing.T) {
	service := NewProcessService()
	service.DefaultTimeoutMS = 1000
	sink := NewNotificationBuffer()
	params := &ProcessSpawnParams{
		Command:       processTestEchoCommand("hello"),
		ProcessHandle: "proc-1",
		CWD:           t.TempDir(),
	}

	if _, err := service.Spawn(context.Background(), params, sinkNotifyFunc(sink)); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	notification := waitForProcessExited(t, sink, "proc-1")
	exited := notification.Params.(*ProcessExitedNotification)
	if exited.ExitCode != 0 || strings.TrimSpace(exited.Stdout) != "hello" {
		t.Fatalf("exited = %+v", exited)
	}
}

func TestProcessServiceSpawnReturnsBeforeExitAndEmitsExitNotificationLikeRust(t *testing.T) {
	service := NewProcessService()
	service.DefaultTimeoutMS = 10_000
	sink := NewNotificationBuffer()
	dir := t.TempDir()
	probeFile := filepath.Join(dir, "process-created")
	releaseFile := filepath.Join(dir, "process-release")
	probeEnv := probeFile
	releaseEnv := releaseFile
	params := &ProcessSpawnParams{
		Command:       processTestProbeReleaseCommand(),
		ProcessHandle: "one-shot-1",
		CWD:           dir,
		Env: map[string]*string{
			"CODEX_PROCESS_EXEC_PROBE_FILE":   &probeEnv,
			"CODEX_PROCESS_EXEC_RELEASE_FILE": &releaseEnv,
		},
	}
	defer func() {
		_ = os.WriteFile(releaseFile, []byte("release"), 0o600)
	}()

	responseCh := make(chan *ProcessSpawnResponse, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, err := service.Spawn(context.Background(), params, sinkNotifyFunc(sink))
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- response
	}()

	select {
	case err := <-errorCh:
		t.Fatalf("Spawn() error = %v", err)
	case <-responseCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Spawn() did not return before process exit")
	}
	waitForProcessFile(t, probeFile)
	if data, err := os.ReadFile(probeFile); err != nil || string(data) != "process" {
		t.Fatalf("probe file = %q, %v; want process", data, err)
	}
	if processExitedObserved(sink, "one-shot-1") {
		t.Fatalf("process/exited was emitted before release file existed")
	}
	if err := os.WriteFile(releaseFile, []byte("release"), 0o600); err != nil {
		t.Fatalf("WriteFile(release) error = %v", err)
	}
	exited := waitForProcessExited(t, sink, "one-shot-1").Params.(*ProcessExitedNotification)
	if exited.ExitCode != 0 ||
		exited.Stdout != "process-out" || exited.StdoutCapReached ||
		exited.Stderr != "process-err" || exited.StderrCapReached {
		t.Fatalf("exited = %+v", exited)
	}
}

func TestProcessServiceWriteStdin(t *testing.T) {
	service := NewProcessService()
	service.DefaultTimeoutMS = 1000
	sink := NewNotificationBuffer()
	params := &ProcessSpawnParams{
		Command:       processTestStdinEchoCommand(),
		ProcessHandle: "stdin-1",
		CWD:           t.TempDir(),
		StreamStdin:   true,
	}

	if _, err := service.Spawn(context.Background(), params, sinkNotifyFunc(sink)); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("typed input"))
	if _, err := service.WriteStdin(&ProcessWriteStdinParams{ProcessHandle: "stdin-1", DeltaBase64: &encoded, CloseStdin: true}); err != nil {
		t.Fatalf("WriteStdin() error = %v", err)
	}

	notification := waitForProcessExited(t, sink, "stdin-1")
	exited := notification.Params.(*ProcessExitedNotification)
	if strings.TrimSpace(exited.Stdout) != "typed input" {
		t.Fatalf("Stdout = %q, want stdin echo", exited.Stdout)
	}
}

func TestProcessServiceSpawnStreamsOutputDelta(t *testing.T) {
	service := NewProcessService()
	service.DefaultTimeoutMS = 1000
	sink := NewNotificationBuffer()
	params := &ProcessSpawnParams{
		Command:            processTestEchoCommand("streamed"),
		ProcessHandle:      "stream-1",
		CWD:                t.TempDir(),
		StreamStdoutStderr: true,
	}

	if _, err := service.Spawn(context.Background(), params, sinkNotifyFunc(sink)); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	delta := waitForProcessOutputDelta(t, sink, "stream-1", StreamStdout)
	decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if !strings.Contains(string(decoded), "streamed") {
		t.Fatalf("delta = %q", decoded)
	}
	exited := waitForProcessExited(t, sink, "stream-1").Params.(*ProcessExitedNotification)
	if exited.Stdout != "" || exited.Stderr != "" {
		t.Fatalf("streamed exit output should be empty, got stdout=%q stderr=%q", exited.Stdout, exited.Stderr)
	}
}

func TestProcessServiceSpawnReportsBufferedOutputCapReachedLikeRust(t *testing.T) {
	service := NewProcessService()
	service.DefaultTimeoutMS = 1000
	sink := NewNotificationBuffer()
	capValue := 3
	params := &ProcessSpawnParams{
		Command:        commandExecTestOutputCommand("abcde", "12345"),
		ProcessHandle:  "capped-one-shot-1",
		CWD:            t.TempDir(),
		OutputBytesCap: &OptionalInt{Set: true, Value: &capValue},
	}

	if _, err := service.Spawn(context.Background(), params, sinkNotifyFunc(sink)); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	exited := waitForProcessExited(t, sink, "capped-one-shot-1").Params.(*ProcessExitedNotification)
	if exited.ExitCode != 0 ||
		exited.Stdout != "abc" || !exited.StdoutCapReached ||
		exited.Stderr != "123" || !exited.StderrCapReached {
		t.Fatalf("exited = %+v, want capped stdout/stderr", exited)
	}
}

func TestProcessServiceDuplicateKillAndResize(t *testing.T) {
	service := NewProcessService()
	timeoutMS := int64(10_000)
	params := &ProcessSpawnParams{
		Command:       processTestSleepCommand(5),
		ProcessHandle: "sleep-1",
		CWD:           t.TempDir(),
		TimeoutMS:     &OptionalInt64{Set: true, Value: &timeoutMS},
	}

	if _, err := service.Spawn(context.Background(), params, nil); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	if _, err := service.Spawn(context.Background(), params, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate Spawn() error = %v, want ErrInvalidRequest", err)
	} else if err.Error() != `duplicate active process handle: "sleep-1"` {
		t.Fatalf("duplicate Spawn() error = %v", err)
	}
	if _, err := service.Resize(&ProcessResizePtyParams{ProcessHandle: "sleep-1", Size: TerminalSize{Rows: 24, Cols: 80}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Resize() error = %v, want ErrInvalidRequest", err)
	} else if err.Error() != "process/resizePty is only supported for PTY processes" {
		t.Fatalf("Resize() error = %v", err)
	}
	if _, err := service.Kill(&ProcessKillParams{ProcessHandle: "sleep-1"}); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
}

func TestProcessServiceControlErrorsMatchRust(t *testing.T) {
	service := NewProcessService()
	if _, err := service.WriteStdin(&ProcessWriteStdinParams{ProcessHandle: "missing", CloseStdin: true}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("WriteStdin(missing) error = %v, want ErrInvalidRequest", err)
	} else if err.Error() != `no active process for process handle "missing"` {
		t.Fatalf("WriteStdin(missing) error = %v", err)
	}

	timeoutMS := int64(1000)
	params := &ProcessSpawnParams{
		Command:       processTestSleepCommand(1),
		ProcessHandle: "no-stdin",
		CWD:           t.TempDir(),
		TimeoutMS:     &OptionalInt64{Set: true, Value: &timeoutMS},
	}
	if _, err := service.Spawn(context.Background(), params, nil); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	if _, err := service.WriteStdin(&ProcessWriteStdinParams{ProcessHandle: "no-stdin", CloseStdin: true}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("WriteStdin(no stream) error = %v, want ErrInvalidRequest", err)
	} else if err.Error() != "stdin streaming is not enabled for this process" {
		t.Fatalf("WriteStdin(no stream) error = %v", err)
	}
	if _, err := service.Kill(&ProcessKillParams{ProcessHandle: "no-stdin"}); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
}

func TestProcessServiceSessionsAreConnectionScoped(t *testing.T) {
	service := NewProcessService()
	timeoutMS := int64(10_000)
	handle := "shared-process"
	paramsA := &ProcessSpawnParams{
		Command:       processTestStdinEchoCommand(),
		ProcessHandle: handle,
		CWD:           t.TempDir(),
		StreamStdin:   true,
		TimeoutMS:     &OptionalInt64{Set: true, Value: &timeoutMS},
	}
	paramsB := &ProcessSpawnParams{
		Command:       processTestStdinEchoCommand(),
		ProcessHandle: handle,
		CWD:           t.TempDir(),
		StreamStdin:   true,
		TimeoutMS:     &OptionalInt64{Set: true, Value: &timeoutMS},
	}

	if _, err := service.SpawnWithOptions(context.Background(), paramsA, nil, &ProcessSpawnOptions{ConnectionID: "conn-a"}); err != nil {
		t.Fatalf("SpawnWithOptions(conn-a) error = %v", err)
	}
	if _, err := service.SpawnWithOptions(context.Background(), paramsB, nil, &ProcessSpawnOptions{ConnectionID: "conn-b"}); err != nil {
		t.Fatalf("SpawnWithOptions(conn-b) error = %v", err)
	}
	waitForActiveProcessInConnection(t, service, "conn-a", handle)
	waitForActiveProcessInConnection(t, service, "conn-b", handle)
	if _, err := service.WriteStdinWithConnection("conn-a", &ProcessWriteStdinParams{ProcessHandle: handle, CloseStdin: true}); err != nil {
		t.Fatalf("WriteStdinWithConnection(conn-a) error = %v", err)
	}
	if _, err := service.WriteStdinWithConnection("conn-b", &ProcessWriteStdinParams{ProcessHandle: handle, CloseStdin: true}); err != nil {
		t.Fatalf("WriteStdinWithConnection(conn-b) error = %v", err)
	}
	waitForNoActiveProcessInConnection(t, service, "conn-a", handle)
	waitForNoActiveProcessInConnection(t, service, "conn-b", handle)
}

func TestProcessServiceConnectionClosedCancelsOnlyThatConnection(t *testing.T) {
	service := NewProcessService()
	timeoutMS := int64(10_000)
	handle := "shared-close"
	paramsA := &ProcessSpawnParams{
		Command:       processTestStdinEchoCommand(),
		ProcessHandle: handle,
		CWD:           t.TempDir(),
		StreamStdin:   true,
		TimeoutMS:     &OptionalInt64{Set: true, Value: &timeoutMS},
	}
	paramsB := &ProcessSpawnParams{
		Command:       processTestStdinEchoCommand(),
		ProcessHandle: handle,
		CWD:           t.TempDir(),
		StreamStdin:   true,
		TimeoutMS:     &OptionalInt64{Set: true, Value: &timeoutMS},
	}

	if _, err := service.SpawnWithOptions(context.Background(), paramsA, nil, &ProcessSpawnOptions{ConnectionID: "conn-a"}); err != nil {
		t.Fatalf("SpawnWithOptions(conn-a) error = %v", err)
	}
	if _, err := service.SpawnWithOptions(context.Background(), paramsB, nil, &ProcessSpawnOptions{ConnectionID: "conn-b"}); err != nil {
		t.Fatalf("SpawnWithOptions(conn-b) error = %v", err)
	}
	waitForActiveProcessInConnection(t, service, "conn-a", handle)
	waitForActiveProcessInConnection(t, service, "conn-b", handle)
	service.ConnectionClosed("conn-a")
	waitForNoActiveProcessInConnection(t, service, "conn-a", handle)
	if _, err := service.activeProcessForConnection("conn-b", handle); err != nil {
		t.Fatalf("conn-b should remain active, got %v", err)
	}
	if _, err := service.WriteStdinWithConnection("conn-b", &ProcessWriteStdinParams{ProcessHandle: handle, CloseStdin: true}); err != nil {
		t.Fatalf("WriteStdinWithConnection(conn-b) error = %v", err)
	}
	waitForNoActiveProcessInConnection(t, service, "conn-b", handle)
}

func TestRuntimeRouterProcessSpawnNotifications(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	router.SetNotificationSink(sink)

	spawn := router.Handle(requestWithParams(t, IntID(1), MethodProcessSpawn, ProcessSpawnParams{
		Command:       processTestEchoCommand("router"),
		ProcessHandle: "router-1",
		CWD:           t.TempDir(),
	}))
	if spawn.Error != nil {
		t.Fatalf("process spawn = %+v", spawn)
	}
	notification := waitForProcessExited(t, sink, "router-1")
	exited := notification.Params.(*ProcessExitedNotification)
	if strings.TrimSpace(exited.Stdout) != "router" {
		t.Fatalf("exited = %+v", exited)
	}
}

func TestRuntimeRouterProcessSpawnReturnsErrorWhenLocalEnvironmentDisabledLikeRust(t *testing.T) {
	t.Setenv(CodexExecServerURLEnvVar, "none")
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})

	spawn := router.Handle(requestWithParams(t, IntID(1), MethodProcessSpawn, ProcessSpawnParams{
		Command:       processTestEchoCommand("disabled"),
		ProcessHandle: "disabled-process",
		CWD:           t.TempDir(),
	}))
	if spawn.Error == nil {
		t.Fatalf("process/spawn response = %+v, want local environment error", spawn)
	}
	if spawn.Error.Code != JSONRPCInternalErrorCode || spawn.Error.Message != "local environment is not configured" {
		t.Fatalf("process/spawn disabled error = %+v", spawn.Error)
	}
}

func TestRuntimeRouterProcessControlInvalidRequestAndParamsCodes(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	missing := router.Handle(requestWithParams(t, IntID(1), MethodProcessWriteStdin, ProcessWriteStdinParams{
		ProcessHandle: "missing",
		CloseStdin:    true,
	}))
	if missing.Error == nil || missing.Error.Code != -32600 || missing.Error.Message != `no active process for process handle "missing"` {
		t.Fatalf("missing process response = %+v", missing)
	}

	invalidDelta := "not base64"
	write := router.Handle(requestWithParams(t, IntID(2), MethodProcessWriteStdin, ProcessWriteStdinParams{
		ProcessHandle: "missing",
		DeltaBase64:   &invalidDelta,
	}))
	if write.Error == nil || write.Error.Code != -32602 || !strings.HasPrefix(write.Error.Message, "invalid deltaBase64:") {
		t.Fatalf("invalid delta response = %+v", write)
	}
}

func waitForProcessExited(t *testing.T, sink *NotificationBuffer, handle string) *Notification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationProcessExited {
				continue
			}
			exited, ok := notification.Params.(*ProcessExitedNotification)
			if ok && exited.ProcessHandle == handle {
				return notification
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process/exited notification for %q not observed", handle)
	return nil
}

func processExitedObserved(sink *NotificationBuffer, handle string) bool {
	for _, notification := range sink.List() {
		if notification.Method != NotificationProcessExited {
			continue
		}
		exited, ok := notification.Params.(*ProcessExitedNotification)
		if ok && exited.ProcessHandle == handle {
			return true
		}
	}
	return false
}

func waitForProcessFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file %q was not created", path)
}

func waitForProcessOutputDelta(t *testing.T, sink *NotificationBuffer, handle string, stream OutputStream) *ProcessOutputDeltaNotification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationProcessOutputDelta {
				continue
			}
			delta, ok := notification.Params.(*ProcessOutputDeltaNotification)
			if ok && delta.ProcessHandle == handle && delta.Stream == stream {
				return delta
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process/outputDelta notification for %q %s not observed", handle, stream)
	return nil
}

func waitForActiveProcessInConnection(t *testing.T, service *ProcessService, connectionID string, handle string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := service.activeProcessForConnection(connectionID, handle); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %q for connection %q did not become active", handle, connectionID)
}

func waitForNoActiveProcessInConnection(t *testing.T, service *ProcessService, connectionID string, handle string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := service.activeProcessForConnection(connectionID, handle); errors.Is(err, ErrInvalidRequest) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %q for connection %q remained active", handle, connectionID)
}

func sinkNotifyFunc(sink *NotificationBuffer) func(NotificationMethod, any) {
	return func(method NotificationMethod, params any) {
		sink.Notify(NewNotification(method, params))
	}
}

func processTestEchoCommand(value string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo " + value}
	}
	return []string{"sh", "-c", "printf " + shellQuote(value)}
}

func processTestProbeReleaseCommand() []string {
	if runtime.GOOS == "windows" {
		script := `$ProgressPreference = 'SilentlyContinue'; [IO.File]::WriteAllText($env:CODEX_PROCESS_EXEC_PROBE_FILE, 'process'); while (!(Test-Path -LiteralPath $env:CODEX_PROCESS_EXEC_RELEASE_FILE)) { Start-Sleep -Milliseconds 20 }; [Console]::Out.Write('process-out'); [Console]::Error.Write('process-err')`
		return []string{"powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", powershellEncodedScript(script)}
	}
	return []string{"sh", "-c", "printf process > \"$CODEX_PROCESS_EXEC_PROBE_FILE\"; while [ ! -e \"$CODEX_PROCESS_EXEC_RELEASE_FILE\" ]; do sleep 0.05; done; printf process-out; printf process-err >&2"}
}

func processTestStdinEchoCommand() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "more"}
	}
	return []string{"sh", "-c", "cat"}
}

func processTestSleepCommand(seconds int) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", fmt.Sprintf("ping -n %d 127.0.0.1 >nul", seconds+1)}
	}
	return []string{"sh", "-c", fmt.Sprintf("sleep %d", seconds)}
}
