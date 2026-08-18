//go:build windows

package idecontext

import (
	"errors"
	"io"
	"time"

	"golang.org/x/sys/windows"
)

type windowsPipeStream struct {
	handle   windows.Handle
	deadline time.Time
}

// windowsPipeSecurityFlags mirrors Rust #39020: the IDE-context named pipe is
// opened with SECURITY_SQOS_PRESENT | SECURITY_IDENTIFICATION so the server can
// identify the client but cannot impersonate it beyond the identification level.
const windowsPipeSecurityFlags = windows.SECURITY_SQOS_PRESENT | windows.SECURITY_IDENTIFICATION

func connectIDEContext(_ string, _ string, deadline time.Time) (io.ReadWriteCloser, error) {
	path, err := windows.UTF16PtrFromString(DefaultWindowsPipeName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		path,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED|windowsPipeSecurityFlags,
		0,
	)
	if err != nil {
		return nil, err
	}
	if err := validateWindowsPipeServerOwner(handle); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return &windowsPipeStream{handle: handle, deadline: deadline}, nil
}

func (s *windowsPipeStream) Read(buffer []byte) (int, error) {
	return s.overlappedIO(buffer, false)
}

func (s *windowsPipeStream) Write(buffer []byte) (int, error) {
	return s.overlappedIO(buffer, true)
}

func (s *windowsPipeStream) Close() error {
	if s == nil || s.handle == 0 || s.handle == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(s.handle)
	s.handle = 0
	return err
}

func (s *windowsPipeStream) overlappedIO(buffer []byte, write bool) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	var transferred uint32
	if write {
		err = windows.WriteFile(s.handle, buffer, &transferred, &overlapped)
	} else {
		err = windows.ReadFile(s.handle, buffer, &transferred, &overlapped)
	}
	if err == nil {
		return int(transferred), nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, err
	}

	waitResult, waitErr := windows.WaitForSingleObject(event, RemainingTimeoutMS(s.deadline, time.Now()))
	if waitErr != nil {
		return 0, waitErr
	}
	if waitResult == uint32(windows.WAIT_TIMEOUT) {
		_ = windows.CancelIoEx(s.handle, &overlapped)
		_ = windows.GetOverlappedResult(s.handle, &overlapped, &transferred, true)
		return 0, ErrIDEContextTimedOut
	}
	if waitResult != windows.WAIT_OBJECT_0 {
		return 0, errors.New("unexpected named-pipe wait result")
	}
	if err := windows.GetOverlappedResult(s.handle, &overlapped, &transferred, false); err != nil {
		return 0, err
	}
	return int(transferred), nil
}

func validateWindowsPipeServerOwner(pipe windows.Handle) error {
	var processID uint32
	if err := windows.GetNamedPipeServerProcessId(pipe, &processID); err != nil {
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)

	serverSID, err := windowsProcessUserSID(process)
	if err != nil {
		return err
	}
	currentSID, err := windowsProcessUserSID(windows.CurrentProcess())
	if err != nil {
		return err
	}
	if !serverSID.Equals(currentSID) {
		return errors.New("IDE context provider is not owned by the current user")
	}
	return nil
}

func windowsProcessUserSID(process windows.Handle) (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}
