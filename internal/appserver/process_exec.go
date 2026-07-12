package appserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"time"
)

type ProcessService struct {
	DefaultTimeoutMS      int64
	DefaultOutputBytesCap int

	mu        sync.Mutex
	processes map[processSessionKey]*managedProcess
}

type managedProcess struct {
	connectionID   string
	handle         string
	cmd            *osexec.Cmd
	process        *ptyProcess
	cancel         context.CancelFunc
	stdin          io.WriteCloser
	pty            *ptyHandle
	stdout         *commandExecOutputBuffer
	stderr         *commandExecOutputBuffer
	streamOutput   bool
	outputActivity chan struct{}
	outputDone     chan struct{}
	done           chan struct{}
	exitCode       int32
}

type ProcessSpawnOptions struct {
	ConnectionID string
}

type processSessionKey struct {
	connectionID string
	handle       string
}

func NewProcessService() *ProcessService {
	return &ProcessService{
		DefaultTimeoutMS:      defaultCommandExecTimeoutMS,
		DefaultOutputBytesCap: defaultCommandExecOutputBytesCap,
		processes:             map[processSessionKey]*managedProcess{},
	}
}

func (s *ProcessService) Spawn(ctx context.Context, params *ProcessSpawnParams, notify func(NotificationMethod, any)) (*ProcessSpawnResponse, error) {
	return s.SpawnWithOptions(ctx, params, notify, nil)
}

func (s *ProcessService) SpawnWithOptions(ctx context.Context, params *ProcessSpawnParams, notify func(NotificationMethod, any), options *ProcessSpawnOptions) (*ProcessSpawnResponse, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: process service is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	connectionID := normalizeProcessConnectionID(options)
	key := processKey(connectionID, params.ProcessHandle)
	s.mu.Lock()
	if s.processes == nil {
		s.processes = map[processSessionKey]*managedProcess{}
	}
	if _, exists := s.processes[key]; exists {
		s.mu.Unlock()
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("duplicate active process handle: %q", params.ProcessHandle))
	}
	s.mu.Unlock()

	execCtx, cancel := s.contextForProcess(ctx, params)
	cmd := osexec.CommandContext(execCtx, params.Command[0], params.Command[1:]...)
	cmd.Dir = params.CWD
	cmd.Env = commandExecEnv(os.Environ(), params.Env)

	stdout := newCommandExecOutputBuffer(s.outputBytesCap(params))
	stderr := newCommandExecOutputBuffer(s.outputBytesCap(params))
	if params.TTY {
		return s.spawnPTYWithConnection(execCtx, cancel, connectionID, cmd, params, stdout, stderr, notify)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if params.StreamStdoutStderr {
		cmd.Stdout = &processOutputNotifier{writer: stdout, notify: notify, handle: params.ProcessHandle, stream: StreamStdout}
		cmd.Stderr = &processOutputNotifier{writer: stderr, notify: notify, handle: params.ProcessHandle, stream: StreamStderr}
	}

	var stdin io.WriteCloser
	var err error
	if params.StreamStdin {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, err
		}
	}

	process := &managedProcess{
		connectionID: key.connectionID,
		handle:       params.ProcessHandle,
		cmd:          cmd,
		cancel:       cancel,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		streamOutput: params.StreamStdoutStderr,
		done:         make(chan struct{}),
	}

	s.mu.Lock()
	if _, exists := s.processes[key]; exists {
		s.mu.Unlock()
		cancel()
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("duplicate active process handle: %q", params.ProcessHandle))
	}
	s.processes[key] = process
	s.mu.Unlock()

	if err := cmd.Start(); err != nil {
		s.removeProcess(connectionID, params.ProcessHandle)
		cancel()
		return nil, fmt.Errorf("failed to spawn process: %w", err)
	}

	go s.waitProcess(execCtx, process, notify)
	return &ProcessSpawnResponse{}, nil
}

func normalizeProcessConnectionID(options *ProcessSpawnOptions) string {
	if options == nil {
		return defaultRequestConnectionID
	}
	return normalizeConnectionID(options.ConnectionID)
}

type processOutputNotifier struct {
	writer *commandExecOutputBuffer
	notify func(NotificationMethod, any)
	handle string
	stream OutputStream
}

func (w *processOutputNotifier) Write(p []byte) (int, error) {
	if w == nil || w.writer == nil {
		return len(p), nil
	}
	before := w.writer.Len()
	n, err := w.writer.Write(p)
	accepted := w.writer.BytesFrom(before)
	if len(accepted) > 0 && w.notify != nil {
		w.notify(NotificationProcessOutputDelta, &ProcessOutputDeltaNotification{
			ProcessHandle: w.handle,
			Stream:        w.stream,
			DeltaBase64:   base64.StdEncoding.EncodeToString(accepted),
			CapReached:    w.writer.CapReached(),
		})
	}
	return n, err
}

func (s *ProcessService) WriteStdin(params *ProcessWriteStdinParams) (*ProcessWriteStdinResponse, error) {
	return s.WriteStdinWithConnection(defaultRequestConnectionID, params)
}

func (s *ProcessService) WriteStdinWithConnection(connectionID string, params *ProcessWriteStdinParams) (*ProcessWriteStdinResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	process, err := s.activeProcessForConnection(connectionID, params.ProcessHandle)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	stdin := process.stdin
	s.mu.Unlock()
	if stdin == nil {
		return nil, jsonRPCInvalidRequest("stdin streaming is not enabled for this process")
	}
	if params.DeltaBase64 != nil {
		delta, err := base64.StdEncoding.DecodeString(*params.DeltaBase64)
		if err != nil {
			return nil, invalidFSRequest(fmt.Sprintf("invalid deltaBase64: %v", err))
		}
		if len(delta) > 0 {
			if _, err := stdin.Write(delta); err != nil {
				return nil, fmt.Errorf("%w: stdin is already closed", ErrInvalidRequest)
			}
		}
	}
	if params.CloseStdin {
		if err := stdin.Close(); err != nil {
			return nil, fmt.Errorf("%w: stdin is already closed", ErrInvalidRequest)
		}
		s.mu.Lock()
		if process.stdin == stdin {
			process.stdin = nil
		}
		s.mu.Unlock()
	}
	return &ProcessWriteStdinResponse{}, nil
}

func (s *ProcessService) Kill(params *ProcessKillParams) (*ProcessKillResponse, error) {
	return s.KillWithConnection(defaultRequestConnectionID, params)
}

func (s *ProcessService) KillWithConnection(connectionID string, params *ProcessKillParams) (*ProcessKillResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	process, err := s.activeProcessForConnection(connectionID, params.ProcessHandle)
	if err != nil {
		return nil, err
	}
	process.cancel()
	return &ProcessKillResponse{}, nil
}

func (s *ProcessService) Resize(params *ProcessResizePtyParams) (*ProcessResizePtyResponse, error) {
	return s.ResizeWithConnection(defaultRequestConnectionID, params)
}

func (s *ProcessService) ResizeWithConnection(connectionID string, params *ProcessResizePtyParams) (*ProcessResizePtyResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	process, err := s.activeProcessForConnection(connectionID, params.ProcessHandle)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	ptySession := process.pty
	s.mu.Unlock()
	if ptySession == nil {
		return nil, jsonRPCInvalidRequest("process/resizePty is only supported for PTY processes")
	}
	if err := ptySession.Resize(&params.Size); err != nil {
		return nil, err
	}
	return &ProcessResizePtyResponse{}, nil
}

func (s *ProcessService) contextForProcess(ctx context.Context, params *ProcessSpawnParams) (context.Context, context.CancelFunc) {
	if params.TimeoutMS != nil && params.TimeoutMS.Set && params.TimeoutMS.Value == nil {
		return context.WithCancel(ctx)
	}
	timeoutMS := s.DefaultTimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = defaultCommandExecTimeoutMS
	}
	if params.TimeoutMS != nil && params.TimeoutMS.Value != nil {
		timeoutMS = *params.TimeoutMS.Value
	}
	return context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
}

func (s *ProcessService) outputBytesCap(params *ProcessSpawnParams) *int {
	if params.OutputBytesCap != nil && params.OutputBytesCap.Set && params.OutputBytesCap.Value == nil {
		return nil
	}
	outputCap := s.DefaultOutputBytesCap
	if outputCap <= 0 {
		outputCap = defaultCommandExecOutputBytesCap
	}
	if params.OutputBytesCap != nil && params.OutputBytesCap.Value != nil {
		outputCap = *params.OutputBytesCap.Value
	}
	return &outputCap
}

func (s *ProcessService) waitProcess(ctx context.Context, process *managedProcess, notify func(NotificationMethod, any)) {
	err := error(nil)
	if process.process != nil {
		err = process.process.Wait()
	} else {
		err = process.cmd.Wait()
	}
	process.exitCode, _ = commandExecExitCode(ctx, err)
	if process.pty != nil {
		waitForPTYOutputAfterExit(process.outputActivity, process.outputDone)
		_ = process.pty.ClosePTY()
		waitForPTYOutputDone(process.pty, process.outputDone)
		_ = process.pty.Cleanup()
	}
	if process.cancel != nil {
		process.cancel()
	}
	s.removeProcess(process.connectionID, process.handle)
	close(process.done)

	if notify != nil {
		stdoutText := process.stdout.String()
		stderrText := process.stderr.String()
		if process.streamOutput {
			stdoutText = ""
			stderrText = ""
		}
		notify(NotificationProcessExited, &ProcessExitedNotification{
			ProcessHandle:    process.handle,
			ExitCode:         process.exitCode,
			Stdout:           stdoutText,
			StdoutCapReached: process.stdout.CapReached(),
			Stderr:           stderrText,
			StderrCapReached: process.stderr.CapReached(),
		})
	}
}

func (s *ProcessService) activeProcess(handle string) (*managedProcess, error) {
	return s.activeProcessForConnection(defaultRequestConnectionID, handle)
}

func (s *ProcessService) activeProcessForConnection(connectionID string, handle string) (*managedProcess, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: process service is nil", ErrInvalidRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	process := s.processes[processKey(connectionID, handle)]
	if process == nil {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("no active process for process handle %q", handle))
	}
	return process, nil
}

func (s *ProcessService) spawnPTY(ctx context.Context, cancel context.CancelFunc, cmd *osexec.Cmd, params *ProcessSpawnParams, stdout *commandExecOutputBuffer, stderr *commandExecOutputBuffer, notify func(NotificationMethod, any)) (*ProcessSpawnResponse, error) {
	return s.spawnPTYWithConnection(ctx, cancel, defaultRequestConnectionID, cmd, params, stdout, stderr, notify)
}

func (s *ProcessService) spawnPTYWithConnection(ctx context.Context, cancel context.CancelFunc, connectionID string, cmd *osexec.Cmd, params *ProcessSpawnParams, stdout *commandExecOutputBuffer, stderr *commandExecOutputBuffer, notify func(NotificationMethod, any)) (*ProcessSpawnResponse, error) {
	key := processKey(connectionID, params.ProcessHandle)
	process := &managedProcess{
		connectionID:   key.connectionID,
		handle:         params.ProcessHandle,
		cmd:            cmd,
		cancel:         cancel,
		stdout:         stdout,
		stderr:         stderr,
		streamOutput:   true,
		outputActivity: make(chan struct{}, 1),
		outputDone:     make(chan struct{}),
		done:           make(chan struct{}),
	}

	s.mu.Lock()
	if s.processes == nil {
		s.processes = map[processSessionKey]*managedProcess{}
	}
	if _, exists := s.processes[key]; exists {
		s.mu.Unlock()
		cancel()
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("duplicate active process handle: %q", params.ProcessHandle))
	}
	s.processes[key] = process
	s.mu.Unlock()

	ptyProcess, ptySession, err := startPTYCommand(ctx, cmd, params.Size)
	if err != nil {
		s.removeProcess(connectionID, params.ProcessHandle)
		cancel()
		return nil, fmt.Errorf("failed to spawn process: %w", err)
	}
	s.mu.Lock()
	process.process = ptyProcess
	process.stdin = ptySession
	process.pty = ptySession
	s.mu.Unlock()

	go readPTYOutput(ptySession, stdout, process.outputActivity, process.outputDone, func(data []byte) {
		if len(data) == 0 || notify == nil {
			return
		}
		notify(NotificationProcessOutputDelta, &ProcessOutputDeltaNotification{
			ProcessHandle: params.ProcessHandle,
			Stream:        StreamStdout,
			DeltaBase64:   base64.StdEncoding.EncodeToString(data),
			CapReached:    stdout.CapReached(),
		})
	})
	go s.waitProcess(ctx, process, notify)
	return &ProcessSpawnResponse{}, nil
}

func (s *ProcessService) removeProcess(connectionID string, handle string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.processes, processKey(connectionID, handle))
}

func (s *ProcessService) ConnectionClosed(connectionID string) {
	if s == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	var processes []*managedProcess
	s.mu.Lock()
	for key, process := range s.processes {
		if key.connectionID == connectionID && process != nil {
			processes = append(processes, process)
		}
	}
	s.mu.Unlock()
	for _, process := range processes {
		s.cancelProcessForConnectionClose(process)
	}
}

func (s *ProcessService) cancelProcessForConnectionClose(process *managedProcess) {
	if process == nil {
		return
	}
	if process.cancel != nil {
		process.cancel()
	}
	stdin := io.WriteCloser(nil)
	s.mu.Lock()
	if process.pty == nil && process.stdin != nil {
		stdin = process.stdin
		process.stdin = nil
	}
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
}

func processKey(connectionID string, handle string) processSessionKey {
	return processSessionKey{
		connectionID: normalizeConnectionID(connectionID),
		handle:       strings.TrimSpace(handle),
	}
}
