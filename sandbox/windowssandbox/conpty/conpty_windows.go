//go:build windows

package conpty

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"codex_go/sandbox/windowssandbox"
	"golang.org/x/sys/windows"
)

const (
	defaultColumns           int16  = 80
	defaultRows              int16  = 24
	pseudoConsoleResizeQuirk uint32 = 0x2
)

func Create(columns int16, rows int16) (*Instance, error) {
	columns, rows = normalizeSize(columns, rows)
	var inputRead, inputWrite windows.Handle
	var outputRead, outputWrite windows.Handle
	if err := windows.CreatePipe(&inputRead, &inputWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("CreatePipe ConPTY input failed: %w", err)
	}
	cleanup := func(handles ...windows.Handle) {
		for _, handle := range handles {
			if handle != 0 && handle != windows.InvalidHandle {
				_ = windows.CloseHandle(handle)
			}
		}
	}
	if err := windows.CreatePipe(&outputRead, &outputWrite, nil, 0); err != nil {
		cleanup(inputRead, inputWrite)
		return nil, fmt.Errorf("CreatePipe ConPTY output failed: %w", err)
	}
	var pseudo windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: columns, Y: rows}, inputRead, outputWrite, pseudoConsoleResizeQuirk, &pseudo)
	if err != nil {
		cleanup(inputRead, inputWrite, outputRead, outputWrite)
		return nil, fmt.Errorf("CreatePseudoConsole failed: %w", err)
	}
	return &Instance{
		pseudoConsole: uintptr(pseudo),
		inputRead:     uintptr(inputRead),
		inputWrite:    uintptr(inputWrite),
		outputRead:    uintptr(outputRead),
		outputWrite:   uintptr(outputWrite),
	}, nil
}

func SpawnProcessAsUser(command []string, cwd string) (*Instance, error) {
	token, err := windowssandbox.GetCurrentTokenForRestriction()
	if err != nil {
		return nil, err
	}
	defer windowssandbox.CloseTokenHandle(token)
	created, instance, err := SpawnProcessAsUserWithToken(SpawnRequest{
		Token:   token,
		Command: command,
		CWD:     cwd,
		Env:     environmentMap(),
	})
	if err != nil {
		return nil, err
	}
	if created != nil {
		_ = created.Close()
	}
	return instance, nil
}

func SpawnProcessAsUserWithToken(req SpawnRequest) (*windowssandbox.CreatedProcess, *Instance, error) {
	if req.Token == 0 || len(req.Command) == 0 {
		return nil, nil, windowssandbox.ErrInvalidRequest
	}
	columns, rows := normalizeSize(req.Columns, req.Rows)
	commandLineString := windowssandbox.ArgvToCommandLine(req.Command)
	commandLine, err := windows.UTF16FromString(commandLineString)
	if err != nil {
		return nil, nil, err
	}
	envBlock := windowssandbox.MakeEnvBlock(req.Env)
	cwdPtr, err := utf16PtrOrNil(req.CWD)
	if err != nil {
		return nil, nil, err
	}
	desktop, err := windowssandbox.PrepareLaunchDesktop(req.UsePrivateDesktop)
	if err != nil {
		return nil, nil, err
	}
	instance, err := Create(columns, rows)
	if err != nil {
		_ = desktop.Close()
		return nil, nil, err
	}
	attrs, err := windowssandbox.NewProcThreadAttributeListWithCount(1)
	if err != nil {
		_ = instance.Close()
		_ = desktop.Close()
		return nil, nil, err
	}
	defer attrs.Close()
	if err := attrs.SetPseudoconsole(instance.RawHandle()); err != nil {
		_ = instance.Close()
		_ = desktop.Close()
		return nil, nil, err
	}
	startupInfo := windows.StartupInfoEx{}
	startupInfo.StartupInfo.Cb = uint32(unsafe.Sizeof(windows.StartupInfoEx{}))
	startupInfo.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	startupInfo.StartupInfo.StdInput = windows.InvalidHandle
	startupInfo.StartupInfo.StdOutput = windows.InvalidHandle
	startupInfo.StartupInfo.StdErr = windows.InvalidHandle
	startupInfo.StartupInfo.Desktop = desktop.StartupInfoDesktop()
	startupInfo.ProcThreadAttributeList = attrs.WindowsList()

	var processInfo windows.ProcessInformation
	creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	err = windows.CreateProcessAsUser(
		windows.Token(req.Token),
		nil,
		&commandLine[0],
		nil,
		nil,
		false,
		creationFlags,
		&envBlock[0],
		cwdPtr,
		&startupInfo.StartupInfo,
		&processInfo,
	)
	if err != nil {
		_ = instance.Close()
		_ = desktop.Close()
		logCreateProcessFailure(req, commandLineString, len(envBlock), creationFlags, err)
		return nil, nil, err
	}
	runtime.KeepAlive(instance)
	runtime.KeepAlive(startupInfo)
	created := &windowssandbox.CreatedProcess{
		ProcessHandle: uintptr(processInfo.Process),
		ThreadHandle:  uintptr(processInfo.Thread),
		ProcessID:     processInfo.ProcessId,
		ThreadID:      processInfo.ThreadId,
		StartupFlags:  startupInfo.StartupInfo.Flags,
		Desktop:       desktop,
	}
	return created, instance, nil
}

func (i *Instance) Resize(columns uint16, rows uint16) error {
	if i == nil || i.pseudoConsole == 0 {
		return windowssandbox.ErrInvalidRequest
	}
	cols, rws := normalizeSize(int16(columns), int16(rows))
	return windows.ResizePseudoConsole(windows.Handle(i.pseudoConsole), windows.Coord{X: cols, Y: rws})
}

func (i *Instance) CloseInputWrite() error {
	handle := i.forgetInputWrite()
	if handle == 0 || windows.Handle(handle) == windows.InvalidHandle {
		return nil
	}
	return windows.CloseHandle(windows.Handle(handle))
}

func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	var firstErr error
	closeHandle := func(handle *uintptr) {
		if *handle == 0 || windows.Handle(*handle) == windows.InvalidHandle {
			*handle = 0
			return
		}
		if err := windows.CloseHandle(windows.Handle(*handle)); firstErr == nil && err != nil {
			firstErr = err
		}
		*handle = 0
	}
	closeHandle(&i.inputWrite)
	closeHandle(&i.outputRead)
	if i.pseudoConsole != 0 && windows.Handle(i.pseudoConsole) != windows.InvalidHandle {
		windows.ClosePseudoConsole(windows.Handle(i.pseudoConsole))
		i.pseudoConsole = 0
	}
	closeHandle(&i.inputRead)
	closeHandle(&i.outputWrite)
	return firstErr
}

func normalizeSize(columns int16, rows int16) (int16, int16) {
	if columns <= 0 {
		columns = defaultColumns
	}
	if rows <= 0 {
		rows = defaultRows
	}
	return columns, rows
}

func utf16PtrOrNil(value string) (*uint16, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return windows.UTF16PtrFromString(value)
}

func environmentMap() map[string]string {
	out := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return windowssandbox.PrepareEnvironment(out)
}

func logCreateProcessFailure(req SpawnRequest, commandLine string, envBlockLen int, creationFlags uint32, err error) {
	if strings.TrimSpace(req.LogsBaseDir) == "" {
		return
	}
	_ = windowssandbox.LogNoteInDir(req.LogsBaseDir, fmt.Sprintf(
		"CreateProcessAsUserW ConPTY failed: %v | cwd=%s | cmd=%s | env_u16_len=%d | creation_flags=%d",
		err,
		req.CWD,
		commandLine,
		envBlockLen,
		creationFlags,
	))
}
