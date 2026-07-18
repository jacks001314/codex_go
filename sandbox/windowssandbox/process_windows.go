//go:build windows

package windowssandbox

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const waitTimeoutCode uint32 = 0x00000102

func CreateProcessAsUserWithToken(req ProcessSpawnRequest) (*CreatedProcess, error) {
	if req.Token == 0 || len(req.Command) == 0 {
		return nil, ErrInvalidRequest
	}
	commandLineString := ArgvToCommandLine(req.Command)
	commandLine, err := windows.UTF16FromString(commandLineString)
	if err != nil {
		return nil, err
	}
	envBlock := MakeEnvBlock(req.Env)
	cwdPtr, err := utf16PtrOrNil(req.CWD)
	if err != nil {
		return nil, err
	}
	desktop, err := PrepareLaunchDesktop(req.UsePrivateDesktop)
	if err != nil {
		return nil, err
	}
	var processInfo windows.ProcessInformation
	if req.Stdio != nil {
		startupInfo, attrs, err := startupInfoWithExplicitStdio(req.Stdio, desktop)
		if err != nil {
			_ = desktop.Close()
			return nil, err
		}
		defer attrs.Close()
		creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
		err = windows.CreateProcessAsUser(
			windows.Token(req.Token),
			nil,
			&commandLine[0],
			nil,
			nil,
			true,
			creationFlags,
			&envBlock[0],
			cwdPtr,
			&startupInfo.StartupInfo,
			&processInfo,
		)
		if err != nil {
			_ = desktop.Close()
			logCreateProcessFailure(req, commandLineString, len(envBlock), startupInfo.StartupInfo.Flags, creationFlags, err)
			return nil, err
		}
		return createdProcessFromWindows(processInfo, startupInfo.StartupInfo.Flags, desktop), nil
	}

	startupInfo := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	startupInfo.Desktop = desktop.StartupInfoDesktop()
	if err := ensureInheritableStdio(&startupInfo); err != nil {
		_ = desktop.Close()
		return nil, err
	}
	creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT)
	err = windows.CreateProcessAsUser(
		windows.Token(req.Token),
		nil,
		&commandLine[0],
		nil,
		nil,
		true,
		creationFlags,
		&envBlock[0],
		cwdPtr,
		&startupInfo,
		&processInfo,
	)
	if err != nil {
		_ = desktop.Close()
		logCreateProcessFailure(req, commandLineString, len(envBlock), startupInfo.Flags, creationFlags, err)
		return nil, err
	}
	return createdProcessFromWindows(processInfo, startupInfo.Flags, desktop), nil
}

func SpawnProcessWithPipesWithToken(req PipeSpawnRequest) (*PipeSpawnHandles, error) {
	if req.StdinMode == "" {
		req.StdinMode = StdinModeClosed
	}
	if req.StderrMode == "" {
		req.StderrMode = StderrModeSeparate
	}
	var inR, inW windows.Handle
	var outR, outW windows.Handle
	var errR, errW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, fmt.Errorf("CreatePipe stdin failed: %w", err)
	}
	cleanup := func(handles ...windows.Handle) {
		for _, handle := range handles {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
		}
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		cleanup(inR, inW)
		return nil, fmt.Errorf("CreatePipe stdout failed: %w", err)
	}
	if req.StderrMode == StderrModeSeparate {
		if err := windows.CreatePipe(&errR, &errW, nil, 0); err != nil {
			cleanup(inR, inW, outR, outW)
			return nil, fmt.Errorf("CreatePipe stderr failed: %w", err)
		}
	}
	stderrHandle := outW
	if req.StderrMode == StderrModeSeparate {
		stderrHandle = errW
	}
	created, err := CreateProcessAsUserWithToken(ProcessSpawnRequest{
		Token:             req.Token,
		Command:           req.Command,
		CWD:               req.CWD,
		Env:               req.Env,
		LogsBaseDir:       req.LogsBaseDir,
		UsePrivateDesktop: req.UsePrivateDesktop,
		Stdio: &ProcessStdio{
			Stdin:  uintptr(inR),
			Stdout: uintptr(outW),
			Stderr: uintptr(stderrHandle),
		},
	})
	if err != nil {
		cleanup(inR, inW, outR, outW, errR, errW)
		return nil, err
	}
	cleanup(inR, outW)
	if req.StderrMode == StderrModeSeparate {
		cleanup(errW)
	}
	handles := &PipeSpawnHandles{
		Process:       created,
		StdoutRead:    uintptr(outR),
		HasStdinWrite: req.StdinMode == StdinModeOpen,
		HasStderrRead: req.StderrMode == StderrModeSeparate,
	}
	if req.StdinMode == StdinModeOpen {
		handles.StdinWrite = uintptr(inW)
	} else {
		cleanup(inW)
	}
	if req.StderrMode == StderrModeSeparate {
		handles.StderrRead = uintptr(errR)
	}
	return handles, nil
}

func ReadHandleLoop(handle uintptr, onChunk func([]byte)) (<-chan struct{}, error) {
	if handle == 0 {
		return nil, ErrInvalidRequest
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer windows.CloseHandle(windows.Handle(handle))
		buf := make([]byte, 8192)
		for {
			var read uint32
			err := windows.ReadFile(windows.Handle(handle), buf, &read, nil)
			if err != nil || read == 0 {
				return
			}
			if onChunk != nil {
				chunk := append([]byte(nil), buf[:read]...)
				onChunk(chunk)
			}
		}
	}()
	return done, nil
}

func WaitCreatedProcess(process *CreatedProcess, timeoutMS *int64, cancellation CancellationToken) (ProcessWaitOutcome, error) {
	if process == nil || process.ProcessHandle == 0 {
		return "", ErrInvalidRequest
	}
	handle := windows.Handle(process.ProcessHandle)
	if cancellation.IsCancelled == nil {
		timeout := uint32(windows.INFINITE)
		if timeoutMS != nil {
			if *timeoutMS <= 0 {
				timeout = 0
			} else if *timeoutMS > int64(^uint32(0)) {
				timeout = windows.INFINITE
			} else {
				timeout = uint32(*timeoutMS)
			}
		}
		result, err := windows.WaitForSingleObject(handle, timeout)
		if err != nil {
			return "", err
		}
		if result == waitTimeoutCode {
			return ProcessWaitTimedOut, nil
		}
		return ProcessWaitExited, nil
	}
	var deadline time.Time
	if timeoutMS != nil {
		deadline = time.Now().Add(time.Duration(*timeoutMS) * time.Millisecond)
	}
	for {
		if cancellation.Cancelled() {
			return ProcessWaitCancelled, nil
		}
		waitMS := uint32(50)
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return ProcessWaitTimedOut, nil
			}
			if remaining < 50*time.Millisecond {
				waitMS = uint32(remaining / time.Millisecond)
				if waitMS == 0 {
					waitMS = 1
				}
			}
		}
		result, err := windows.WaitForSingleObject(handle, waitMS)
		if err != nil {
			return "", err
		}
		if result == waitTimeoutCode {
			continue
		}
		return ProcessWaitExited, nil
	}
}

func TerminateCreatedProcess(process *CreatedProcess, exitCode uint32) error {
	if process == nil || process.ProcessHandle == 0 {
		return ErrInvalidRequest
	}
	return windows.TerminateProcess(windows.Handle(process.ProcessHandle), exitCode)
}

func CreatedProcessExitCode(process *CreatedProcess) (int, error) {
	if process == nil || process.ProcessHandle == 0 {
		return 0, ErrInvalidRequest
	}
	var code uint32
	if err := windows.GetExitCodeProcess(windows.Handle(process.ProcessHandle), &code); err != nil {
		return 0, err
	}
	return int(code), nil
}

func (p *CreatedProcess) Close() error {
	if p == nil {
		return nil
	}
	var firstErr error
	closeHandle := func(handle *uintptr) {
		if *handle == 0 {
			return
		}
		if err := windows.CloseHandle(windows.Handle(*handle)); firstErr == nil && err != nil {
			firstErr = err
		}
		*handle = 0
	}
	closeHandle(&p.ThreadHandle)
	closeHandle(&p.ProcessHandle)
	if p.Desktop != nil {
		if err := p.Desktop.Close(); firstErr == nil && err != nil {
			firstErr = err
		}
		p.Desktop = nil
	}
	return firstErr
}

func (h *PipeSpawnHandles) Close() error {
	if h == nil {
		return nil
	}
	var firstErr error
	closeRaw := func(handle *uintptr) {
		if *handle == 0 {
			return
		}
		if err := windows.CloseHandle(windows.Handle(*handle)); firstErr == nil && err != nil {
			firstErr = err
		}
		*handle = 0
	}
	closeRaw(&h.StdinWrite)
	closeRaw(&h.StdoutRead)
	closeRaw(&h.StderrRead)
	if h.Process != nil {
		if err := h.Process.Close(); firstErr == nil && err != nil {
			firstErr = err
		}
		h.Process = nil
	}
	h.HasStdinWrite = false
	h.HasStderrRead = false
	return firstErr
}

func ensureInheritableStdio(si *windows.StartupInfo) error {
	stdin, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return fmt.Errorf("GetStdHandle stdin failed: %w", err)
	}
	stdout, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return fmt.Errorf("GetStdHandle stdout failed: %w", err)
	}
	stderr, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		return fmt.Errorf("GetStdHandle stderr failed: %w", err)
	}
	for _, handle := range []windows.Handle{stdin, stdout, stderr} {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return fmt.Errorf("SetHandleInformation failed: %w", err)
		}
	}
	si.Flags |= windows.STARTF_USESTDHANDLES
	si.StdInput = stdin
	si.StdOutput = stdout
	si.StdErr = stderr
	return nil
}

func startupInfoWithExplicitStdio(stdio *ProcessStdio, desktop *LaunchDesktop) (*windows.StartupInfoEx, *ProcThreadAttributeList, error) {
	if stdio == nil || stdio.Stdin == 0 || stdio.Stdout == 0 || stdio.Stderr == 0 {
		return nil, nil, ErrInvalidRequest
	}
	startupInfo := &windows.StartupInfoEx{}
	startupInfo.StartupInfo.Cb = uint32(unsafe.Sizeof(windows.StartupInfoEx{}))
	startupInfo.StartupInfo.Desktop = desktop.StartupInfoDesktop()
	startupInfo.StartupInfo.Flags |= windows.STARTF_USESTDHANDLES
	startupInfo.StartupInfo.StdInput = windows.Handle(stdio.Stdin)
	startupInfo.StartupInfo.StdOutput = windows.Handle(stdio.Stdout)
	startupInfo.StartupInfo.StdErr = windows.Handle(stdio.Stderr)
	handles := []uintptr{stdio.Stdin, stdio.Stdout}
	if stdio.Stderr != stdio.Stdout {
		handles = append(handles, stdio.Stderr)
	}
	for _, handle := range handles {
		if err := windows.SetHandleInformation(windows.Handle(handle), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return nil, nil, fmt.Errorf("SetHandleInformation failed for stdio handle: %w", err)
		}
	}
	attrs, err := NewProcThreadAttributeListWithCount(1)
	if err != nil {
		return nil, nil, err
	}
	if err := attrs.SetHandleList(handles); err != nil {
		attrs.Close()
		return nil, nil, err
	}
	startupInfo.ProcThreadAttributeList = attrs.WindowsList()
	runtime.KeepAlive(handles)
	return startupInfo, attrs, nil
}

func createdProcessFromWindows(pi windows.ProcessInformation, startupFlags uint32, desktop *LaunchDesktop) *CreatedProcess {
	return &CreatedProcess{
		ProcessHandle: uintptr(pi.Process),
		ThreadHandle:  uintptr(pi.Thread),
		ProcessID:     pi.ProcessId,
		ThreadID:      pi.ThreadId,
		StartupFlags:  startupFlags,
		Desktop:       desktop,
	}
}

func utf16PtrOrNil(value string) (*uint16, error) {
	if value == "" {
		return nil, nil
	}
	return windows.UTF16PtrFromString(value)
}

func logCreateProcessFailure(req ProcessSpawnRequest, commandLine string, envBlockLen int, startupFlags uint32, creationFlags uint32, err error) {
	if req.LogsBaseDir == "" {
		return
	}
	_ = LogNoteInDir(req.LogsBaseDir, fmt.Sprintf(
		"CreateProcessAsUserW failed: %v | cwd=%s | cmd=%s | env_u16_len=%d | si_flags=%d | creation_flags=%d",
		err,
		req.CWD,
		commandLine,
		envBlockLen,
		startupFlags,
		creationFlags,
	))
}
