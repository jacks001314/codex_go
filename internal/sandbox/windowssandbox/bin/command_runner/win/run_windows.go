//go:build windows

package win

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"codex_go/internal/sandbox/windowssandbox"
	setupwin "codex_go/internal/sandbox/windowssandbox/bin/setup_main/win"
	"codex_go/internal/sandbox/windowssandbox/conpty"
	"codex_go/internal/sandbox/windowssandbox/elevated"
	"golang.org/x/sys/windows"
)

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	pipeIn, pipeOut, err := parseRunnerArgs(args)
	if err != nil {
		return err
	}
	readFile, err := openRunnerPipe(pipeIn, windows.GENERIC_READ)
	if err != nil {
		return err
	}
	defer readFile.Close()
	writeFile, err := openRunnerPipe(pipeOut, windows.GENERIC_WRITE)
	if err != nil {
		return err
	}
	defer writeFile.Close()
	writer := &lockedFrameWriter{file: writeFile}

	request, err := readSpawnRequest(readFile)
	if err != nil {
		_ = sendRunnerError(writer, elevated.ErrorStageReadSpawnRequest, err)
		return err
	}
	spawned, err := spawnIPCProcess(request)
	if err != nil {
		_ = sendRunnerError(writer, elevated.ErrorStageSpawnChild, err)
		return err
	}
	defer spawned.Close()

	if err := writer.Write(&elevated.FramedMessage{
		Version: elevated.IPCProtocolVersion,
		Message: elevated.Message{SpawnReady: &elevated.SpawnReady{
			ProcessID: spawned.Handles.Process.ProcessID,
		}},
	}); err != nil {
		_ = sendRunnerError(writer, elevated.ErrorStageWriteSpawnReady, err)
		return err
	}

	stdoutDone, err := startRunnerOutputReader(writer, spawned.Handles.StdoutRead, elevated.OutputStreamStdout)
	if err != nil {
		return err
	}
	stderrDone, err := startRunnerStderrReader(writer, spawned)
	if err != nil {
		return err
	}
	inputDone := startRunnerInputLoop(readFile, spawned)

	outcome, err := windowssandbox.WaitCreatedProcess(spawned.Handles.Process, timeoutInt64(request.TimeoutMS), windowssandbox.CancellationToken{})
	if err != nil {
		return err
	}
	timedOut := outcome == windowssandbox.ProcessWaitTimedOut
	exitCode := 1
	if timedOut {
		_ = windowssandbox.TerminateCreatedProcess(spawned.Handles.Process, 1)
		exitCode = 128 + 64
	} else {
		exitCode, err = windowssandbox.CreatedProcessExitCode(spawned.Handles.Process)
		if err != nil {
			return err
		}
	}
	<-stdoutDone
	<-stderrDone
	spawned.Handles.StdoutRead = 0
	spawned.Handles.StderrRead = 0
	if spawned.Handles.StdinWrite != 0 {
		_ = windows.CloseHandle(windows.Handle(spawned.Handles.StdinWrite))
		spawned.Handles.StdinWrite = 0
	}
	_ = readFile.Close()
	select {
	case <-inputDone:
	case <-time.After(time.Second):
	}
	return writer.Write(&elevated.FramedMessage{
		Version: elevated.IPCProtocolVersion,
		Message: elevated.Message{Exit: &elevated.ExitPayload{
			ExitCode: exitCode,
			TimedOut: timedOut,
		}},
	})
}

type ipcSpawnedProcess struct {
	Handles *windowssandbox.PipeSpawnHandles
	Token   uintptr
	ConPTY  *conpty.Instance
	TTY     bool
}

func (p *ipcSpawnedProcess) Close() error {
	if p == nil {
		return nil
	}
	var firstErr error
	if p.Handles != nil {
		if err := p.Handles.Close(); err != nil {
			firstErr = err
		}
		p.Handles = nil
	}
	if p.ConPTY != nil {
		if err := p.ConPTY.Close(); firstErr == nil && err != nil {
			firstErr = err
		}
		p.ConPTY = nil
	}
	if p.Token != 0 {
		if err := windowssandbox.CloseTokenHandle(p.Token); firstErr == nil && err != nil {
			firstErr = err
		}
		p.Token = 0
	}
	return firstErr
}

type lockedFrameWriter struct {
	mu   sync.Mutex
	file *os.File
}

func (w *lockedFrameWriter) Write(msg *elevated.FramedMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return elevated.WriteFrame(w.file, msg)
}

func parseRunnerArgs(args []string) (string, string, error) {
	var pipeIn string
	var pipeOut string
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--pipe-in="); ok {
			pipeIn = value
		} else if value, ok := strings.CutPrefix(arg, "--pipe-out="); ok {
			pipeOut = value
		}
	}
	if strings.TrimSpace(pipeIn) == "" {
		return "", "", fmt.Errorf("runner: no pipe-in provided")
	}
	if strings.TrimSpace(pipeOut) == "" {
		return "", "", fmt.Errorf("runner: no pipe-out provided")
	}
	return pipeIn, pipeOut, nil
}

func openRunnerPipe(name string, access uint32) (*os.File, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(namePtr, access, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateFileW failed for pipe %s: %w", name, err)
	}
	return os.NewFile(uintptr(handle), name), nil
}

func readSpawnRequest(reader io.Reader) (*elevated.SpawnRequest, error) {
	msg, err := elevated.ReadFrame(reader)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, fmt.Errorf("runner: pipe closed before spawn_request")
	}
	if msg.Version != elevated.IPCProtocolVersion {
		return nil, fmt.Errorf("runner: unsupported protocol version %d", msg.Version)
	}
	if msg.Message.SpawnRequest == nil {
		return nil, fmt.Errorf("runner: expected spawn_request, got %s", msg.Message.Type())
	}
	return msg.Message.SpawnRequest, nil
}

func spawnIPCProcess(request *elevated.SpawnRequest) (*ipcSpawnedProcess, error) {
	if request == nil {
		return nil, windowssandbox.ErrInvalidRequest
	}
	_ = windowssandbox.HideCurrentUserProfileDir(request.CodexHome)
	tokenMode, err := windowssandbox.TokenModeForPermissionProfile(request.PermissionProfile, request.WorkspaceRoots, request.CWD, request.Env)
	if err != nil {
		return nil, err
	}
	if len(request.CapSIDs) == 0 {
		return nil, fmt.Errorf("runner: empty capability SID list")
	}
	base, err := windowssandbox.GetCurrentTokenForRestriction()
	if err != nil {
		return nil, err
	}
	defer windowssandbox.CloseTokenHandle(base)
	var token uintptr
	switch tokenMode {
	case windowssandbox.WindowsSandboxTokenModeReadOnlyCapability:
		token, err = windowssandbox.CreateReadonlyTokenWithCapsAndUserFrom(base, request.CapSIDs)
	case windowssandbox.WindowsSandboxTokenModeWritableRootsCapability:
		token, err = windowssandbox.CreateWorkspaceWriteTokenWithCapsAndUserFrom(base, request.CapSIDs)
	default:
		err = fmt.Errorf("runner: unsupported token mode %s", tokenMode)
	}
	if err != nil {
		return nil, err
	}
	for _, sid := range request.CapSIDs {
		_ = windowssandbox.AllowNullDevice(sid)
	}
	effectiveCWD := effectiveRunnerCWD(request.CWD, request.CodexHome)
	if request.TTY {
		created, instance, err := conpty.SpawnProcessAsUserWithToken(conpty.SpawnRequest{
			Token:             token,
			Command:           request.Command,
			CWD:               effectiveCWD,
			Env:               request.Env,
			UsePrivateDesktop: request.UsePrivateDesktop,
			LogsBaseDir:       request.CodexHome,
		})
		if err != nil {
			_ = windowssandbox.CloseTokenHandle(token)
			return nil, err
		}
		inputWrite := instance.TakeInputWrite()
		stdinWrite := uintptr(0)
		if request.StdinOpen {
			stdinWrite = inputWrite
		} else if inputWrite != 0 {
			_ = windows.CloseHandle(windows.Handle(inputWrite))
		}
		return &ipcSpawnedProcess{
			Handles: &windowssandbox.PipeSpawnHandles{
				Process:       created,
				StdinWrite:    stdinWrite,
				StdoutRead:    instance.TakeOutputRead(),
				HasStdinWrite: request.StdinOpen,
			},
			Token:  token,
			ConPTY: instance,
			TTY:    true,
		}, nil
	}
	stdinMode := windowssandbox.StdinModeClosed
	if request.StdinOpen {
		stdinMode = windowssandbox.StdinModeOpen
	}
	handles, err := windowssandbox.SpawnProcessWithPipesWithToken(windowssandbox.PipeSpawnRequest{
		Token:             token,
		Command:           request.Command,
		CWD:               effectiveCWD,
		Env:               request.Env,
		StdinMode:         stdinMode,
		StderrMode:        windowssandbox.StderrModeSeparate,
		UsePrivateDesktop: request.UsePrivateDesktop,
		LogsBaseDir:       request.CodexHome,
	})
	if err != nil {
		_ = windowssandbox.CloseTokenHandle(token)
		return nil, err
	}
	return &ipcSpawnedProcess{Handles: handles, Token: token}, nil
}

func effectiveRunnerCWD(cwd string, logDir string) string {
	exists, err := setupwin.ReadACLMutexExists()
	if err == nil && exists {
		if junction, junctionErr := CreateCWDJunctionWithLogDir(cwd, logDir); junctionErr == nil {
			return junction
		}
	}
	return cwd
}

func startRunnerOutputReader(writer *lockedFrameWriter, handle uintptr, stream elevated.OutputStream) (<-chan struct{}, error) {
	return windowssandbox.ReadHandleLoop(handle, func(chunk []byte) {
		_ = writer.Write(&elevated.FramedMessage{
			Version: elevated.IPCProtocolVersion,
			Message: elevated.Message{Output: &elevated.OutputPayload{
				Stream:  stream,
				DataB64: elevated.EncodeBytes(chunk),
			}},
		})
	})
}

func startRunnerStderrReader(writer *lockedFrameWriter, spawned *ipcSpawnedProcess) (<-chan struct{}, error) {
	if spawned == nil || spawned.Handles == nil || !spawned.Handles.HasStderrRead || spawned.Handles.StderrRead == 0 {
		done := make(chan struct{})
		close(done)
		return done, nil
	}
	return startRunnerOutputReader(writer, spawned.Handles.StderrRead, elevated.OutputStreamStderr)
}

func startRunnerInputLoop(reader io.Reader, spawned *ipcSpawnedProcess) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msg, err := elevated.ReadFrame(reader)
			if err != nil || msg == nil {
				return
			}
			switch {
			case msg.Message.Stdin != nil:
				bytes, err := elevated.DecodeBytes(msg.Message.Stdin.DataB64)
				if err == nil {
					writeRunnerStdin(spawned, bytes)
				}
			case msg.Message.CloseStdin != nil:
				closeRunnerStdin(spawned)
			case msg.Message.Terminate != nil:
				if spawned != nil && spawned.Handles != nil {
					_ = windowssandbox.TerminateCreatedProcess(spawned.Handles.Process, 1)
				}
			case msg.Message.Resize != nil:
				if spawned != nil && spawned.ConPTY != nil {
					_ = spawned.ConPTY.Resize(msg.Message.Resize.Cols, msg.Message.Resize.Rows)
				}
			}
		}
	}()
	return done
}

func writeRunnerStdin(spawned *ipcSpawnedProcess, data []byte) {
	if spawned == nil || spawned.Handles == nil || spawned.Handles.StdinWrite == 0 {
		return
	}
	handle := windows.Handle(spawned.Handles.StdinWrite)
	for len(data) > 0 {
		chunk := data
		if len(chunk) > int(^uint32(0)) {
			chunk = chunk[:int(^uint32(0))]
		}
		var written uint32
		err := windows.WriteFile(handle, chunk, &written, nil)
		if err != nil || written == 0 {
			closeRunnerStdin(spawned)
			return
		}
		data = data[int(written):]
	}
}

func closeRunnerStdin(spawned *ipcSpawnedProcess) {
	if spawned == nil || spawned.Handles == nil || spawned.Handles.StdinWrite == 0 {
		return
	}
	_ = windows.CloseHandle(windows.Handle(spawned.Handles.StdinWrite))
	spawned.Handles.StdinWrite = 0
	spawned.Handles.HasStdinWrite = false
}

func sendRunnerError(writer *lockedFrameWriter, stage elevated.ErrorStage, err error) error {
	if err == nil {
		return nil
	}
	return writer.Write(&elevated.FramedMessage{
		Version: elevated.IPCProtocolVersion,
		Message: elevated.Message{Error: &elevated.ErrorPayload{
			Message: err.Error(),
			Stage:   stage,
		}},
	})
}

func timeoutInt64(timeout *uint64) *int64 {
	if timeout == nil {
		return nil
	}
	value := *timeout
	const maxInt64 = uint64(1<<63 - 1)
	if value > maxInt64 {
		value = maxInt64
	}
	out := int64(value)
	return &out
}
