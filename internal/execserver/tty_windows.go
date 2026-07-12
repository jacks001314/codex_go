//go:build windows

package execserver

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"codex_go/internal/sandbox/windowssandbox"
	"codex_go/internal/sandbox/windowssandbox/conpty"
	"golang.org/x/sys/windows"
)

type startedExecServerTTY struct {
	stdin    io.WriteCloser
	reader   io.ReadCloser
	wait     func() (int, error)
	kill     func() error
	closePTY func() error
	cleanup  func() error
}

func startExecServerTTY(cmd *exec.Cmd) (*startedExecServerTTY, bool, error) {
	if cmd == nil || len(cmd.Args) == 0 {
		return nil, true, windowssandbox.ErrInvalidRequest
	}
	instance, err := conpty.Create(80, 24)
	if err != nil {
		return nil, true, err
	}
	attrs, err := windowssandbox.NewProcThreadAttributeListWithCount(1)
	if err != nil {
		_ = instance.Close()
		return nil, true, err
	}
	defer attrs.Close()
	if err := attrs.SetPseudoconsole(instance.RawHandle()); err != nil {
		_ = instance.Close()
		return nil, true, err
	}
	desktop, err := windowssandbox.PrepareLaunchDesktop(false)
	if err != nil {
		_ = instance.Close()
		return nil, true, err
	}
	desktopOwned := true
	defer func() {
		if desktopOwned {
			_ = desktop.Close()
		}
	}()
	applicationName, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		_ = instance.Close()
		return nil, true, err
	}
	commandLine, err := windows.UTF16FromString(windowssandbox.ArgvToCommandLine(cmd.Args))
	if err != nil {
		_ = instance.Close()
		return nil, true, err
	}
	var cwd *uint16
	if cmd.Dir != "" {
		cwd, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			_ = instance.Close()
			return nil, true, err
		}
	}
	envBlock := windowssandbox.MakeEnvBlock(execServerTTYEnv(cmd.Env))
	startup := windows.StartupInfoEx{}
	startup.StartupInfo.Cb = uint32(unsafe.Sizeof(startup))
	startup.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	startup.StartupInfo.StdInput = windows.InvalidHandle
	startup.StartupInfo.StdOutput = windows.InvalidHandle
	startup.StartupInfo.StdErr = windows.InvalidHandle
	startup.StartupInfo.Desktop = desktop.StartupInfoDesktop()
	startup.ProcThreadAttributeList = attrs.WindowsList()
	var processInfo windows.ProcessInformation
	err = windows.CreateProcess(
		applicationName,
		&commandLine[0],
		nil,
		nil,
		false,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT,
		&envBlock[0],
		cwd,
		&startup.StartupInfo,
		&processInfo,
	)
	runtime.KeepAlive(attrs)
	runtime.KeepAlive(commandLine)
	runtime.KeepAlive(envBlock)
	runtime.KeepAlive(instance)
	runtime.KeepAlive(startup)
	if err != nil {
		_ = instance.Close()
		return nil, true, err
	}
	process := &windowssandbox.CreatedProcess{
		ProcessHandle: uintptr(processInfo.Process),
		ThreadHandle:  uintptr(processInfo.Thread),
		ProcessID:     processInfo.ProcessId,
		ThreadID:      processInfo.ThreadId,
		StartupFlags:  startup.StartupInfo.Flags,
		Desktop:       desktop,
	}
	desktopOwned = false
	reader := os.NewFile(instance.TakeOutputRead(), "codex-exec-server-conpty-output")
	rawWriter := os.NewFile(instance.TakeInputWrite(), "codex-exec-server-conpty-input")
	writer := &execServerWindowsTTYWriter{WriteCloser: rawWriter}
	var closePTYOnce sync.Once
	closePTY := func() error {
		var closeErr error
		closePTYOnce.Do(func() {
			_ = rawWriter.Close()
			closeErr = instance.Close()
		})
		return closeErr
	}
	var cleanupOnce sync.Once
	cleanup := func() error {
		var cleanupErr error
		cleanupOnce.Do(func() {
			cleanupErr = process.Close()
		})
		return cleanupErr
	}
	return &startedExecServerTTY{
		stdin:  writer,
		reader: reader,
		wait: func() (int, error) {
			outcome, waitErr := windowssandbox.WaitCreatedProcess(process, nil, windowssandbox.CancellationToken{})
			if waitErr != nil {
				return -1, waitErr
			}
			if outcome != windowssandbox.ProcessWaitExited {
				return -1, windowssandbox.ErrInvalidRequest
			}
			return windowssandbox.CreatedProcessExitCode(process)
		},
		kill: func() error {
			return windowssandbox.TerminateCreatedProcess(process, 1)
		},
		closePTY: func() error {
			time.Sleep(200 * time.Millisecond)
			return closePTY()
		},
		cleanup: cleanup,
	}, true, nil
}

func execServerTTYEnv(values []string) map[string]string {
	out := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if ok && key != "" {
			out[key] = item
		}
	}
	return out
}

type execServerWindowsTTYWriter struct {
	io.WriteCloser
	mu            sync.Mutex
	previousWasCR bool
}

func (w *execServerWindowsTTYWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	normalized := make([]byte, 0, len(data))
	for _, value := range data {
		switch value {
		case '\b':
			normalized = append(normalized, 0x7f)
		case '\n':
			if !w.previousWasCR {
				normalized = append(normalized, '\r')
			}
		default:
			normalized = append(normalized, value)
		}
		w.previousWasCR = value == '\r'
	}
	if _, err := w.WriteCloser.Write(normalized); err != nil {
		return 0, err
	}
	return len(data), nil
}
