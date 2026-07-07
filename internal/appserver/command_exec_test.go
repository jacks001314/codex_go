package appserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/internal/sandbox"
	"codex_go/internal/tool"
)

func TestCommandExecExecuteBuffered(t *testing.T) {
	service := NewCommandExecService()
	cwd := t.TempDir()
	params := &CommandExecParams{
		Command: commandExecTestOutputCommand("stdout", "stderr"),
		CWD:     &cwd,
	}

	response, err := service.Execute(context.Background(), params, "")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.ExitCode != 0 || response.Stdout != "stdout" || response.Stderr != "stderr" {
		t.Fatalf("response = %+v", response)
	}
}

func TestCommandExecExecuteNonZeroExit(t *testing.T) {
	service := NewCommandExecService()
	params := &CommandExecParams{Command: commandExecTestExitCommand(7)}

	response, err := service.Execute(context.Background(), params, t.TempDir())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", response.ExitCode)
	}
}

func TestCommandExecOutputCap(t *testing.T) {
	service := NewCommandExecService()
	outputCap := 4
	params := &CommandExecParams{
		Command:        commandExecTestOutputCommand("123456789", ""),
		OutputBytesCap: &outputCap,
	}

	response, err := service.Execute(context.Background(), params, t.TempDir())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.Stdout != "1234" {
		t.Fatalf("Stdout = %q, want capped output", response.Stdout)
	}

	params.DisableOutputCap = true
	params.OutputBytesCap = nil
	response, err = service.Execute(context.Background(), params, t.TempDir())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.Stdout != "123456789" {
		t.Fatalf("Stdout = %q, want full output", response.Stdout)
	}
}

func TestCommandExecEnvOverrides(t *testing.T) {
	service := NewCommandExecService()
	value := "from-test"
	params := &CommandExecParams{
		Command: commandExecTestEnvCommand("CODEX_GO_COMMAND_EXEC_ENV_TEST"),
		Env: map[string]*string{
			"CODEX_GO_COMMAND_EXEC_ENV_TEST": &value,
		},
	}

	response, err := service.Execute(context.Background(), params, t.TempDir())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.TrimSpace(response.Stdout) != "from-test" {
		t.Fatalf("Stdout = %q, want env override", response.Stdout)
	}
}

func TestCommandExecSandboxDangerFullAccessRunsAndInjectsProfile(t *testing.T) {
	service := NewCommandExecService()
	params := &CommandExecParams{
		Command:       commandExecTestEnvCommand("CODEX_PERMISSION_PROFILE"),
		SandboxPolicy: sandbox.NewDangerFullAccessPolicy(),
	}

	response, err := service.Execute(context.Background(), params, t.TempDir())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.TrimSpace(response.Stdout) != sandbox.BuiltInPermissionProfileDangerFullAccess {
		t.Fatalf("Stdout = %q, want injected permission profile", response.Stdout)
	}
}

func TestCommandExecFullAccessPermissionProfileRuns(t *testing.T) {
	service := NewCommandExecService()
	profile := ":danger-full-access"
	params := &CommandExecParams{
		Command:           commandExecTestEnvCommand("CODEX_PERMISSION_PROFILE"),
		PermissionProfile: &profile,
	}

	response, err := service.Execute(context.Background(), params, t.TempDir())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.TrimSpace(response.Stdout) != profile {
		t.Fatalf("Stdout = %q, want injected permission profile", response.Stdout)
	}
}

func TestCommandExecSandboxPolicyRequiringRunnerUsesSandboxRunner(t *testing.T) {
	service := NewCommandExecService()
	params := &CommandExecParams{
		Command:       commandExecTestOutputCommand("ok", ""),
		SandboxPolicy: sandbox.NewReadOnlyPolicy(),
	}
	oldRunner := runCommandExecSandboxed
	called := false
	runCommandExecSandboxed = func(ctx context.Context, req *tool.ShellRequest) (*tool.ShellResult, error) {
		called = true
		if req.PermissionProfile == nil || req.PermissionProfile.Disabled {
			t.Fatalf("PermissionProfile = %#v, want sandboxed profile", req.PermissionProfile)
		}
		if req.PermissionProfileID != sandbox.BuiltInPermissionProfileReadOnly {
			t.Fatalf("PermissionProfileID = %q", req.PermissionProfileID)
		}
		return &tool.ShellResult{ExitCode: 0, Stdout: "sandboxed"}, nil
	}
	defer func() { runCommandExecSandboxed = oldRunner }()

	response, err := service.Execute(context.Background(), params, t.TempDir())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatalf("sandbox runner was not called")
	}
	if response.ExitCode != 0 || response.Stdout != "sandboxed" {
		t.Fatalf("response = %+v", response)
	}
}

func TestCommandExecSandboxStreamingRemainsExplicit(t *testing.T) {
	service := NewCommandExecService()
	processID := "sandbox-stream"
	params := &CommandExecParams{
		Command:            commandExecTestOutputCommand("ok", ""),
		ProcessID:          &processID,
		StreamStdoutStderr: true,
		SandboxPolicy:      sandbox.NewReadOnlyPolicy(),
	}

	_, err := service.Execute(context.Background(), params, t.TempDir())
	if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "sandbox is not supported for tty or streaming") {
		t.Fatalf("Execute() error = %v, want explicit streaming sandbox rejection", err)
	}
}

func TestCommandExecStreamingSessionOperations(t *testing.T) {
	service := NewCommandExecService()
	sink := NewNotificationBuffer()
	processID := "proc-1"
	params := &CommandExecParams{
		Command:            commandExecTestOutputCommand("hi", ""),
		ProcessID:          &processID,
		StreamStdoutStderr: true,
	}

	response, err := service.ExecuteWithNotify(context.Background(), params, t.TempDir(), sinkNotifyFunc(sink))
	if err != nil {
		t.Fatalf("ExecuteWithNotify() error = %v", err)
	}
	if response.ExitCode != 0 || response.Stdout != "" || response.Stderr != "" {
		t.Fatalf("streaming response = %+v", response)
	}
	delta := waitForCommandExecOutputDelta(t, sink, processID, StreamStdout)
	decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if string(decoded) != "hi" {
		t.Fatalf("delta = %q", decoded)
	}
	waitForNoActiveCommandExec(t, service, processID)

	_, err = service.Write(&CommandExecWriteParams{ProcessID: processID, CloseStdin: true})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Write() error = %v, want ErrInvalidRequest", err)
	}
	_, err = service.Resize(&CommandExecResizeParams{ProcessID: processID, Size: TerminalSize{Rows: 24, Cols: 80}})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Resize() error = %v, want ErrInvalidRequest", err)
	}
}

func TestCommandExecStreamingStdin(t *testing.T) {
	service := NewCommandExecService()
	sink := NewNotificationBuffer()
	processID := "stdin-1"
	params := &CommandExecParams{
		Command:            commandExecTestStdinEchoCommand(),
		ProcessID:          &processID,
		StreamStdin:        true,
		StreamStdoutStderr: true,
	}

	responseCh := make(chan *CommandExecResponse, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, err := service.ExecuteWithNotify(context.Background(), params, t.TempDir(), sinkNotifyFunc(sink))
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- response
	}()
	waitForActiveCommandExec(t, service, processID)
	encoded := base64.StdEncoding.EncodeToString([]byte("typed"))
	if _, err := service.Write(&CommandExecWriteParams{ProcessID: processID, DeltaBase64: &encoded, CloseStdin: true}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	delta := waitForCommandExecOutputDelta(t, sink, processID, StreamStdout)
	decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if string(decoded) != "typed" {
		t.Fatalf("delta = %q", decoded)
	}
	select {
	case err := <-errorCh:
		t.Fatalf("ExecuteWithNotify() error = %v", err)
	case response := <-responseCh:
		if response.ExitCode != 0 || response.Stdout != "" || response.Stderr != "" {
			t.Fatalf("streaming final response = %+v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteWithNotify() did not return final response")
	}
}

func TestCommandExecWriteEmitsTerminalInteraction(t *testing.T) {
	service := NewCommandExecService()
	sink := NewNotificationBuffer()
	processID := "terminal-interaction-1"
	threadID := "thread-1"
	turnID := "turn-1"
	itemID := "item-1"
	params := &CommandExecParams{
		Command:            commandExecTestStdinEchoCommand(),
		ProcessID:          &processID,
		ThreadID:           &threadID,
		TurnID:             &turnID,
		ItemID:             &itemID,
		StreamStdin:        true,
		StreamStdoutStderr: true,
	}

	responseCh := make(chan *CommandExecResponse, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, err := service.ExecuteWithNotify(context.Background(), params, t.TempDir(), sinkNotifyFunc(sink))
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- response
	}()
	waitForActiveCommandExec(t, service, processID)
	encoded := base64.StdEncoding.EncodeToString([]byte("hello\n"))
	if _, err := service.Write(&CommandExecWriteParams{ProcessID: processID, DeltaBase64: &encoded, CloseStdin: true}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	interaction := waitForTerminalInteraction(t, sink, threadID, turnID, itemID, processID)
	if interaction.Stdin != "hello\n" {
		t.Fatalf("terminal interaction stdin = %q, want hello newline", interaction.Stdin)
	}
	select {
	case err := <-errorCh:
		t.Fatalf("ExecuteWithNotify() error = %v", err)
	case response := <-responseCh:
		if response.ExitCode != 0 || response.Stdout != "" || response.Stderr != "" {
			t.Fatalf("streaming final response = %+v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteWithNotify() did not return final response")
	}
}

func TestCommandExecStreamStdinBuffersFinalOutputWhenNotStreamingStdout(t *testing.T) {
	service := NewCommandExecService()
	processID := "stdin-buffered-1"
	params := &CommandExecParams{
		Command:     commandExecTestStdinEchoCommand(),
		ProcessID:   &processID,
		StreamStdin: true,
	}

	responseCh := make(chan *CommandExecResponse, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, err := service.ExecuteWithNotify(context.Background(), params, t.TempDir(), nil)
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- response
	}()
	waitForActiveCommandExec(t, service, processID)
	encoded := base64.StdEncoding.EncodeToString([]byte("typed-buffered"))
	if _, err := service.Write(&CommandExecWriteParams{ProcessID: processID, DeltaBase64: &encoded, CloseStdin: true}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	select {
	case err := <-errorCh:
		t.Fatalf("ExecuteWithNotify() error = %v", err)
	case response := <-responseCh:
		if response.ExitCode != 0 || response.Stdout != "typed-buffered" || response.Stderr != "" {
			t.Fatalf("buffered final response = %+v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteWithNotify() did not return final response")
	}
}

func TestCommandExecSessionsAreConnectionScoped(t *testing.T) {
	service := NewCommandExecService()
	timeoutMS := int64(10_000)
	processID := "shared-command"
	paramsA := &CommandExecParams{
		Command:     commandExecTestStdinEchoCommand(),
		ProcessID:   &processID,
		StreamStdin: true,
		TimeoutMS:   &timeoutMS,
	}
	paramsB := &CommandExecParams{
		Command:     commandExecTestStdinEchoCommand(),
		ProcessID:   &processID,
		StreamStdin: true,
		TimeoutMS:   &timeoutMS,
	}
	doneA := make(chan *CommandExecResponse, 1)
	doneB := make(chan *CommandExecResponse, 1)
	errA := make(chan error, 1)
	errB := make(chan error, 1)

	go func() {
		response, err := service.ExecuteWithOptions(context.Background(), paramsA, t.TempDir(), nil, &CommandExecOptions{ConnectionID: "conn-a"})
		if err != nil {
			errA <- err
			return
		}
		doneA <- response
	}()
	go func() {
		response, err := service.ExecuteWithOptions(context.Background(), paramsB, t.TempDir(), nil, &CommandExecOptions{ConnectionID: "conn-b"})
		if err != nil {
			errB <- err
			return
		}
		doneB <- response
	}()

	waitForActiveCommandExecInConnection(t, service, "conn-a", processID)
	waitForActiveCommandExecInConnection(t, service, "conn-b", processID)
	if _, err := service.WriteWithConnection("conn-a", &CommandExecWriteParams{ProcessID: processID, CloseStdin: true}); err != nil {
		t.Fatalf("WriteWithConnection(conn-a) error = %v", err)
	}
	if _, err := service.WriteWithConnection("conn-b", &CommandExecWriteParams{ProcessID: processID, CloseStdin: true}); err != nil {
		t.Fatalf("WriteWithConnection(conn-b) error = %v", err)
	}

	select {
	case err := <-errA:
		t.Fatalf("conn-a ExecuteWithOptions() error = %v", err)
	case response := <-doneA:
		if response.ExitCode != 0 {
			t.Fatalf("conn-a response = %+v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("conn-a command did not exit")
	}
	select {
	case err := <-errB:
		t.Fatalf("conn-b ExecuteWithOptions() error = %v", err)
	case response := <-doneB:
		if response.ExitCode != 0 {
			t.Fatalf("conn-b response = %+v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("conn-b command did not exit")
	}
}

func TestCommandExecConnectionClosedCancelsOnlyThatConnection(t *testing.T) {
	service := NewCommandExecService()
	timeoutMS := int64(10_000)
	processID := "shared-close"
	paramsA := &CommandExecParams{
		Command:     commandExecTestStdinEchoCommand(),
		ProcessID:   &processID,
		StreamStdin: true,
		TimeoutMS:   &timeoutMS,
	}
	paramsB := &CommandExecParams{
		Command:     commandExecTestStdinEchoCommand(),
		ProcessID:   &processID,
		StreamStdin: true,
		TimeoutMS:   &timeoutMS,
	}
	doneA := make(chan *CommandExecResponse, 1)
	doneB := make(chan *CommandExecResponse, 1)
	errA := make(chan error, 1)
	errB := make(chan error, 1)

	go func() {
		response, err := service.ExecuteWithOptions(context.Background(), paramsA, t.TempDir(), nil, &CommandExecOptions{ConnectionID: "conn-a"})
		if err != nil {
			errA <- err
			return
		}
		doneA <- response
	}()
	go func() {
		response, err := service.ExecuteWithOptions(context.Background(), paramsB, t.TempDir(), nil, &CommandExecOptions{ConnectionID: "conn-b"})
		if err != nil {
			errB <- err
			return
		}
		doneB <- response
	}()

	waitForActiveCommandExecInConnection(t, service, "conn-a", processID)
	waitForActiveCommandExecInConnection(t, service, "conn-b", processID)
	service.ConnectionClosed("conn-a")
	waitForNoActiveCommandExecInConnection(t, service, "conn-a", processID)
	if _, err := service.activeCommandExecForConnection("conn-b", processID); err != nil {
		t.Fatalf("conn-b should remain active, got %v", err)
	}
	if _, err := service.WriteWithConnection("conn-b", &CommandExecWriteParams{ProcessID: processID, CloseStdin: true}); err != nil {
		t.Fatalf("WriteWithConnection(conn-b) error = %v", err)
	}

	select {
	case <-doneA:
	case <-errA:
	case <-time.After(2 * time.Second):
		t.Fatal("conn-a command did not finish after connection close")
	}
	select {
	case err := <-errB:
		t.Fatalf("conn-b ExecuteWithOptions() error = %v", err)
	case response := <-doneB:
		if response.ExitCode != 0 {
			t.Fatalf("conn-b response = %+v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("conn-b command did not exit")
	}
}

func TestRuntimeRouterCommandExec(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	response := router.Handle(requestWithParams(t, IntID(1), MethodCommandExec, CommandExecParams{
		Command: commandExecTestOutputCommand("ok", ""),
	}))
	if response.Error != nil {
		t.Fatalf("command exec = %+v", response)
	}
	result := response.Result.(*CommandExecResponse)
	if result.Stdout != "ok" || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}

	write := router.Handle(requestWithParams(t, IntID(2), MethodCommandExecWrite, CommandExecWriteParams{
		ProcessID:  "missing",
		CloseStdin: true,
	}))
	if write.Error == nil || write.Error.Code != -32600 || write.Error.Message != `no active command/exec for process id "missing"` {
		t.Fatalf("expected invalid command exec write, got %+v", write)
	}
}

func TestRuntimeRouterCommandExecInvalidRequestAndParamsCodes(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	missingProcessID := router.Handle(requestWithParams(t, IntID(1), MethodCommandExec, CommandExecParams{
		Command:     commandExecTestOutputCommand("ok", ""),
		StreamStdin: true,
	}))
	if missingProcessID.Error == nil || missingProcessID.Error.Code != -32600 || missingProcessID.Error.Message != "command/exec tty or streaming requires a client-supplied processId" {
		t.Fatalf("missing process id response = %+v", missingProcessID)
	}

	processID := "proc-invalid"
	outputCap := 1
	outputConflict := router.Handle(requestWithParams(t, IntID(2), MethodCommandExec, CommandExecParams{
		Command:          commandExecTestOutputCommand("ok", ""),
		ProcessID:        &processID,
		OutputBytesCap:   &outputCap,
		DisableOutputCap: true,
	}))
	if outputConflict.Error == nil || outputConflict.Error.Code != -32602 || outputConflict.Error.Message != "command/exec cannot set both outputBytesCap and disableOutputCap" {
		t.Fatalf("output conflict response = %+v", outputConflict)
	}

	invalidDelta := "not base64"
	write := router.Handle(requestWithParams(t, IntID(3), MethodCommandExecWrite, CommandExecWriteParams{
		ProcessID:   processID,
		DeltaBase64: &invalidDelta,
	}))
	if write.Error == nil || write.Error.Code != -32602 || !strings.HasPrefix(write.Error.Message, "invalid deltaBase64:") {
		t.Fatalf("invalid delta response = %+v", write)
	}
}

func waitForCommandExecOutputDelta(t *testing.T, sink *NotificationBuffer, processID string, stream OutputStream) *CommandExecOutputDeltaNotification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationCommandExecOutputDelta {
				continue
			}
			delta, ok := notification.Params.(*CommandExecOutputDeltaNotification)
			if ok && delta.ProcessID == processID && delta.Stream == stream {
				return delta
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command/exec/outputDelta notification for %q %s not observed", processID, stream)
	return nil
}

func waitForTerminalInteraction(t *testing.T, sink *NotificationBuffer, threadID string, turnID string, itemID string, processID string) *TerminalInteractionNotification {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationTerminalInteraction {
				continue
			}
			interaction, ok := notification.Params.(*TerminalInteractionNotification)
			if ok && interaction.ThreadID == threadID && interaction.TurnID == turnID && interaction.ItemID == itemID && interaction.ProcessID == processID {
				return interaction
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal interaction notification for %q/%q/%q process %q not observed", threadID, turnID, itemID, processID)
	return nil
}

func waitForNoActiveCommandExec(t *testing.T, service *CommandExecService, processID string) {
	t.Helper()
	waitForNoActiveCommandExecInConnection(t, service, defaultRequestConnectionID, processID)
}

func waitForNoActiveCommandExecInConnection(t *testing.T, service *CommandExecService, connectionID string, processID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := service.activeCommandExecForConnection(connectionID, processID); errors.Is(err, ErrInvalidRequest) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command/exec %q remained active", processID)
}

func waitForActiveCommandExec(t *testing.T, service *CommandExecService, processID string) {
	t.Helper()
	waitForActiveCommandExecInConnection(t, service, defaultRequestConnectionID, processID)
}

func waitForActiveCommandExecInConnection(t *testing.T, service *CommandExecService, connectionID string, processID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := service.activeCommandExecForConnection(connectionID, processID); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command/exec %q did not become active", processID)
}

func waitForActiveCommandExecPTY(t *testing.T, service *CommandExecService, processID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		active, err := service.activeCommandExecForConnection(defaultRequestConnectionID, processID)
		if err == nil {
			service.mu.Lock()
			ptySession := active.pty
			service.mu.Unlock()
			if ptySession != nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command/exec %q did not become active with PTY", processID)
}

func commandExecTestOutputCommand(stdout string, stderr string) []string {
	if runtime.GOOS == "windows" {
		parts := []string{}
		if stdout != "" {
			parts = append(parts, "[Console]::Out.Write("+powerShellSingleQuote(stdout)+")")
		}
		if stderr != "" {
			parts = append(parts, "[Console]::Error.Write("+powerShellSingleQuote(stderr)+")")
		}
		return []string{"powershell", "-NoProfile", "-EncodedCommand", powershellEncodedScript(strings.Join(parts, "; "))}
	}
	script := ""
	if stdout != "" {
		script += "printf " + shellQuote(stdout)
	}
	if stderr != "" {
		if script != "" {
			script += "; "
		}
		script += "printf " + shellQuote(stderr) + " 1>&2"
	}
	return []string{"sh", "-c", script}
}

func commandExecTestStdinEchoCommand() []string {
	if runtime.GOOS == "windows" {
		script := `$stdinStream = [Console]::OpenStandardInput(); $stdoutStream = [Console]::OpenStandardOutput(); $stdinStream.CopyTo($stdoutStream); $stdoutStream.Flush()`
		return []string{"powershell", "-NoProfile", "-Command", script}
	}
	return []string{"sh", "-c", "cat"}
}

func commandExecTestExitCommand(code int) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", fmt.Sprintf("exit /b %d", code)}
	}
	return []string{"sh", "-c", fmt.Sprintf("exit %d", code)}
}

func commandExecTestEnvCommand(name string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo %" + name + "%"}
	}
	return []string{"sh", "-c", "printf \"$" + name + "\""}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
