//go:build windows

package appserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"
	"sort"
	"strings"
	"unsafe"

	"codex_go/internal/sandbox/windowssandbox"
	"codex_go/internal/sandbox/windowssandbox/conpty"

	"golang.org/x/sys/windows"
)

func startPTYCommand(ctx context.Context, cmd *osexec.Cmd, size *TerminalSize) (*ptyProcess, *ptyHandle, error) {
	if cmd == nil || len(cmd.Args) == 0 {
		return nil, nil, fmt.Errorf("%w: pty command is empty", ErrInvalidRequest)
	}
	if cmd.Err != nil {
		return nil, nil, cmd.Err
	}

	terminalSize := terminalSizeOrDefault(size)
	conptyInstance, err := conpty.Create(int16(terminalSize.Cols), int16(terminalSize.Rows))
	if err != nil {
		return nil, nil, err
	}

	attributeList, err := windowssandbox.NewProcThreadAttributeListWithCount(1)
	if err != nil {
		_ = conptyInstance.Close()
		return nil, nil, err
	}
	defer attributeList.Close()
	if err := attributeList.SetPseudoconsole(conptyInstance.RawHandle()); err != nil {
		_ = conptyInstance.Close()
		return nil, nil, err
	}

	desktop, err := windowssandbox.PrepareLaunchDesktop(false)
	if err != nil {
		_ = conptyInstance.Close()
		return nil, nil, err
	}

	processInfo, err := createWindowsPTYProcess(cmd, attributeList, desktop)
	if err != nil {
		_ = desktop.Close()
		_ = conptyInstance.Close()
		return nil, nil, err
	}
	windows.CloseHandle(processInfo.Thread)
	job := attachWindowsPTYJobObject(processInfo.Process)

	done := make(chan struct{})
	process := &ptyProcess{
		wait: func() error {
			defer close(done)
			defer windows.CloseHandle(processInfo.Process)
			defer desktop.Close()
			if job != 0 {
				defer windows.CloseHandle(job)
			}
			_, err := windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
			if err != nil {
				return err
			}
			var exitCode uint32
			if err := windows.GetExitCodeProcess(processInfo.Process, &exitCode); err != nil {
				return err
			}
			if exitCode != 0 {
				return &ptyExitError{code: int(exitCode)}
			}
			return nil
		},
		kill: func() error {
			if job != 0 {
				if err := windows.TerminateJobObject(job, 1); err == nil || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
					return nil
				}
			}
			if err := windows.TerminateProcess(processInfo.Process, 1); err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return err
			}
			return nil
		},
	}
	reader := os.NewFile(conptyInstance.TakeOutputRead(), "codex-conpty-output")
	writer := os.NewFile(conptyInstance.TakeInputWrite(), "codex-conpty-input")
	handle := &ptyHandle{
		reader:    reader,
		writer:    writer,
		normalize: (&windowsTTYInputNormalizer{}).Normalize,
		closeInput: func() error {
			return writer.Close()
		},
		closePTY: func() error {
			_ = writer.Close()
			return conptyInstance.Close()
		},
		closeReader: func() error {
			return reader.Close()
		},
		resize: func(size *TerminalSize) error {
			terminalSize := terminalSizeOrDefault(size)
			return conptyInstance.Resize(uint16(terminalSize.Cols), uint16(terminalSize.Rows))
		},
	}
	go monitorPTYContext(ctx.Done(), done, process)
	return process, handle, nil
}

func attachWindowsPTYJobObject(process windows.Handle) windows.Handle {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	return job
}

func createWindowsPTYProcess(cmd *osexec.Cmd, attributeList *windowssandbox.ProcThreadAttributeList, desktop *windowssandbox.LaunchDesktop) (*windows.ProcessInformation, error) {
	applicationName, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, err
	}
	commandLine := windows.ComposeCommandLine(cmd.Args)
	commandLinePtr, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, err
	}
	var cwdPtr *uint16
	if cmd.Dir != "" {
		cwdPtr, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, err
		}
	}
	envBlock, err := windowsPTYEnvBlock(cmd.Env)
	if err != nil {
		return nil, err
	}
	startupInfo := &windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:        uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:     windows.STARTF_USESTDHANDLES,
			StdInput:  windows.InvalidHandle,
			StdOutput: windows.InvalidHandle,
			StdErr:    windows.InvalidHandle,
			Desktop:   desktop.StartupInfoDesktop(),
		},
		ProcThreadAttributeList: attributeList.WindowsList(),
	}
	processInfo := &windows.ProcessInformation{}
	creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	var envPtr *uint16
	if len(envBlock) > 0 {
		envPtr = &envBlock[0]
	}
	err = windows.CreateProcess(applicationName, commandLinePtr, nil, nil, false, creationFlags, envPtr, cwdPtr, &startupInfo.StartupInfo, processInfo)
	runtime.KeepAlive(attributeList)
	runtime.KeepAlive(envBlock)
	if err != nil {
		return nil, err
	}
	return processInfo, nil
}

func windowsPTYEnvBlock(env []string) ([]uint16, error) {
	if len(env) == 0 {
		return []uint16{0, 0}, nil
	}
	sorted := append([]string(nil), env...)
	sort.SliceStable(sorted, func(i int, j int) bool {
		return strings.ToUpper(envKey(sorted[i])) < strings.ToUpper(envKey(sorted[j]))
	})
	var block []uint16
	for _, item := range sorted {
		if strings.ContainsRune(item, 0) {
			return nil, windows.ERROR_INVALID_PARAMETER
		}
		encoded, err := windows.UTF16FromString(item)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded...)
	}
	block = append(block, 0)
	return block, nil
}

func envKey(item string) string {
	key, _, ok := strings.Cut(item, "=")
	if !ok {
		return item
	}
	return key
}

type windowsTTYInputNormalizer struct {
	previousWasCR bool
}

func (n *windowsTTYInputNormalizer) Normalize(data []byte) []byte {
	if n == nil || len(data) == 0 {
		return data
	}
	normalized := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case '\b':
			normalized = append(normalized, 0x7f)
		case '\n':
			if !n.previousWasCR {
				normalized = append(normalized, '\r')
			}
		default:
			normalized = append(normalized, b)
		}
		n.previousWasCR = b == '\r'
	}
	return normalized
}

var _ io.WriteCloser = (*ptyHandle)(nil)
