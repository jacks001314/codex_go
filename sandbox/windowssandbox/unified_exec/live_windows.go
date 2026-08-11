//go:build windows

package unified_exec

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/conpty"
	"codex_go/sandbox/windowssandbox/elevated"
)

const liveSessionPostExitQuietWindow = 200 * time.Millisecond

func SpawnWindowsSandboxLiveSessionForLevel(req *WindowsSandboxSessionRequest) (*LiveSession, error) {
	if req == nil {
		return nil, windowssandbox.ErrInvalidRequest
	}
	capture := req.Capture
	capture.TTY = req.PTY
	capture.StdinOpen = capture.StdinOpen || req.PTY
	capture.ProxyEnforced = req.ProxyEnforced
	if req.WindowsSandboxLevel == WindowsSandboxLevelElevated {
		return spawnWindowsSandboxLiveSessionElevated(&capture)
	}
	if capture.ProxyEnforced {
		return nil, fmt.Errorf("managed networking requires the elevated Windows sandbox backend")
	}
	if err := capture.Validate(); err != nil {
		return nil, err
	}
	spawnContext, err := windowssandbox.PrepareLegacySpawnContext(&capture, windowssandbox.SpawnPrepOptions{
		InheritPath:         false,
		AddGitSafeDirectory: false,
	})
	if err != nil {
		return nil, err
	}
	if !spawnContext.Permissions.HasFullDiskReadAccess() {
		return nil, fmt.Errorf("restricted read-only access requires the elevated Windows sandbox backend")
	}
	if len(capture.DenyReadPaths) > 0 {
		return nil, fmt.Errorf("deny-read overrides require the elevated Windows sandbox backend")
	}
	capabilityRoots := windowssandbox.LegacySessionCapabilityRoots(spawnContext.Permissions, spawnContext.CurrentDir, spawnContext.Env, capture.CodexHome)
	security, err := windowssandbox.PrepareLegacySessionSecurity(spawnContext.UsesWriteCapabilities, capture.CodexHome, capture.CWD, capabilityRoots)
	if err != nil {
		return nil, err
	}
	cleanupToken := true
	defer func() {
		if cleanupToken {
			windowssandbox.CloseTokenHandle(security.Token)
		}
	}()

	windowssandbox.AllowNullDeviceForWorkspaceWrite(spawnContext.UsesWriteCapabilities)
	if err := windowssandbox.ApplyLegacySessionACLRules(
		spawnContext.Permissions,
		capture.CodexHome,
		spawnContext.CurrentDir,
		spawnContext.Env,
		nil,
		capture.DenyWritePaths,
		windowssandbox.LegacyAclSIDs{ReadonlySID: security.ReadonlySID, WriteRootSIDs: security.WriteRootSIDs},
	); err != nil {
		return nil, err
	}

	var process *windowssandbox.CreatedProcess
	var instance *conpty.Instance
	var pipeHandles *windowssandbox.PipeSpawnHandles
	var stdin *os.File
	var readers []io.ReadCloser
	if capture.TTY {
		process, instance, err = conpty.SpawnProcessAsUserWithToken(conpty.SpawnRequest{
			Token:             security.Token,
			Command:           capture.Command,
			CWD:               capture.CWD,
			Env:               spawnContext.Env,
			UsePrivateDesktop: capture.UsePrivateDesktop,
			LogsBaseDir:       spawnContext.LogsBaseDir,
		})
		if err != nil {
			return nil, err
		}
		stdin = os.NewFile(instance.TakeInputWrite(), "codex-unified-exec-conpty-input")
		output := os.NewFile(instance.TakeOutputRead(), "codex-unified-exec-conpty-output")
		if stdin == nil || output == nil {
			if stdin != nil {
				_ = stdin.Close()
			}
			if output != nil {
				_ = output.Close()
			}
			_ = instance.Close()
			_ = process.Close()
			return nil, fmt.Errorf("failed to create ConPTY stream files")
		}
		readers = []io.ReadCloser{output}
	} else {
		stdinMode := windowssandbox.StdinModeClosed
		if capture.StdinOpen {
			stdinMode = windowssandbox.StdinModeOpen
		}
		pipeHandles, err = windowssandbox.SpawnProcessWithPipesWithToken(windowssandbox.PipeSpawnRequest{
			Token:             security.Token,
			Command:           capture.Command,
			CWD:               capture.CWD,
			Env:               spawnContext.Env,
			StdinMode:         stdinMode,
			StderrMode:        windowssandbox.StderrModeSeparate,
			UsePrivateDesktop: capture.UsePrivateDesktop,
			LogsBaseDir:       spawnContext.LogsBaseDir,
		})
		if err != nil {
			return nil, err
		}
		process = pipeHandles.Process
		if pipeHandles.StdinWrite != 0 {
			stdin = os.NewFile(pipeHandles.StdinWrite, "codex-unified-exec-stdin")
			pipeHandles.StdinWrite = 0
			pipeHandles.HasStdinWrite = false
		}
		stdout := os.NewFile(pipeHandles.StdoutRead, "codex-unified-exec-stdout")
		stderr := os.NewFile(pipeHandles.StderrRead, "codex-unified-exec-stderr")
		pipeHandles.StdoutRead = 0
		pipeHandles.StderrRead = 0
		if stdout == nil || stderr == nil {
			if stdout != nil {
				_ = stdout.Close()
			}
			if stderr != nil {
				_ = stderr.Close()
			}
			_ = pipeHandles.Close()
			return nil, fmt.Errorf("failed to create sandbox pipe stream files")
		}
		readers = []io.ReadCloser{stdout, stderr}
	}
	cleanupToken = false
	var sessionStdin io.WriteCloser = stdin
	if capture.TTY && stdin != nil {
		sessionStdin = &windowsTTYWriteCloser{inner: stdin}
	}
	session := &LiveSession{Stdin: sessionStdin, Readers: readers}
	session.wait = func() (int, error) {
		outcome, waitErr := windowssandbox.WaitCreatedProcess(process, nil, windowssandbox.CancellationToken{})
		if waitErr != nil {
			return -1, waitErr
		}
		if outcome != windowssandbox.ProcessWaitExited {
			return -1, fmt.Errorf("unexpected Windows sandbox process wait outcome %s", outcome)
		}
		exitCode, exitErr := windowssandbox.CreatedProcessExitCode(process)
		if capture.TTY {
			time.Sleep(liveSessionPostExitQuietWindow)
			_ = instance.Close()
		}
		return exitCode, exitErr
	}
	session.terminate = func() error {
		return windowssandbox.TerminateCreatedProcess(process, 1)
	}
	session.close = func() error {
		var firstErr error
		exitCode, exitCodeErr := windowssandbox.CreatedProcessExitCode(process)
		if instance != nil {
			if err := instance.Close(); firstErr == nil && err != nil {
				firstErr = err
			}
		}
		if pipeHandles != nil {
			if err := pipeHandles.Close(); firstErr == nil && err != nil {
				firstErr = err
			}
		} else if process != nil {
			if err := process.Close(); firstErr == nil && err != nil {
				firstErr = err
			}
		}
		windowssandbox.CloseTokenHandle(security.Token)
		if exitCodeErr == nil && exitCode == 0 {
			_ = windowssandbox.LogSuccess(capture.Command, capture.CodexHome)
		} else if exitCodeErr == nil {
			_ = windowssandbox.LogFailure(capture.Command, fmt.Sprintf("exit code %d", exitCode), capture.CodexHome)
		}
		return firstErr
	}
	return session, nil
}

type windowsTTYWriteCloser struct {
	mu            sync.Mutex
	inner         io.WriteCloser
	previousWasCR bool
}

func (w *windowsTTYWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inner == nil {
		return 0, os.ErrClosed
	}
	normalized := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case '\b':
			normalized = append(normalized, 0x7f)
		case '\n':
			if !w.previousWasCR {
				normalized = append(normalized, '\r')
			}
		default:
			normalized = append(normalized, b)
		}
		w.previousWasCR = b == '\r'
	}
	if len(normalized) == 0 {
		return len(data), nil
	}
	if _, err := w.inner.Write(normalized); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *windowsTTYWriteCloser) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inner == nil {
		return nil
	}
	err := w.inner.Close()
	w.inner = nil
	return err
}

type elevatedFrameWriter struct {
	mu            sync.Mutex
	writer        io.Writer
	tty           bool
	previousWasCR bool
	closed        bool
}

func (w *elevatedFrameWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.writer == nil {
		return 0, os.ErrClosed
	}
	payload := append([]byte(nil), data...)
	if w.tty {
		normalizer := windowsTTYWriteCloser{previousWasCR: w.previousWasCR}
		var normalized []byte
		for _, b := range payload {
			switch b {
			case '\b':
				normalized = append(normalized, 0x7f)
			case '\n':
				if !normalizer.previousWasCR {
					normalized = append(normalized, '\r')
				}
			default:
				normalized = append(normalized, b)
			}
			normalizer.previousWasCR = b == '\r'
		}
		w.previousWasCR = normalizer.previousWasCR
		payload = normalized
	}
	if len(payload) == 0 {
		return len(data), nil
	}
	err := elevated.WriteFrame(w.writer, &elevated.FramedMessage{
		Version: elevated.IPCProtocolVersion,
		Message: elevated.Message{Stdin: &elevated.StdinPayload{DataB64: elevated.EncodeBytes(payload)}},
	})
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *elevatedFrameWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.writer == nil {
		return nil
	}
	return elevated.WriteFrame(w.writer, &elevated.FramedMessage{
		Version: elevated.IPCProtocolVersion,
		Message: elevated.Message{CloseStdin: &elevated.EmptyPayload{}},
	})
}

func (w *elevatedFrameWriter) terminate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writer == nil {
		return os.ErrClosed
	}
	return elevated.WriteFrame(w.writer, &elevated.FramedMessage{
		Version: elevated.IPCProtocolVersion,
		Message: elevated.Message{Terminate: &elevated.EmptyPayload{}},
	})
}

type elevatedExitResult struct {
	exitCode int
	err      error
}

func spawnWindowsSandboxLiveSessionElevated(capture *windowssandbox.CaptureRequest) (*LiveSession, error) {
	transport, err := windowssandbox.SpawnWindowsSandboxElevatedRunnerTransport(capture)
	if err != nil {
		return nil, err
	}
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	input := &elevatedFrameWriter{writer: transport.PipeWrite, tty: capture.TTY}
	exitCh := make(chan elevatedExitResult, 1)
	go func() {
		defer stdoutWriter.Close()
		defer stderrWriter.Close()
		for {
			message, readErr := elevated.ReadFrame(transport.PipeRead)
			if readErr != nil {
				exitCh <- elevatedExitResult{exitCode: -1, err: readErr}
				return
			}
			if message == nil {
				exitCh <- elevatedExitResult{exitCode: -1, err: errors.New("runner pipe closed before exit")}
				return
			}
			switch {
			case message.Message.Output != nil:
				data, decodeErr := elevated.DecodeBytes(message.Message.Output.DataB64)
				if decodeErr != nil {
					exitCh <- elevatedExitResult{exitCode: -1, err: decodeErr}
					return
				}
				if message.Message.Output.Stream == elevated.OutputStreamStderr {
					_, _ = stderrWriter.Write(data)
				} else {
					_, _ = stdoutWriter.Write(data)
				}
			case message.Message.Exit != nil:
				exitCh <- elevatedExitResult{exitCode: message.Message.Exit.ExitCode}
				return
			case message.Message.Error != nil:
				exitCh <- elevatedExitResult{exitCode: -1, err: fmt.Errorf("runner error: %s", message.Message.Error.Message)}
				return
			}
		}
	}()
	readers := []io.ReadCloser{stdoutReader}
	if !capture.TTY {
		readers = append(readers, stderrReader)
	} else {
		_ = stderrReader.Close()
	}
	session := &LiveSession{Stdin: input, Readers: readers}
	session.wait = func() (int, error) {
		result := <-exitCh
		return result.exitCode, result.err
	}
	session.terminate = input.terminate
	session.close = transport.Close
	return session, nil
}
