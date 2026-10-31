//go:build windows

package provisionservice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procWaitNamedPipeW = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")
	procPeekNamedPipe  = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")
)

const framePollInterval = 5 * time.Millisecond

// WindowsTransport dials the installed provisioning service over its
// authenticated named pipe.
type WindowsTransport struct{}

// NewWindowsTransport returns the installed-service transport.
func NewWindowsTransport() *WindowsTransport {
	return &WindowsTransport{}
}

// Connect opens the provisioning pipe, waiting for a starting service. It
// returns ErrServiceUnavailable when the service is absent or never starts.
func (t *WindowsTransport) Connect(deadline time.Time) (Conn, error) {
	// Startup must not hold up the desktop's readiness check for the whole
	// provisioning timeout; the caller's deadline still bounds real work.
	startupDeadline := earliest(deadline, time.Now().Add(ServiceStartupTimeout))
	pipeName, err := ServicePipeName()
	if err != nil {
		return nil, err
	}
	pipeNameWide, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return nil, err
	}
	for {
		status, statusErr := queryServiceStatus()
		switch {
		case statusErr != nil && isServiceAbsent(statusErr):
			return nil, ErrServiceUnavailable
		case statusErr != nil:
			return nil, statusErr
		case status.CurrentState == windows.SERVICE_START_PENDING:
			remaining := time.Until(startupDeadline)
			if remaining <= 0 {
				return nil, ErrServiceUnavailable
			}
			time.Sleep(minDuration(remaining, reconnectPollInterval))
			continue
		case status.CurrentState != windows.SERVICE_RUNNING:
			return nil, ErrServiceUnavailable
		}
		handle, openErr := openProvisioningPipe(pipeName)
		if openErr == nil {
			return &pipeConn{handle: handle}, nil
		}
		if errors.Is(openErr, windows.ERROR_FILE_NOT_FOUND) {
			return nil, ErrServiceUnavailable
		}
		if !errors.Is(openErr, windows.ERROR_PIPE_BUSY) {
			return nil, fmt.Errorf("open sandbox provisioning pipe: %w", openErr)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ErrServiceUnavailable
		}
		waitMilliseconds := uint32(minInt64(remaining.Milliseconds(), math.MaxUint32))
		if waitMilliseconds == 0 {
			waitMilliseconds = 1
		}
		waited, _, callErr := procWaitNamedPipeW.Call(
			uintptr(unsafe.Pointer(pipeNameWide)),
			uintptr(waitMilliseconds),
		)
		if waited != 0 {
			continue
		}
		if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
			continue
		}
		if errors.Is(callErr, windows.ERROR_SEM_TIMEOUT) {
			return nil, ErrServiceUnavailable
		}
		return nil, fmt.Errorf("wait for sandbox provisioning pipe: %w", callErr)
	}
}

// ConnectAfterRestart waits for a pipe served by a different, running service
// process. A same-PID reuse is conservatively refused.
func (t *WindowsTransport) ConnectAfterRestart(previousPID uint32, deadline time.Time) (Conn, error) {
	for {
		if !time.Now().Before(deadline) {
			return nil, restartTimeoutError()
		}
		conn, err := t.Connect(deadline)
		if err != nil && !errors.Is(err, ErrServiceUnavailable) {
			return nil, err
		}
		if err == nil && conn != nil && conn.ServerPID() != previousPID {
			status, statusErr := queryServiceStatus()
			if statusErr != nil {
				_ = conn.Close()
				return nil, statusErr
			}
			// The service creates its listener before reporting RUNNING.
			if status.CurrentState != windows.SERVICE_START_PENDING {
				return conn, nil
			}
		}
		if conn != nil {
			_ = conn.Close()
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, restartTimeoutError()
		}
		time.Sleep(minDuration(remaining, reconnectPollInterval))
	}
}

// StartServiceForSetup starts a stopped service for explicit setup. The normal
// connection path still waits for startup and authenticates the pipe server.
func StartServiceForSetup() error {
	serviceName, err := ServiceName()
	if err != nil {
		return err
	}
	serviceNameWide, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return err
	}
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("open service control manager: %w", err)
	}
	defer windows.CloseServiceHandle(manager)
	statusHandle, err := windows.OpenService(manager, serviceNameWide, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		if isServiceAbsent(err) {
			return nil
		}
		return fmt.Errorf("request permission to start sandbox service for setup: %w", err)
	}
	status, statusErr := queryServiceStatusFor(statusHandle)
	_ = windows.CloseServiceHandle(statusHandle)
	if statusErr != nil {
		if isServiceAbsent(statusErr) {
			return nil
		}
		return statusErr
	}
	if status.CurrentState != windows.SERVICE_STOPPED {
		return nil
	}
	service, err := windows.OpenService(manager, serviceNameWide, windows.SERVICE_START)
	if err != nil {
		if isServiceAbsent(err) {
			return nil
		}
		return fmt.Errorf("request permission to start sandbox service for setup: %w", err)
	}
	defer windows.CloseServiceHandle(service)
	if err := windows.StartService(service, 0, nil); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("start sandbox service for setup: %w", err)
	}
	return nil
}

type pipeConn struct {
	handle windows.Handle
}

func (p *pipeConn) ServerPID() uint32 {
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(p.handle, &pid); err != nil {
		return 0
	}
	return pid
}

func (p *pipeConn) Close() error {
	if p == nil || p.handle == 0 || p.handle == windows.InvalidHandle {
		return nil
	}
	handle := p.handle
	p.handle = 0
	return windows.CloseHandle(handle)
}

func (p *pipeConn) Exchange(frame *Frame, deadline time.Time) (Response, error) {
	if err := verifyPipeServer(p.handle); err != nil {
		return Response{}, fmt.Errorf("authenticate provisioning pipe server: %w", err)
	}
	if !time.Now().Before(deadline) {
		return Response{}, fmt.Errorf("sandbox provisioning deadline expired before sending request: %w", os.ErrDeadlineExceeded)
	}
	if err := WriteFrame(pipeWriter{handle: p.handle}, frame); err != nil {
		return Response{}, fmt.Errorf("send sandbox provisioning request: %w", err)
	}
	return readProvisioningResponse(p.handle, deadline)
}

type pipeWriter struct {
	handle windows.Handle
}

func (w pipeWriter) Write(data []byte) (int, error) {
	total := 0
	for total < len(data) {
		var written uint32
		if err := windows.WriteFile(w.handle, data[total:], &written, nil); err != nil {
			return total, err
		}
		if written == 0 {
			return total, io.ErrShortWrite
		}
		total += int(written)
	}
	return total, nil
}

type pipeReader struct {
	handle windows.Handle
}

func (r pipeReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	var read uint32
	if err := windows.ReadFile(r.handle, buffer, &read, nil); err != nil {
		return int(read), err
	}
	if read == 0 {
		return 0, io.EOF
	}
	return int(read), nil
}

// readProvisioningResponse waits for a complete frame, then reads it. A
// different protocol version is treated as an unsupported caller.
func readProvisioningResponse(handle windows.Handle, deadline time.Time) (Response, error) {
	if err := waitForCompleteFrame(handle, deadline); err != nil {
		return Response{}, fmt.Errorf("wait for sandbox provisioning response: %w", err)
	}
	frame, err := ReadFrame(pipeReader{handle: handle})
	if err != nil {
		return Response{}, fmt.Errorf("read sandbox provisioning response: %w", err)
	}
	if frame == nil {
		return Response{}, fmt.Errorf("read sandbox provisioning response: %w", io.ErrUnexpectedEOF)
	}
	if frame.Version != ProtocolVersion {
		return Response{Kind: ResponseUnavailable}, nil
	}
	if frame.Message.Kind != KindProvisionSandboxResponse || frame.Message.Response == nil {
		return Response{}, errors.New("unexpected sandbox provisioning response message")
	}
	return *frame.Message.Response, nil
}

// waitForCompleteFrame polls the pipe without consuming bytes, mirroring Rust
// framed_io::wait_for_complete_frame.
func waitForCompleteFrame(handle windows.Handle, deadline time.Time) error {
	var length [4]byte
	for {
		var bytesRead uint32
		var totalAvailable uint32
		ok, _, callErr := procPeekNamedPipe.Call(
			uintptr(handle),
			uintptr(unsafe.Pointer(&length[0])),
			uintptr(len(length)),
			uintptr(unsafe.Pointer(&bytesRead)),
			uintptr(unsafe.Pointer(&totalAvailable)),
			0,
		)
		if ok == 0 {
			if errno, isErrno := callErr.(syscall.Errno); !isErrno || errno == 0 {
				return syscall.EINVAL
			}
			return callErr
		}
		if bytesRead == uint32(len(length)) {
			frameLen := binary.LittleEndian.Uint32(length[:])
			if frameLen > MaxFrameLen {
				return fmt.Errorf("frame too large: %d: %w", frameLen, syscall.EINVAL)
			}
			if totalAvailable >= uint32(len(length))+frameLen {
				return nil
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timed out waiting for a complete IPC frame: %w", os.ErrDeadlineExceeded)
		}
		time.Sleep(minDuration(remaining, framePollInterval))
	}
}

func verifyPipeServer(handle windows.Handle) error {
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(handle, &pid); err != nil {
		return fmt.Errorf("identify provisioning pipe server: %w", err)
	}
	status, err := queryServiceStatus()
	if err != nil {
		return err
	}
	if status.CurrentState != windows.SERVICE_RUNNING || status.ProcessId == 0 || status.ProcessId != pid {
		return errors.New("the provisioning pipe server does not match the running service")
	}
	return nil
}

func queryServiceStatus() (windows.SERVICE_STATUS_PROCESS, error) {
	serviceName, err := ServiceName()
	if err != nil {
		return windows.SERVICE_STATUS_PROCESS{}, err
	}
	serviceNameWide, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return windows.SERVICE_STATUS_PROCESS{}, err
	}
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return windows.SERVICE_STATUS_PROCESS{}, fmt.Errorf("open service control manager: %w", err)
	}
	defer windows.CloseServiceHandle(manager)
	service, err := windows.OpenService(manager, serviceNameWide, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return windows.SERVICE_STATUS_PROCESS{}, fmt.Errorf("open sandbox provisioning service: %w", err)
	}
	defer windows.CloseServiceHandle(service)
	return queryServiceStatusFor(service)
}

func queryServiceStatusFor(service windows.Handle) (windows.SERVICE_STATUS_PROCESS, error) {
	var status windows.SERVICE_STATUS_PROCESS
	var bytesNeeded uint32
	if err := windows.QueryServiceStatusEx(
		service,
		windows.SC_STATUS_PROCESS_INFO,
		(*byte)(unsafe.Pointer(&status)),
		uint32(unsafe.Sizeof(status)),
		&bytesNeeded,
	); err != nil {
		return windows.SERVICE_STATUS_PROCESS{}, fmt.Errorf("query sandbox provisioning service: %w", err)
	}
	return status, nil
}

func openProvisioningPipe(pipeName string) (windows.Handle, error) {
	pipeNameWide, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(
		pipeNameWide,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IMPERSONATION,
		0,
	)
}

func isServiceAbsent(err error) bool {
	return errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) || errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE)
}

func restartTimeoutError() error {
	return fmt.Errorf("timed out waiting for restarted sandbox provisioning service: %w", os.ErrDeadlineExceeded)
}

func earliest(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}

func minDuration(first, second time.Duration) time.Duration {
	if first < second {
		return first
	}
	return second
}

func minInt64(first, second int64) int64 {
	if first < second {
		return first
	}
	return second
}
