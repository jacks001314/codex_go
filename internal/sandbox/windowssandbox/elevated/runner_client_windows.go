//go:build windows

package elevated

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	logonWithProfile         uint32 = 0x00000001
	runnerErrorModeFlag      uint32 = 0x0001 | 0x0002
	windowsCommandRunnerFlag        = "--codex-run-as-windows-command-runner"
)

var (
	modadvapi32                 = windows.NewLazySystemDLL("advapi32.dll")
	procCreateProcessWithLogonW = modadvapi32.NewProc("CreateProcessWithLogonW")
)

func connectRunner(pipeName string) (*RunnerClient, error) {
	if strings.TrimSpace(pipeName) == "" {
		return nil, fmt.Errorf("pipe name is empty")
	}
	namePtr, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(namePtr, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), pipeName)
	return &RunnerClient{PipeName: pipeName, Handle: uintptr(handle), file: file}, nil
}

func closeRunnerClient(c *RunnerClient) error {
	if c == nil {
		return nil
	}
	if c.file != nil {
		err := c.file.Close()
		c.file = nil
		c.Handle = 0
		return err
	}
	if c.Handle != 0 {
		handle := windows.Handle(c.Handle)
		c.Handle = 0
		return windows.CloseHandle(handle)
	}
	return nil
}

func spawnRunnerTransport(codexHome string, cwd string, creds *SandboxCredentials, currentExe string, request SpawnRequest) (*RunnerTransport, error) {
	if creds == nil {
		return nil, fmt.Errorf("sandbox credentials are nil")
	}
	pipeInName, pipeOutName := PipePair()
	pipeIn, err := CreateNamedPipe(pipeInName, PipeAccessOutbound, creds.Username)
	if err != nil {
		return nil, err
	}
	pipeOut, err := CreateNamedPipe(pipeOutName, PipeAccessInbound, creds.Username)
	if err != nil {
		_ = pipeIn.Close()
		return nil, err
	}
	runnerExe := FindRunnerExe(codexHome, currentExe)
	if strings.TrimSpace(runnerExe) == "" {
		runnerExe = "codex-command-runner.exe"
	}
	runnerFullCommand := argvToCommandLine([]string{
		runnerExe,
		windowsCommandRunnerFlag,
		"--pipe-in=" + pipeInName,
		"--pipe-out=" + pipeOutName,
	})
	var processInfo windows.ProcessInformation
	startupInfo := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	previousErrorMode := windows.SetErrorMode(runnerErrorModeFlag)
	err = createProcessWithLogon(creds, runnerExe, runnerFullCommand, cwd, &startupInfo, &processInfo)
	windows.SetErrorMode(previousErrorMode)
	if err != nil {
		_ = pipeIn.Close()
		_ = pipeOut.Close()
		var errno syscall.Errno
		if errorsAsErrno(err, &errno) {
			return nil, &RunnerLogonError{Code: uint32(errno)}
		}
		return nil, err
	}
	defer func() {
		if processInfo.Thread != 0 {
			_ = windows.CloseHandle(processInfo.Thread)
		}
	}()

	connectErr := connectPipeWithTimeout(pipeIn, processInfo.ProcessId, "pipe-in")
	if connectErr == nil {
		connectErr = connectPipeWithTimeout(pipeOut, processInfo.ProcessId, "pipe-out")
	}
	if connectErr != nil {
		if processInfo.Process != 0 {
			_ = windows.TerminateProcess(processInfo.Process, 1)
			_ = windows.CloseHandle(processInfo.Process)
		}
		_ = pipeIn.Close()
		_ = pipeOut.Close()
		return nil, connectErr
	}

	transport := &RunnerTransport{
		PipeWrite: os.NewFile(pipeIn.Handle, pipeIn.Name),
		PipeRead:  os.NewFile(pipeOut.Handle, pipeOut.Name),
	}
	pipeIn.Handle = 0
	pipeOut.Handle = 0

	startupErr := func() error {
		if err := transport.SendSpawnRequest(request); err != nil {
			return err
		}
		return transport.ReadSpawnReady()
	}()
	if startupErr != nil {
		if processInfo.Process != 0 {
			_ = windows.TerminateProcess(processInfo.Process, 1)
			_ = windows.CloseHandle(processInfo.Process)
		}
		_ = transport.Close()
		return nil, startupErr
	}
	if processInfo.Process != 0 {
		_ = windows.CloseHandle(processInfo.Process)
	}
	return transport, nil
}

func createProcessWithLogon(creds *SandboxCredentials, appName string, commandLine string, cwd string, startupInfo *windows.StartupInfo, processInfo *windows.ProcessInformation) error {
	username, err := windows.UTF16PtrFromString(creds.Username)
	if err != nil {
		return err
	}
	domain := creds.Domain
	if strings.TrimSpace(domain) == "" {
		domain = "."
	}
	domainPtr, err := windows.UTF16PtrFromString(domain)
	if err != nil {
		return err
	}
	passwordPtr, err := windows.UTF16PtrFromString(creds.Password)
	if err != nil {
		return err
	}
	appPtr, err := windows.UTF16PtrFromString(appName)
	if err != nil {
		return err
	}
	commandLineW, err := windows.UTF16FromString(commandLine)
	if err != nil {
		return err
	}
	var cwdPtr *uint16
	if strings.TrimSpace(cwd) != "" {
		cwdPtr, err = windows.UTF16PtrFromString(cwd)
		if err != nil {
			return err
		}
	}
	r1, _, e1 := procCreateProcessWithLogonW.Call(
		uintptr(unsafe.Pointer(username)),
		uintptr(unsafe.Pointer(domainPtr)),
		uintptr(unsafe.Pointer(passwordPtr)),
		uintptr(logonWithProfile),
		uintptr(unsafe.Pointer(appPtr)),
		uintptr(unsafe.Pointer(&commandLineW[0])),
		uintptr(windows.CREATE_NO_WINDOW|windows.CREATE_UNICODE_ENVIRONMENT),
		0,
		uintptr(unsafe.Pointer(cwdPtr)),
		uintptr(unsafe.Pointer(startupInfo)),
		uintptr(unsafe.Pointer(processInfo)),
	)
	if r1 == 0 {
		if e1 != windows.ERROR_SUCCESS {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}

func connectPipeWithTimeout(pipe *RunnerPipe, expectedRunnerPID uint32, label string) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- ConnectPipe(pipe, expectedRunnerPID)
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(RunnerPipeConnectTimeout):
		_ = pipe.Close()
		return fmt.Errorf("timed out after %dms connecting runner %s", RunnerPipeConnectTimeout.Milliseconds(), label)
	}
}

func errorsAsErrno(err error, target *syscall.Errno) bool {
	if err == nil {
		return false
	}
	if errno, ok := err.(syscall.Errno); ok {
		*target = errno
		return true
	}
	return false
}
