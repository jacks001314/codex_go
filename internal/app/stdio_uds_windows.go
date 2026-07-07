//go:build windows

package app

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/sys/windows"
)

func bridgeStdioToUDS(socketPath string, stdin io.Reader, stdout io.Writer) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return fmt.Errorf("stdio-to-uds requires SOCKET_PATH")
	}
	if !isWindowsNamedPipePath(socketPath) {
		return fmt.Errorf("stdio-to-uds on Windows requires a named pipe path, got %s", socketPath)
	}
	pipe, err := openWindowsNamedPipe(socketPath)
	if err != nil {
		return err
	}
	defer pipe.Close()

	errCh := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(pipe, stdin)
		errCh <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(stdout, pipe)
		errCh <- copyErr
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		if copyErr := <-errCh; copyErr != nil && !errors.Is(copyErr, io.ErrClosedPipe) && firstErr == nil {
			firstErr = copyErr
		}
	}
	return firstErr
}

type windowsNamedPipe struct {
	handle windows.Handle
}

func openWindowsNamedPipe(path string) (*windowsNamedPipe, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open Windows named pipe %s: %w", path, err)
	}
	return &windowsNamedPipe{handle: handle}, nil
}

func (p *windowsNamedPipe) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if p == nil || p.handle == 0 {
		return 0, io.ErrClosedPipe
	}
	event, overlapped, err := newWindowsPipeOverlapped()
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	err = windows.ReadFile(p.handle, buf, nil, overlapped)
	read, err := completeWindowsPipeOverlapped(p.handle, overlapped, err)
	if err != nil {
		if err == windows.ERROR_BROKEN_PIPE || err == windows.ERROR_HANDLE_EOF {
			return int(read), io.EOF
		}
		return int(read), err
	}
	return int(read), nil
}

func (p *windowsNamedPipe) Write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if p == nil || p.handle == 0 {
		return 0, io.ErrClosedPipe
	}
	var total int
	for total < len(buf) {
		chunk := buf[total:]
		event, overlapped, err := newWindowsPipeOverlapped()
		if err != nil {
			return total, err
		}
		err = windows.WriteFile(p.handle, chunk, nil, overlapped)
		written, err := completeWindowsPipeOverlapped(p.handle, overlapped, err)
		_ = windows.CloseHandle(event)
		total += int(written)
		if err != nil {
			if err == windows.ERROR_BROKEN_PIPE || err == windows.ERROR_NO_DATA {
				return total, io.ErrClosedPipe
			}
			return total, err
		}
		if written == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (p *windowsNamedPipe) Close() error {
	if p == nil || p.handle == 0 {
		return nil
	}
	handle := p.handle
	p.handle = 0
	return windows.CloseHandle(handle)
}

func newWindowsPipeOverlapped() (windows.Handle, *windows.Overlapped, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, nil, err
	}
	overlapped := &windows.Overlapped{HEvent: event}
	return event, overlapped, nil
}

func completeWindowsPipeOverlapped(handle windows.Handle, overlapped *windows.Overlapped, startErr error) (uint32, error) {
	if startErr != nil && startErr != windows.ERROR_IO_PENDING {
		return 0, startErr
	}
	var done uint32
	err := windows.GetOverlappedResult(handle, overlapped, &done, true)
	if err != nil {
		return done, err
	}
	return done, nil
}
