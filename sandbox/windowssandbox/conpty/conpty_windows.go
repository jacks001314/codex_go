//go:build windows

package conpty

import (
	"fmt"
	"log/slog"
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

// releasePseudoConsole exists on Windows 11 24H2 and newer; older Windows keeps
// the ClosePseudoConsole-on-close path (Rust #45504).
var releasePseudoConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReleasePseudoConsole")

// ReleasePseudoConsoleAvailable reports whether the OS exposes
// ReleasePseudoConsole.
func ReleasePseudoConsoleAvailable() bool {
	return releasePseudoConsole.Find() == nil
}

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
	// Rust #46575: a caller with OS package identity passes desktop app context
	// through to the sandboxed child so packaged processes keep working.
	preserveAppContext, err := windowssandbox.CurrentProcessHasPackageIdentity()
	if err != nil {
		_ = instance.Close()
		_ = desktop.Close()
		return nil, nil, err
	}
	attrs, err := windowssandbox.NewProcThreadAttributeListWithCount(windowssandbox.ProcThreadAttributeCountForLaunch(preserveAppContext))
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
	if preserveAppContext {
		if err := attrs.PreserveDesktopAppContext(); err != nil {
			_ = instance.Close()
			_ = desktop.Close()
			return nil, nil, err
		}
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
	// Rust #45504: the creation handles only need to survive until a client has
	// attached. Keeping the output write handle would prevent output readers from
	// seeing EOF while the session stays alive.
	instance.FinishSpawn()
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

// FinishSpawn releases the pseudoconsole's creation handles and, when the OS
// supports it, the creator's ownership. Call it once the pseudoconsole has been
// attached to a spawned client: keeping the output write handle afterwards would
// prevent output readers from seeing EOF while the session stays alive, and
// releasing ownership lets the console close its output once the last attached
// client exits without terminating surviving descendants (Rust #45504).
func (i *Instance) FinishSpawn() {
	i.dropCreationHandles()
	i.releasePseudoConsoleOwnership()
}

// dropCreationHandles closes the pipe handles CreatePseudoConsole borrowed. They
// are retained until a client has attached.
func (i *Instance) dropCreationHandles() {
	if i == nil {
		return
	}
	for _, handle := range []*uintptr{&i.inputRead, &i.outputWrite} {
		if *handle == 0 || windows.Handle(*handle) == windows.InvalidHandle {
			*handle = 0
			continue
		}
		_ = windows.CloseHandle(windows.Handle(*handle))
		*handle = 0
	}
}

// releasePseudoConsoleOwnership releases the creator's ownership of the
// pseudoconsole when the OS supports it, so surviving console descendants keep
// their I/O while the console itself may close.
func (i *Instance) releasePseudoConsoleOwnership() {
	if i == nil || i.pseudoConsole == 0 || !ReleasePseudoConsoleAvailable() {
		return
	}
	result, _, _ := releasePseudoConsole.Call(i.pseudoConsole)
	if int32(result) != 0 /* S_OK */ {
		slog.Warn("failed to release pseudoconsole ownership", "hresult", int32(result))
	}
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
