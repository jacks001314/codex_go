package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/tool"
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

func TestCommandExecNonStreamingWithProcessIDRespectsOutputCapLikeRust(t *testing.T) {
	service := NewCommandExecService()
	outputCap := 5
	processID := "cap-1"
	params := &CommandExecParams{
		Command:        commandExecTestOutputCommand("abcdef", "uvwxyz"),
		ProcessID:      &processID,
		OutputBytesCap: &outputCap,
	}

	response, err := service.Execute(context.Background(), params, t.TempDir())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.ExitCode != 0 || response.Stdout != "abcde" || response.Stderr != "uvwxy" {
		t.Fatalf("response = %+v, want capped stdout/stderr", response)
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

func TestCommandExecEnvOverridesMergeAndUnsetLikeRust(t *testing.T) {
	baseline := "request"
	extra := "added"
	env := commandExecEnvMap(
		[]string{"COMMAND_EXEC_BASELINE=server", "RUST_LOG=debug", "CODEX_HOME=/tmp/codex"},
		map[string]*string{
			"COMMAND_EXEC_BASELINE": &baseline,
			"COMMAND_EXEC_EXTRA":    &extra,
			"RUST_LOG":              nil,
		},
	)
	if env["COMMAND_EXEC_BASELINE"] != "request" || env["COMMAND_EXEC_EXTRA"] != "added" || env["CODEX_HOME"] != "/tmp/codex" {
		t.Fatalf("env = %#v, want merged override/addition with existing CODEX_HOME", env)
	}
	if _, ok := env["RUST_LOG"]; ok {
		t.Fatalf("env = %#v, want RUST_LOG unset", env)
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

func TestCommandExecCustomPermissionProfileResolverLikeRust(t *testing.T) {
	service := NewCommandExecService()
	cwd := t.TempDir()
	profileID := "networked"
	params := &CommandExecParams{
		Command:           commandExecTestOutputCommand("ok", ""),
		PermissionProfile: &profileID,
	}
	oldRunner := runCommandExecSandboxed
	called := false
	runCommandExecSandboxed = func(ctx context.Context, req *tool.ShellRequest) (*tool.ShellResult, error) {
		called = true
		if req.CWD != cwd {
			t.Fatalf("CWD = %q, want %q", req.CWD, cwd)
		}
		if req.PermissionProfileID != profileID {
			t.Fatalf("PermissionProfileID = %q, want %q", req.PermissionProfileID, profileID)
		}
		if req.PermissionProfile == nil || !req.PermissionProfile.AllowsNetwork() || req.PermissionProfile.Disabled {
			t.Fatalf("PermissionProfile = %#v, want enabled network sandbox profile", req.PermissionProfile)
		}
		if req.Env[network.ProxyActiveEnvKey] != "1" {
			t.Fatalf("%s = %q, want active network proxy marker", network.ProxyActiveEnvKey, req.Env[network.ProxyActiveEnvKey])
		}
		return &tool.ShellResult{ExitCode: 0, Stdout: "custom"}, nil
	}
	defer func() { runCommandExecSandboxed = oldRunner }()

	response, err := service.ExecuteWithOptions(context.Background(), params, cwd, nil, &CommandExecOptions{
		PermissionProfileResolver: func(gotProfileID string, gotCWD string) (*CommandExecPermissionProfileResolution, error) {
			if gotProfileID != profileID {
				t.Fatalf("resolver profileID = %q, want %q", gotProfileID, profileID)
			}
			if gotCWD != cwd {
				t.Fatalf("resolver cwd = %q, want %q", gotCWD, cwd)
			}
			profile := sandbox.ReadOnlyPermissionProfile()
			profile.NetworkEnabled = true
			return &CommandExecPermissionProfileResolution{ID: profileID, Profile: &profile}, nil
		},
	})
	if err != nil {
		t.Fatalf("ExecuteWithOptions() error = %v", err)
	}
	if !called {
		t.Fatalf("sandbox runner was not called")
	}
	if response.ExitCode != 0 || response.Stdout != "custom" {
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

func TestCommandExecWithoutStreamsCanBeTerminatedLikeRust(t *testing.T) {
	service := NewCommandExecService()
	processID := "sleep-1"
	params := &CommandExecParams{
		Command:   processTestSleepCommand(30),
		ProcessID: &processID,
	}

	responseCh := make(chan *CommandExecResponse, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, err := service.Execute(context.Background(), params, t.TempDir())
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- response
	}()
	waitForActiveCommandExec(t, service, processID)
	if _, err := service.Terminate(&CommandExecTerminateParams{ProcessID: processID}); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}
	select {
	case err := <-errorCh:
		t.Fatalf("Execute() error = %v", err)
	case response := <-responseCh:
		if response.ExitCode == 0 || response.Stdout != "" || response.Stderr != "" {
			t.Fatalf("terminated response = %+v, want non-zero empty output", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute() did not return after terminate")
	}
	waitForNoActiveCommandExec(t, service, processID)
}

func TestCommandExecStreamingDoesNotBufferOutputLikeRust(t *testing.T) {
	service := NewCommandExecService()
	sink := NewNotificationBuffer()
	processID := "stream-cap-1"
	outputCap := 5
	params := &CommandExecParams{
		Command:            commandExecTestOutputThenSleepCommand("abcdefghij", 30),
		ProcessID:          &processID,
		StreamStdoutStderr: true,
		OutputBytesCap:     &outputCap,
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
	delta := waitForCommandExecOutputDelta(t, sink, processID, StreamStdout)
	decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if string(decoded) != "abcde" || !delta.CapReached {
		t.Fatalf("delta decoded=%q capReached=%v, want capped stdout delta", decoded, delta.CapReached)
	}
	if _, err := service.Terminate(&CommandExecTerminateParams{ProcessID: processID}); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}
	select {
	case err := <-errorCh:
		t.Fatalf("ExecuteWithNotify() error = %v", err)
	case response := <-responseCh:
		if response.ExitCode == 0 || response.Stdout != "" || response.Stderr != "" {
			t.Fatalf("streaming final response = %+v, want non-zero empty output", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ExecuteWithNotify() did not return after terminate")
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

func TestCommandExecPipeStreamsOutputAndAcceptsWriteLikeRust(t *testing.T) {
	service := NewCommandExecService()
	sink := NewNotificationBuffer()
	processID := "pipe-1"
	params := &CommandExecParams{
		Command:            commandExecTestPipeCommand(),
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
	waitForCommandExecOutputsContain(t, sink, processID, "out-start\n", "err-start\n")

	encoded := base64.StdEncoding.EncodeToString([]byte("hello\n"))
	if _, err := service.Write(&CommandExecWriteParams{ProcessID: processID, DeltaBase64: &encoded, CloseStdin: true}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	waitForCommandExecOutputsContain(t, sink, processID, "out:hello\n", "err:hello\n")

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
	if _, err := service.TerminateWithConnection("conn-c", &CommandExecTerminateParams{ProcessID: processID}); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), `no active command/exec for process id "shared-command"`) {
		t.Fatalf("TerminateWithConnection(conn-c) error = %v, want no active connection-scoped process", err)
	}
	if _, err := service.activeCommandExecForConnection("conn-a", processID); err != nil {
		t.Fatalf("conn-a should remain active after cross-connection terminate, got %v", err)
	}
	if _, err := service.activeCommandExecForConnection("conn-b", processID); err != nil {
		t.Fatalf("conn-b should remain active after cross-connection terminate, got %v", err)
	}
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
	if write.Error == nil || write.Error.Code != -32600 || write.Error.Message != `command/exec "missing" is no longer running` {
		t.Fatalf("expected invalid command exec write, got %+v", write)
	}
	terminate := router.Handle(requestWithParams(t, IntID(3), MethodCommandExecTerminate, CommandExecTerminateParams{
		ProcessID: "missing",
	}))
	if terminate.Error == nil || terminate.Error.Code != -32600 || terminate.Error.Message != `command/exec "missing" is no longer running` {
		t.Fatalf("expected invalid command exec terminate, got %+v", terminate)
	}
	resize := router.Handle(requestWithParams(t, IntID(4), MethodCommandExecResize, CommandExecResizeParams{
		ProcessID: "missing",
		Size:      TerminalSize{Rows: 24, Cols: 80},
	}))
	if resize.Error == nil || resize.Error.Code != -32600 || resize.Error.Message != `command/exec "missing" is no longer running` {
		t.Fatalf("expected invalid command exec resize, got %+v", resize)
	}
}

func TestRuntimeRouterCommandExecResolvesCustomPermissionProfileFromConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[permissions.networked.filesystem]
":minimal" = "read"

[permissions.networked.network]
enabled = true
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	profileID := "networked"
	oldRunner := runCommandExecSandboxed
	called := false
	runCommandExecSandboxed = func(ctx context.Context, req *tool.ShellRequest) (*tool.ShellResult, error) {
		called = true
		if req.CWD != cwd {
			t.Fatalf("CWD = %q, want %q", req.CWD, cwd)
		}
		if req.PermissionProfileID != profileID {
			t.Fatalf("PermissionProfileID = %q, want %q", req.PermissionProfileID, profileID)
		}
		if req.PermissionProfile == nil || !req.PermissionProfile.AllowsNetwork() || req.PermissionProfile.Disabled {
			t.Fatalf("PermissionProfile = %#v, want custom networked sandbox profile", req.PermissionProfile)
		}
		if req.Env[network.ProxyActiveEnvKey] != "1" {
			t.Fatalf("%s = %q, want active network proxy marker", network.ProxyActiveEnvKey, req.Env[network.ProxyActiveEnvKey])
		}
		return &tool.ShellResult{ExitCode: 0, Stdout: req.PermissionProfileID}, nil
	}
	defer func() { runCommandExecSandboxed = oldRunner }()

	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:  cwd,
		Config:      config.NewConfigService(home),
		CommandExec: NewCommandExecService(),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodCommandExec, CommandExecParams{
		Command:           commandExecTestOutputCommand("ignored", ""),
		PermissionProfile: &profileID,
	}))
	if response.Error != nil {
		t.Fatalf("command exec = %+v", response)
	}
	if !called {
		t.Fatalf("sandbox runner was not called")
	}
	result := response.Result.(*CommandExecResponse)
	if result.ExitCode != 0 || result.Stdout != profileID {
		t.Fatalf("result = %+v", result)
	}
}

func TestRuntimeRouterCommandExecPermissionProfileDoesNotReuseDefaultNetworkProxyLikeRust(t *testing.T) {
	t.Setenv(network.ProxyActiveEnvKey, "1")
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
default_permissions = "networked"

[permissions.networked.filesystem]
":minimal" = "read"

[permissions.networked.network]
enabled = true
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	profileID := sandbox.BuiltInPermissionProfileReadOnly
	oldRunner := runCommandExecSandboxed
	called := false
	runCommandExecSandboxed = func(ctx context.Context, req *tool.ShellRequest) (*tool.ShellResult, error) {
		called = true
		if req.PermissionProfileID != profileID {
			t.Fatalf("PermissionProfileID = %q, want %q", req.PermissionProfileID, profileID)
		}
		if req.PermissionProfile == nil || req.PermissionProfile.AllowsNetwork() {
			t.Fatalf("PermissionProfile = %#v, want read-only profile without network", req.PermissionProfile)
		}
		if _, ok := req.Env[network.ProxyActiveEnvKey]; ok {
			t.Fatalf("%s leaked into explicit read-only command env: %#v", network.ProxyActiveEnvKey, req.Env)
		}
		return &tool.ShellResult{ExitCode: 0, Stdout: "unset"}, nil
	}
	defer func() { runCommandExecSandboxed = oldRunner }()

	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:  cwd,
		Config:      config.NewConfigService(home),
		CommandExec: NewCommandExecService(),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodCommandExec, CommandExecParams{
		Command:           commandExecTestOutputCommand("ignored", ""),
		PermissionProfile: &profileID,
	}))
	if response.Error != nil {
		t.Fatalf("command exec = %+v", response)
	}
	if !called {
		t.Fatalf("sandbox runner was not called")
	}
	result := response.Result.(*CommandExecResponse)
	if result.ExitCode != 0 || result.Stdout != "unset" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRuntimeRouterCommandExecPermissionProfileProjectRootsUseCommandCWDLikeRust(t *testing.T) {
	home := t.TempDir()
	defaultCWD := t.TempDir()
	commandDir := filepath.Join(defaultCWD, "command-cwd")
	if err := os.Mkdir(commandDir, 0o700); err != nil {
		t.Fatalf("Mkdir(commandDir) error = %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[permissions.command-cwd.filesystem]
":root" = "read"
":workspace_roots" = "write"
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	profileID := "command-cwd"
	relativeCWD := "command-cwd"
	oldRunner := runCommandExecSandboxed
	called := false
	runCommandExecSandboxed = func(ctx context.Context, req *tool.ShellRequest) (*tool.ShellResult, error) {
		called = true
		if req.CWD != commandDir {
			t.Fatalf("CWD = %q, want %q", req.CWD, commandDir)
		}
		if req.PermissionProfileID != profileID {
			t.Fatalf("PermissionProfileID = %q, want %q", req.PermissionProfileID, profileID)
		}
		if access := commandExecPermissionProfileJSONPathAccess(t, req.PermissionProfileJSON, commandDir); access != string(sandbox.FileSystemAccessWrite) {
			t.Fatalf("command cwd access = %q, want write in %s", access, req.PermissionProfileJSON)
		}
		if access := commandExecPermissionProfileJSONPathAccess(t, req.PermissionProfileJSON, defaultCWD); access == string(sandbox.FileSystemAccessWrite) {
			t.Fatalf("default cwd unexpectedly had write access in %s", req.PermissionProfileJSON)
		}
		return &tool.ShellResult{ExitCode: 0, Stdout: "cwd"}, nil
	}
	defer func() { runCommandExecSandboxed = oldRunner }()

	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:  defaultCWD,
		Config:      config.NewConfigService(home),
		CommandExec: NewCommandExecService(),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodCommandExec, CommandExecParams{
		Command:           commandExecTestOutputCommand("ignored", ""),
		CWD:               &relativeCWD,
		PermissionProfile: &profileID,
	}))
	if response.Error != nil {
		t.Fatalf("command exec = %+v", response)
	}
	if !called {
		t.Fatalf("sandbox runner was not called")
	}
	result := response.Result.(*CommandExecResponse)
	if result.ExitCode != 0 || result.Stdout != "cwd" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRuntimeRouterCommandExecReturnsErrorWhenLocalEnvironmentDisabledLikeRust(t *testing.T) {
	t.Setenv(CodexExecServerURLEnvVar, "none")
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})

	response := router.Handle(requestWithParams(t, IntID(1), MethodCommandExec, CommandExecParams{
		Command: commandExecTestOutputCommand("disabled", ""),
	}))
	if response.Error == nil {
		t.Fatalf("command/exec response = %+v, want local environment error", response)
	}
	if response.Error.Code != JSONRPCInternalErrorCode || response.Error.Message != "local environment is not configured" {
		t.Fatalf("command/exec disabled error = %+v", response.Error)
	}
}

func TestRuntimeRouterCommandExecRejectsSandboxPolicyWithPermissionProfileLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	profile := sandbox.BuiltInPermissionProfileDangerFullAccess

	response := router.Handle(requestWithParams(t, IntID(1), MethodCommandExec, CommandExecParams{
		Command:           commandExecTestOutputCommand("ok", ""),
		SandboxPolicy:     sandbox.NewReadOnlyPolicy(),
		PermissionProfile: &profile,
	}))
	if response.Error == nil {
		t.Fatalf("command/exec response = %+v, want sandbox/profile conflict", response)
	}
	if response.Error.Code != JSONRPCInvalidRequestErrorCode ||
		response.Error.Message != "`permissionProfile` cannot be combined with `sandboxPolicy`" {
		t.Fatalf("command/exec sandbox/profile error = %+v", response.Error)
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

	timeoutMS := int64(1000)
	timeoutConflict := router.Handle(requestWithParams(t, IntID(3), MethodCommandExec, CommandExecParams{
		Command:        commandExecTestOutputCommand("ok", ""),
		ProcessID:      &processID,
		TimeoutMS:      &timeoutMS,
		DisableTimeout: true,
	}))
	if timeoutConflict.Error == nil || timeoutConflict.Error.Code != -32602 || timeoutConflict.Error.Message != "command/exec cannot set both timeoutMs and disableTimeout" {
		t.Fatalf("timeout conflict response = %+v", timeoutConflict)
	}

	negativeTimeoutMS := int64(-1)
	negativeTimeout := router.Handle(requestWithParams(t, IntID(4), MethodCommandExec, CommandExecParams{
		Command:   commandExecTestOutputCommand("ok", ""),
		ProcessID: &processID,
		TimeoutMS: &negativeTimeoutMS,
	}))
	if negativeTimeout.Error == nil || negativeTimeout.Error.Code != -32602 || negativeTimeout.Error.Message != "command/exec timeoutMs must be non-negative, got -1" {
		t.Fatalf("negative timeout response = %+v", negativeTimeout)
	}

	invalidDelta := "not base64"
	write := router.Handle(requestWithParams(t, IntID(5), MethodCommandExecWrite, CommandExecWriteParams{
		ProcessID:   processID,
		DeltaBase64: &invalidDelta,
	}))
	if write.Error == nil || write.Error.Code != -32602 || !strings.HasPrefix(write.Error.Message, "invalid deltaBase64:") {
		t.Fatalf("invalid delta response = %+v", write)
	}
}

func commandExecPermissionProfileJSONPathAccess(t *testing.T, raw string, path string) string {
	t.Helper()
	var wire struct {
		FileSystem struct {
			Entries []struct {
				Path struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"path"`
				Access string `json:"access"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatalf("Unmarshal permission profile JSON error = %v: %s", err, raw)
	}
	cleaned := filepath.Clean(path)
	for _, entry := range wire.FileSystem.Entries {
		if entry.Path.Type == "path" && filepath.Clean(entry.Path.Path) == cleaned {
			return entry.Access
		}
	}
	return ""
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

func waitForCommandExecOutputsContain(t *testing.T, sink *NotificationBuffer, processID string, stdoutWant string, stderrWant string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var stdout strings.Builder
		var stderr strings.Builder
		for _, notification := range sink.List() {
			if notification.Method != NotificationCommandExecOutputDelta {
				continue
			}
			delta, ok := notification.Params.(*CommandExecOutputDeltaNotification)
			if !ok || delta.ProcessID != processID {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
			if err != nil {
				t.Fatalf("DecodeString() error = %v", err)
			}
			switch delta.Stream {
			case StreamStdout:
				stdout.Write(decoded)
			case StreamStderr:
				stderr.Write(decoded)
			}
		}
		if strings.Contains(stdout.String(), stdoutWant) && strings.Contains(stderr.String(), stderrWant) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command/exec output for %q did not contain stdout %q and stderr %q", processID, stdoutWant, stderrWant)
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

func commandExecTestOutputThenSleepCommand(stdout string, seconds int) []string {
	if runtime.GOOS == "windows" {
		script := "[Console]::Out.Write(" + powerShellSingleQuote(stdout) + "); Start-Sleep -Seconds " + fmt.Sprint(seconds)
		return []string{"powershell", "-NoProfile", "-EncodedCommand", powershellEncodedScript(script)}
	}
	return []string{"sh", "-c", "printf " + shellQuote(stdout) + "; sleep " + fmt.Sprint(seconds)}
}

func commandExecTestStdinEchoCommand() []string {
	if runtime.GOOS == "windows" {
		script := `$stdinStream = [Console]::OpenStandardInput(); $stdoutStream = [Console]::OpenStandardOutput(); $stdinStream.CopyTo($stdoutStream); $stdoutStream.Flush()`
		return []string{"powershell", "-NoProfile", "-Command", script}
	}
	return []string{"sh", "-c", "cat"}
}

func commandExecTestPipeCommand() []string {
	if runtime.GOOS == "windows" {
		script := `[Console]::Out.Write("out-start` + "`n" + `"); [Console]::Error.Write("err-start` + "`n" + `"); $line = [Console]::In.ReadLine(); [Console]::Out.Write("out:$line` + "`n" + `"); [Console]::Error.Write("err:$line` + "`n" + `")`
		return []string{"powershell", "-NoProfile", "-EncodedCommand", powershellEncodedScript(script)}
	}
	return []string{"sh", "-c", "printf 'out-start\\n'; printf 'err-start\\n' >&2; IFS= read line; printf 'out:%s\\n' \"$line\"; printf 'err:%s\\n' \"$line\" >&2"}
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
