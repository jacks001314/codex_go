//go:build windows

package elevated

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func CurrentUsername() (string, error) {
	var size uint32 = 256
	for {
		buf := make([]uint16, size)
		err := windows.GetUserNameEx(windows.NameSamCompatible, &buf[0], &size)
		if err == nil {
			return windows.UTF16ToString(buf), nil
		}
		if err != windows.ERROR_MORE_DATA && err != windows.ERROR_INSUFFICIENT_BUFFER {
			return "", err
		}
		if size <= uint32(len(buf)) {
			size = uint32(len(buf) * 2)
		}
	}
}

func CreateNamedPipe(name string, access uint32, sandboxUsername string) (*RunnerPipe, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("pipe name is empty")
	}
	if strings.TrimSpace(sandboxUsername) == "" {
		return nil, fmt.Errorf("sandbox username is empty")
	}
	sid, _, _, err := windows.LookupSID("", sandboxUsername)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox user SID: %w", err)
	}
	sddl := fmt.Sprintf("D:(A;;GA;;;%s)", sid.String())
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	securityAttributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateNamedPipe(
		namePtr,
		access,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1,
		65536,
		65536,
		0,
		securityAttributes,
	)
	if err != nil {
		return nil, err
	}
	return &RunnerPipe{Name: name, Handle: uintptr(handle)}, nil
}

func ConnectPipe(pipe *RunnerPipe, expectedRunnerPID uint32) error {
	if pipe == nil || pipe.Handle == 0 {
		return fmt.Errorf("pipe handle is empty")
	}
	return ConnectPipeHandle(pipe.Handle, expectedRunnerPID)
}

func ConnectPipeHandle(handle uintptr, expectedRunnerPID uint32) error {
	if handle == 0 {
		return fmt.Errorf("pipe handle is empty")
	}
	err := windows.ConnectNamedPipe(windows.Handle(handle), nil)
	if err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return err
	}
	var clientPID uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(handle), &clientPID); err != nil {
		return err
	}
	if expectedRunnerPID != 0 && clientPID != expectedRunnerPID {
		return fmt.Errorf("named pipe client pid %d did not match runner pid %d", clientPID, expectedRunnerPID)
	}
	return nil
}

func (p *RunnerPipe) Close() error {
	if p == nil || p.Handle == 0 {
		return nil
	}
	handle := windows.Handle(p.Handle)
	p.Handle = 0
	return windows.CloseHandle(handle)
}
