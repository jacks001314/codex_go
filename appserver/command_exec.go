package appserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/envutil"
	codexexec "codex_go/exec"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/tool"
)

const (
	defaultCommandExecTimeoutMS      int64 = 10_000
	defaultCommandExecOutputBytesCap       = 1024 * 1024
	commandExecTimeoutExitCode       int32 = 124
)

type CommandExecService struct {
	DefaultTimeoutMS      int64
	DefaultOutputBytesCap int

	mu     sync.Mutex
	active map[commandExecSessionKey]*managedCommandExec
}

type managedCommandExec struct {
	mu                  sync.Mutex
	connectionID        string
	processID           string
	cmd                 *osexec.Cmd
	process             *ptyProcess
	cancel              context.CancelFunc
	stdin               io.WriteCloser
	pty                 *ptyHandle
	outputActivity      chan struct{}
	outputDone          chan struct{}
	done                chan struct{}
	response            chan commandExecResult
	notify              func(NotificationMethod, any)
	terminalInteraction *CommandExecTerminalInteractionContext
}

func (a *managedCommandExec) setPTY(process *ptyProcess, stdin io.WriteCloser, pty *ptyHandle, outputActivity chan struct{}, outputDone chan struct{}) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.process = process
	a.stdin = stdin
	a.pty = pty
	a.outputActivity = outputActivity
	a.outputDone = outputDone
	a.mu.Unlock()
}

func (a *managedCommandExec) commandProcessHandle() *os.Process {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd == nil {
		return nil
	}
	return a.cmd.Process
}

func (a *managedCommandExec) snapshotForWait(cmd *osexec.Cmd) (*ptyProcess, *ptyHandle, chan struct{}, chan struct{}, context.CancelFunc, chan commandExecResult, chan struct{}) {
	if a == nil {
		return nil, nil, nil, nil, nil, nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.process, a.pty, a.outputActivity, a.outputDone, a.cancel, a.response, a.done
}

func (a *managedCommandExec) stdinWriter() io.WriteCloser {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stdin
}

func (a *managedCommandExec) clearStdinIfCurrent(stdin io.WriteCloser) {
	if a == nil || stdin == nil {
		return
	}
	a.mu.Lock()
	if a.stdin == stdin {
		a.stdin = nil
	}
	a.mu.Unlock()
}

func (a *managedCommandExec) cancelCommand() {
	if a == nil {
		return
	}
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *managedCommandExec) ptySession() *ptyHandle {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pty
}

func (a *managedCommandExec) startCommand(cmd *osexec.Cmd) error {
	if a == nil || cmd == nil {
		return errors.New("command/exec active command is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return cmd.Start()
}

type CommandExecOptions struct {
	ConnectionID              string
	PermissionProfileResolver CommandExecPermissionProfileResolver
	// ApplyPatchPreserveLineEndings carries the apply_patch line-ending
	// rollout state (Rust c9c6c0daa9) into command/exec child processes.
	ApplyPatchPreserveLineEndings bool
}

var runCommandExecSandboxed = func(ctx context.Context, req *tool.ShellRequest) (*tool.ShellResult, error) {
	return tool.NewLocalShellRunner().Run(ctx, req)
}

type CommandExecPermissionProfileResolver func(profileID string, cwd string) (*CommandExecPermissionProfileResolution, error)

type CommandExecPermissionProfileResolution struct {
	ID          string
	Profile     *sandbox.PermissionProfile
	ProfileJSON string
}

type CommandExecTerminalInteractionContext struct {
	ThreadID string
	TurnID   string
	ItemID   string
}

func cloneCommandExecTerminalInteractionContext(value *CommandExecTerminalInteractionContext) *CommandExecTerminalInteractionContext {
	if value == nil {
		return nil
	}
	return &CommandExecTerminalInteractionContext{
		ThreadID: strings.TrimSpace(value.ThreadID),
		TurnID:   strings.TrimSpace(value.TurnID),
		ItemID:   strings.TrimSpace(value.ItemID),
	}
}

type commandExecSandboxResolution struct {
	PermissionProfileID   *string
	PermissionProfile     *sandbox.PermissionProfile
	PermissionProfileJSON string
}

type commandExecSessionKey struct {
	connectionID string
	processID    string
}

func applyPatchPreserveLineEndings(options *CommandExecOptions) bool {
	return options != nil && options.ApplyPatchPreserveLineEndings
}

func NewCommandExecService() *CommandExecService {
	return &CommandExecService{
		DefaultTimeoutMS:      defaultCommandExecTimeoutMS,
		DefaultOutputBytesCap: defaultCommandExecOutputBytesCap,
		active:                map[commandExecSessionKey]*managedCommandExec{},
	}
}

func (s *CommandExecService) Execute(ctx context.Context, params *CommandExecParams, defaultCWD string) (*CommandExecResponse, error) {
	return s.ExecuteWithNotify(ctx, params, defaultCWD, nil)
}

func (s *CommandExecService) ExecuteWithNotify(ctx context.Context, params *CommandExecParams, defaultCWD string, notify func(NotificationMethod, any)) (*CommandExecResponse, error) {
	return s.ExecuteWithOptions(ctx, params, defaultCWD, notify, nil)
}

func (s *CommandExecService) ExecuteWithOptions(ctx context.Context, params *CommandExecParams, defaultCWD string, notify func(NotificationMethod, any), options *CommandExecOptions) (*CommandExecResponse, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: command exec service is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := params.Validate(defaultCWD); err != nil {
		return nil, err
	}
	cwd := commandExecCWD(params, defaultCWD)
	resolution, err := resolveCommandExecSandbox(params, cwd, commandExecPermissionProfileResolver(options))
	if err != nil {
		return nil, err
	}
	connectionID := normalizeCommandExecConnectionID(options)

	execCtx, cancel := s.contextForCommand(ctx, params)
	if (params.StreamStdin || params.StreamStdoutStderr) && cancel == nil {
		execCtx, cancel = context.WithCancel(execCtx)
	}
	if cancel != nil && !params.StreamStdin && !params.StreamStdoutStderr {
		defer cancel()
	}

	if commandExecRequiresPlatformSandbox(resolution) {
		return s.executeSandboxedBuffered(execCtx, params, defaultCWD, resolution, options)
	}

	cmd := osexec.CommandContext(execCtx, params.Command[0], params.Command[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	envMap := commandExecEnvMap(os.Environ(), params.Env)
	codexexec.InjectPermissionProfile(envMap, resolution.PermissionProfileID)
	commandExecInjectNetworkProxyEnv(envMap, resolution)
	// Rust c4513cb982: model-reachable exec children must not inherit Codex
	// launch context (OPENAI_FEDERATION_RULE_ID / OPENAI_IDENTITY_TOKEN_FILE).
	envutil.ScrubMap(envMap)
	// Rust c9c6c0daa9: carry the apply_patch line-ending rollout state into
	// command/exec children, authoritative over client-provided values.
	envMap = envutil.InjectApplyPatchEnv(envMap, applyPatchPreserveLineEndings(options))
	cmd.Env = commandExecEnvList(envMap)

	outputCap := s.outputBytesCap(params)
	stdout := newCommandExecOutputBuffer(outputCap)
	stderr := newCommandExecOutputBuffer(outputCap)
	if params.TTY {
		return s.executePTYWithConnection(execCtx, cancel, connectionID, params, cmd, stdout, stderr, notify)
	}
	prepareCommandExecProcess(cmd)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if params.StreamStdoutStderr {
		cmd.Stdout = &commandExecOutputNotifier{writer: stdout, notify: notify, processID: *params.ProcessID, stream: StreamStdout}
		cmd.Stderr = &commandExecOutputNotifier{writer: stderr, notify: notify, processID: *params.ProcessID, stream: StreamStderr}
	}
	if params.StreamStdin {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			if cancel != nil {
				cancel()
			}
			return nil, err
		}
		active, err := s.registerCommandExec(connectionID, *params.ProcessID, cmd, cancel, stdin, notify, params.terminalInteractionContext())
		if err != nil {
			if cancel != nil {
				cancel()
			}
			return nil, err
		}
		if err := active.startCommand(cmd); err != nil {
			s.removeCommandExec(connectionID, *params.ProcessID)
			if cancel != nil {
				cancel()
			}
			return nil, fmt.Errorf("failed to start command/exec: %w", err)
		}
		go s.waitCommandExec(execCtx, connectionID, *params.ProcessID, cmd)
		return s.waitCommandExecResponse(execCtx, active, stdout, stderr, params.StreamStdoutStderr)
	}
	if params.StreamStdoutStderr {
		active, err := s.registerCommandExec(connectionID, *params.ProcessID, cmd, cancel, nil, notify, params.terminalInteractionContext())
		if err != nil {
			if cancel != nil {
				cancel()
			}
			return nil, err
		}
		if err := active.startCommand(cmd); err != nil {
			s.removeCommandExec(connectionID, *params.ProcessID)
			if cancel != nil {
				cancel()
			}
			return nil, fmt.Errorf("failed to start command/exec: %w", err)
		}
		go s.waitCommandExec(execCtx, connectionID, *params.ProcessID, cmd)
		return s.waitCommandExecResponse(execCtx, active, stdout, stderr, params.StreamStdoutStderr)
	}
	if params.ProcessID != nil && strings.TrimSpace(*params.ProcessID) != "" {
		active, err := s.registerCommandExec(connectionID, *params.ProcessID, cmd, cancel, nil, notify, params.terminalInteractionContext())
		if err != nil {
			if cancel != nil {
				cancel()
			}
			return nil, err
		}
		if err := active.startCommand(cmd); err != nil {
			s.removeCommandExec(connectionID, *params.ProcessID)
			if cancel != nil {
				cancel()
			}
			return nil, fmt.Errorf("failed to start command/exec: %w", err)
		}
		go s.waitCommandExec(execCtx, connectionID, *params.ProcessID, cmd)
		return s.waitCommandExecResponse(execCtx, active, stdout, stderr, false)
	}

	err = cmd.Run()
	exitCode, exitErr := commandExecExitCode(execCtx, err)
	if exitErr != nil {
		return nil, exitErr
	}
	return &CommandExecResponse{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

func (s *CommandExecService) executeSandboxedBuffered(ctx context.Context, params *CommandExecParams, defaultCWD string, resolution *commandExecSandboxResolution, options *CommandExecOptions) (*CommandExecResponse, error) {
	if params == nil {
		return nil, errors.New("command/exec params are required")
	}
	if params.TTY || params.StreamStdin || params.StreamStdoutStderr {
		return nil, jsonRPCInvalidRequest("command/exec sandbox is not supported for tty or streaming commands yet")
	}
	envMap := commandExecEnvMap(os.Environ(), params.Env)
	commandExecInjectNetworkProxyEnv(envMap, resolution)
	envMap = envutil.InjectApplyPatchEnv(envMap, applyPatchPreserveLineEndings(options))
	result, err := runCommandExecSandboxed(ctx, &tool.ShellRequest{
		Command:               append([]string(nil), params.Command...),
		CWD:                   commandExecCWD(params, defaultCWD),
		Env:                   envMap,
		PermissionProfileID:   sandboxResolutionProfileID(resolution),
		PermissionProfile:     sandboxResolutionProfile(resolution),
		PermissionProfileJSON: sandboxResolutionProfileJSON(resolution),
	})
	if err != nil {
		if errors.Is(err, sandbox.ErrPlatformSandboxUnsupported) {
			return nil, jsonRPCInvalidRequest(err.Error())
		}
		return nil, err
	}
	if result == nil {
		return nil, errors.New("command/exec sandbox returned nil result")
	}
	outputCap := s.outputBytesCap(params)
	stdout := newCommandExecOutputBuffer(outputCap)
	stderr := newCommandExecOutputBuffer(outputCap)
	_, _ = stdout.Write([]byte(result.Stdout))
	_, _ = stderr.Write([]byte(result.Stderr))
	return &CommandExecResponse{
		ExitCode: int32(result.ExitCode),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

func normalizeCommandExecConnectionID(options *CommandExecOptions) string {
	if options == nil {
		return defaultRequestConnectionID
	}
	return normalizeConnectionID(options.ConnectionID)
}

func (s *CommandExecService) Write(params *CommandExecWriteParams) (*CommandExecWriteResponse, error) {
	return s.WriteWithConnection(defaultRequestConnectionID, params)
}

func (s *CommandExecService) WriteWithConnection(connectionID string, params *CommandExecWriteParams) (*CommandExecWriteResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	active, err := s.activeCommandExecForConnection(connectionID, params.ProcessID)
	if err != nil {
		return nil, err
	}
	stdin := active.stdinWriter()
	if stdin == nil {
		return nil, jsonRPCInvalidRequest("stdin streaming is not enabled for this command/exec")
	}
	if params.DeltaBase64 != nil {
		delta, err := base64.StdEncoding.DecodeString(*params.DeltaBase64)
		if err != nil {
			return nil, invalidFSRequest(fmt.Sprintf("invalid deltaBase64: %v", err))
		}
		if len(delta) > 0 {
			n, err := stdin.Write(delta)
			if err != nil {
				return nil, fmt.Errorf("%w: stdin is already closed", ErrInvalidRequest)
			}
			if n > 0 {
				s.notifyTerminalInteraction(active, delta[:n])
			}
		}
	}
	if params.CloseStdin {
		if err := stdin.Close(); err != nil {
			return nil, fmt.Errorf("%w: stdin is already closed", ErrInvalidRequest)
		}
		active.clearStdinIfCurrent(stdin)
	}
	return &CommandExecWriteResponse{}, nil
}

func (s *CommandExecService) Terminate(params *CommandExecTerminateParams) (*CommandExecTerminateResponse, error) {
	return s.TerminateWithConnection(defaultRequestConnectionID, params)
}

func (s *CommandExecService) TerminateWithConnection(connectionID string, params *CommandExecTerminateParams) (*CommandExecTerminateResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	active, err := s.activeCommandExecForConnection(connectionID, params.ProcessID)
	if err != nil {
		return nil, err
	}
	active.cancelCommand()
	terminateCommandExecProcess(active)
	return &CommandExecTerminateResponse{}, nil
}

func (s *CommandExecService) Resize(params *CommandExecResizeParams) (*CommandExecResizeResponse, error) {
	return s.ResizeWithConnection(defaultRequestConnectionID, params)
}

func (s *CommandExecService) ResizeWithConnection(connectionID string, params *CommandExecResizeParams) (*CommandExecResizeResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	active, err := s.activeCommandExecForConnection(connectionID, params.ProcessID)
	if err != nil {
		return nil, err
	}
	ptySession := active.ptySession()
	if ptySession == nil {
		return nil, jsonRPCInvalidRequest("command/exec resize is only supported for PTY processes")
	}
	if err := ptySession.Resize(&params.Size); err != nil {
		return nil, err
	}
	return &CommandExecResizeResponse{}, nil
}

func (s *CommandExecService) contextForCommand(ctx context.Context, params *CommandExecParams) (context.Context, context.CancelFunc) {
	if params.DisableTimeout {
		return ctx, nil
	}
	timeoutMS := s.DefaultTimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = defaultCommandExecTimeoutMS
	}
	if params.TimeoutMS != nil {
		timeoutMS = *params.TimeoutMS
	}
	return context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
}

func (s *CommandExecService) outputBytesCap(params *CommandExecParams) *int {
	if params.DisableOutputCap {
		return nil
	}
	outputCap := s.DefaultOutputBytesCap
	if outputCap <= 0 {
		outputCap = defaultCommandExecOutputBytesCap
	}
	if params.OutputBytesCap != nil {
		outputCap = *params.OutputBytesCap
	}
	return &outputCap
}

func commandExecCWD(params *CommandExecParams, defaultCWD string) string {
	if params != nil && params.CWD != nil {
		cwd := strings.TrimSpace(*params.CWD)
		if cwd != "" && !filepath.IsAbs(cwd) && strings.TrimSpace(defaultCWD) != "" {
			return filepath.Join(defaultCWD, cwd)
		}
		return cwd
	}
	return strings.TrimSpace(defaultCWD)
}

func commandExecExitCode(ctx context.Context, err error) (int32, error) {
	if err == nil {
		return 0, nil
	}
	if ctx != nil && ctx.Err() == context.DeadlineExceeded {
		return commandExecTimeoutExitCode, nil
	}
	if exitErr, ok := err.(interface{ ExitCode() int }); ok {
		return int32(exitErr.ExitCode()), nil
	}
	if exitErr, ok := err.(*osexec.ExitError); ok {
		return int32(exitErr.ExitCode()), nil
	}
	return 0, fmt.Errorf("exec failed: %w", err)
}

func noActiveCommandExecError(processID string) error {
	return jsonRPCInvalidRequest(fmt.Sprintf("command/exec %q is no longer running", strings.TrimSpace(processID)))
}

func noActiveCommandExecForProcessIDError(processID string) error {
	return jsonRPCInvalidRequest(fmt.Sprintf("no active command/exec for process id %q", strings.TrimSpace(processID)))
}

func (s *CommandExecService) registerCommandExec(connectionID string, processID string, cmd *osexec.Cmd, cancel context.CancelFunc, stdin io.WriteCloser, notify func(NotificationMethod, any), terminalInteraction *CommandExecTerminalInteractionContext) (*managedCommandExec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		s.active = map[commandExecSessionKey]*managedCommandExec{}
	}
	key := commandExecKey(connectionID, processID)
	if s.active[key] != nil {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("duplicate active command/exec process id: %q", processID))
	}
	active := &managedCommandExec{
		connectionID:        key.connectionID,
		processID:           processID,
		cmd:                 cmd,
		cancel:              cancel,
		stdin:               stdin,
		done:                make(chan struct{}),
		response:            make(chan commandExecResult, 1),
		notify:              notify,
		terminalInteraction: cloneCommandExecTerminalInteractionContext(terminalInteraction),
	}
	s.active[key] = active
	return active, nil
}

func (s *CommandExecService) executePTY(execCtx context.Context, cancel context.CancelFunc, params *CommandExecParams, cmd *osexec.Cmd, stdout *commandExecOutputBuffer, stderr *commandExecOutputBuffer, notify func(NotificationMethod, any)) (*CommandExecResponse, error) {
	return s.executePTYWithConnection(execCtx, cancel, defaultRequestConnectionID, params, cmd, stdout, stderr, notify)
}

func (s *CommandExecService) executePTYWithConnection(execCtx context.Context, cancel context.CancelFunc, connectionID string, params *CommandExecParams, cmd *osexec.Cmd, stdout *commandExecOutputBuffer, stderr *commandExecOutputBuffer, notify func(NotificationMethod, any)) (*CommandExecResponse, error) {
	if params == nil || params.ProcessID == nil {
		return nil, jsonRPCInvalidRequest("command/exec tty or streaming requires a client-supplied processId")
	}
	if cancel == nil {
		execCtx, cancel = context.WithCancel(execCtx)
	}
	active, err := s.registerCommandExec(connectionID, *params.ProcessID, cmd, cancel, nil, notify, params.terminalInteractionContext())
	if err != nil {
		cancel()
		return nil, err
	}

	active.mu.Lock()
	process, ptySession, err := startPTYCommand(execCtx, cmd, params.Size)
	active.mu.Unlock()
	if err != nil {
		s.removeCommandExec(connectionID, *params.ProcessID)
		cancel()
		return nil, fmt.Errorf("failed to start command/exec: %w", err)
	}
	outputActivity := make(chan struct{}, 1)
	outputDone := make(chan struct{})
	active.setPTY(process, ptySession, ptySession, outputActivity, outputDone)

	go readPTYOutput(ptySession, stdout, outputActivity, outputDone, func(data []byte) {
		if len(data) == 0 || notify == nil {
			return
		}
		notify(NotificationCommandExecOutputDelta, &CommandExecOutputDeltaNotification{
			ProcessID:   *params.ProcessID,
			Stream:      StreamStdout,
			DeltaBase64: base64.StdEncoding.EncodeToString(data),
			CapReached:  stdout.CapReached(),
		})
	})
	go s.waitCommandExec(execCtx, connectionID, *params.ProcessID, cmd)
	return s.waitCommandExecResponse(execCtx, active, stdout, stderr, true)
}

func (s *CommandExecService) activeCommandExec(processID string) (*managedCommandExec, error) {
	return s.activeCommandExecForConnection(defaultRequestConnectionID, processID)
}

func (s *CommandExecService) notifyTerminalInteraction(active *managedCommandExec, delta []byte) {
	if active == nil || len(delta) == 0 || active.notify == nil || active.terminalInteraction == nil {
		return
	}
	context := active.terminalInteraction
	if context.ThreadID == "" || context.TurnID == "" || context.ItemID == "" {
		return
	}
	active.notify(NotificationTerminalInteraction, &TerminalInteractionNotification{
		ThreadID:  context.ThreadID,
		TurnID:    context.TurnID,
		ItemID:    context.ItemID,
		ProcessID: active.processID,
		Stdin:     string(delta),
	})
}

func (s *CommandExecService) activeCommandExecForConnection(connectionID string, processID string) (*managedCommandExec, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: command exec service is nil", ErrInvalidRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.active[commandExecKey(connectionID, processID)]
	if active == nil {
		normalizedProcessID := strings.TrimSpace(processID)
		for key := range s.active {
			if key.processID == normalizedProcessID {
				return nil, noActiveCommandExecForProcessIDError(processID)
			}
		}
		return nil, noActiveCommandExecError(processID)
	}
	return active, nil
}

func (s *CommandExecService) removeCommandExec(connectionID string, processID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, commandExecKey(connectionID, processID))
}

func (s *CommandExecService) waitCommandExec(ctx context.Context, connectionID string, processID string, cmd *osexec.Cmd) {
	active, _ := s.activeCommandExecForConnection(connectionID, processID)
	err := error(nil)
	process, ptySession, outputActivity, outputDone, cancel, response, done := active.snapshotForWait(cmd)
	if process != nil {
		err = process.Wait()
	} else {
		err = cmd.Wait()
	}
	exitCode, waitErr := commandExecExitCode(ctx, err)
	if ptySession != nil {
		waitForPTYOutputAfterExit(outputActivity, outputDone)
		_ = ptySession.ClosePTY()
		waitForPTYOutputDone(ptySession, outputDone)
		_ = ptySession.Cleanup()
	}
	if cancel != nil {
		cancel()
	}
	if response != nil {
		response <- commandExecResult{exitCode: exitCode, err: waitErr}
		close(response)
	}
	s.removeCommandExec(connectionID, processID)
	if done != nil {
		close(done)
	}
}

func (s *CommandExecService) ConnectionClosed(connectionID string) {
	if s == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	var sessions []*managedCommandExec
	s.mu.Lock()
	for key, active := range s.active {
		if key.connectionID == connectionID && active != nil {
			sessions = append(sessions, active)
		}
	}
	s.mu.Unlock()
	for _, active := range sessions {
		active.cancelCommand()
		terminateCommandExecProcess(active)
	}
}

func commandExecKey(connectionID string, processID string) commandExecSessionKey {
	return commandExecSessionKey{
		connectionID: normalizeConnectionID(connectionID),
		processID:    strings.TrimSpace(processID),
	}
}

func (s *CommandExecService) waitCommandExecResponse(ctx context.Context, active *managedCommandExec, stdout *commandExecOutputBuffer, stderr *commandExecOutputBuffer, outputStreamed bool) (*CommandExecResponse, error) {
	if active == nil {
		return nil, fmt.Errorf("%w: command/exec active session is nil", ErrInvalidRequest)
	}
	select {
	case result, ok := <-active.response:
		if !ok {
			return nil, noActiveCommandExecError(active.processID)
		}
		if result.err != nil {
			return nil, result.err
		}
		return commandExecFinalResponse(result.exitCode, stdout, stderr, outputStreamed), nil
	case <-ctx.Done():
		active.cancelCommand()
		result, ok := <-active.response
		if !ok {
			if ctx.Err() == context.DeadlineExceeded {
				return commandExecFinalResponse(commandExecTimeoutExitCode, stdout, stderr, outputStreamed), nil
			}
			return nil, ctx.Err()
		}
		if result.err != nil {
			return nil, result.err
		}
		return commandExecFinalResponse(result.exitCode, stdout, stderr, outputStreamed), nil
	}
}

func commandExecFinalResponse(exitCode int32, stdout *commandExecOutputBuffer, stderr *commandExecOutputBuffer, outputStreamed bool) *CommandExecResponse {
	response := &CommandExecResponse{ExitCode: exitCode}
	if !outputStreamed {
		response.Stdout = stdout.String()
		response.Stderr = stderr.String()
	}
	return response
}

func resolveCommandExecSandbox(params *CommandExecParams, cwd string, resolver CommandExecPermissionProfileResolver) (*commandExecSandboxResolution, error) {
	if params == nil {
		return &commandExecSandboxResolution{}, nil
	}
	if params.PermissionProfile != nil {
		profileID := strings.TrimSpace(*params.PermissionProfile)
		if profileID == "" {
			return nil, jsonRPCInvalidRequest("command/exec permissionProfile must not be empty")
		}
		resolved, err := resolver(profileID, cwd)
		if err != nil {
			return nil, err
		}
		if resolved == nil || resolved.Profile == nil {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("command/exec permissionProfile %q did not resolve to a profile", profileID))
		}
		resolvedID := strings.TrimSpace(resolved.ID)
		if resolvedID == "" {
			resolvedID = profileID
		}
		return &commandExecSandboxResolution{
			PermissionProfileID:   &resolvedID,
			PermissionProfile:     resolved.Profile,
			PermissionProfileJSON: strings.TrimSpace(resolved.ProfileJSON),
		}, nil
	}
	if params.SandboxPolicy == nil {
		return &commandExecSandboxResolution{}, nil
	}
	profile := &sandbox.PermissionProfile{SandboxPolicy: params.SandboxPolicy}
	switch params.SandboxPolicy.Kind {
	case sandbox.SandboxDangerFullAccess:
		profile = &sandbox.PermissionProfile{Disabled: true, SandboxPolicy: params.SandboxPolicy, NetworkEnabled: true}
		profileID := sandbox.BuiltInPermissionProfileDangerFullAccess
		return &commandExecSandboxResolution{PermissionProfileID: &profileID, PermissionProfile: profile}, nil
	default:
		profileID := commandExecSandboxPolicyProfileID(params.SandboxPolicy)
		return &commandExecSandboxResolution{PermissionProfileID: &profileID, PermissionProfile: profile}, nil
	}
}

func commandExecSandboxPolicyProfileID(policy *sandbox.SandboxPolicy) string {
	if policy == nil {
		return ""
	}
	switch policy.Kind {
	case sandbox.SandboxReadOnly:
		return sandbox.BuiltInPermissionProfileReadOnly
	case sandbox.SandboxWorkspaceWrite, "":
		return sandbox.BuiltInPermissionProfileWorkspace
	case sandbox.SandboxDangerFullAccess:
		return sandbox.BuiltInPermissionProfileDangerFullAccess
	default:
		return string(policy.Kind)
	}
}

func commandExecRequiresPlatformSandbox(resolution *commandExecSandboxResolution) bool {
	profile := sandboxResolutionProfile(resolution)
	return profile != nil && !profile.Disabled
}

func sandboxResolutionProfileID(resolution *commandExecSandboxResolution) string {
	if resolution == nil || resolution.PermissionProfileID == nil {
		return ""
	}
	return strings.TrimSpace(*resolution.PermissionProfileID)
}

func sandboxResolutionProfile(resolution *commandExecSandboxResolution) *sandbox.PermissionProfile {
	if resolution == nil {
		return nil
	}
	return resolution.PermissionProfile
}

func sandboxResolutionProfileJSON(resolution *commandExecSandboxResolution) string {
	if resolution == nil {
		return ""
	}
	return strings.TrimSpace(resolution.PermissionProfileJSON)
}

func commandExecPermissionProfileResolver(options *CommandExecOptions) CommandExecPermissionProfileResolver {
	if options != nil && options.PermissionProfileResolver != nil {
		return options.PermissionProfileResolver
	}
	return commandExecBuiltinPermissionProfileResolver
}

func commandExecBuiltinPermissionProfileResolver(profileID string, _ string) (*CommandExecPermissionProfileResolution, error) {
	profile, _, err := sandbox.ResolvePermissionProfile(profileID)
	if err != nil {
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	return &CommandExecPermissionProfileResolution{ID: strings.TrimSpace(profileID), Profile: profile}, nil
}

type commandExecResult struct {
	exitCode int32
	err      error
}

type commandExecOutputNotifier struct {
	writer    *commandExecOutputBuffer
	notify    func(NotificationMethod, any)
	processID string
	stream    OutputStream
}

func (w *commandExecOutputNotifier) Write(p []byte) (int, error) {
	if w == nil || w.writer == nil {
		return len(p), nil
	}
	n, accepted, capReached, err := w.writer.WriteAndAccepted(p)
	if len(accepted) > 0 && w.notify != nil {
		w.notify(NotificationCommandExecOutputDelta, &CommandExecOutputDeltaNotification{
			ProcessID:   w.processID,
			Stream:      w.stream,
			DeltaBase64: base64.StdEncoding.EncodeToString(accepted),
			CapReached:  capReached,
		})
	}
	return n, err
}

func commandExecEnv(base []string, overrides map[string]*string) []string {
	return commandExecEnvList(commandExecEnvMap(base, overrides))
}

func commandExecEnvMap(base []string, overrides map[string]*string) map[string]string {
	env := map[string]string{}
	names := map[string]string{}
	for _, item := range base {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		normalized := commandExecEnvKey(key)
		env[normalized] = value
		names[normalized] = key
	}
	for key, value := range overrides {
		normalized := commandExecEnvKey(key)
		if value == nil {
			delete(env, normalized)
			delete(names, normalized)
			continue
		}
		env[normalized] = *value
		names[normalized] = key
	}
	out := make(map[string]string, len(env))
	for normalized, value := range env {
		name := names[normalized]
		if name == "" {
			name = normalized
		}
		out[name] = value
	}
	return out
}

func commandExecInjectNetworkProxyEnv(env map[string]string, resolution *commandExecSandboxResolution) {
	if env == nil {
		return
	}
	commandExecRemoveEnvKey(env, network.ProxyActiveEnvKey)
	profile := sandboxResolutionProfile(resolution)
	if profile != nil && !profile.Disabled && profile.AllowsNetwork() {
		env[network.ProxyActiveEnvKey] = "1"
	}
}

func commandExecRemoveEnvKey(env map[string]string, key string) {
	if runtime.GOOS == "windows" {
		for existing := range env {
			if strings.EqualFold(existing, key) {
				delete(env, existing)
			}
		}
		return
	}
	delete(env, key)
}

func commandExecEnvList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func commandExecEnvKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

type commandExecOutputBuffer struct {
	limit      *int
	mu         sync.Mutex
	data       bytes.Buffer
	capReached bool
}

func newCommandExecOutputBuffer(limit *int) *commandExecOutputBuffer {
	return &commandExecOutputBuffer{limit: limit}
}

func (b *commandExecOutputBuffer) Write(p []byte) (int, error) {
	n, _, _, err := b.WriteAndAccepted(p)
	return n, err
}

func (b *commandExecOutputBuffer) WriteAndAccepted(p []byte) (int, []byte, bool, error) {
	if b == nil {
		return len(p), nil, false, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	before := b.data.Len()
	originalLen := len(p)
	if b.limit == nil {
		_, _ = b.data.Write(p)
		return originalLen, append([]byte(nil), b.data.Bytes()[before:]...), b.capReached, nil
	}
	remaining := *b.limit - b.data.Len()
	if remaining <= 0 {
		if originalLen > 0 {
			b.capReached = true
		}
		return originalLen, nil, b.capReached, nil
	}
	if len(p) > remaining {
		b.capReached = true
		p = p[:remaining]
	}
	if len(p) > 0 {
		_, _ = b.data.Write(p)
	}
	return originalLen, append([]byte(nil), b.data.Bytes()[before:]...), b.capReached, nil
}

func (b *commandExecOutputBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func (b *commandExecOutputBuffer) Len() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Len()
}

func (b *commandExecOutputBuffer) BytesFrom(offset int) []byte {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset >= b.data.Len() {
		return nil
	}
	if offset < 0 {
		offset = 0
	}
	data := b.data.Bytes()
	out := make([]byte, len(data[offset:]))
	copy(out, data[offset:])
	return out
}

func (b *commandExecOutputBuffer) CapReached() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capReached
}

var _ io.Writer = (*commandExecOutputBuffer)(nil)
