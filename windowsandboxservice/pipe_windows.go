//go:build windows

package windowsandboxservice

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	setupwin "codex_go/sandbox/windowssandbox/bin/setup_main/win"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

const (
	pipeAccessDuplex          = 0x0000_0003
	fileFlagFirstPipeInstance = 0x0008_0000
	pipeTypeByte              = 0x0000_0000
	pipeReadModeByte          = 0x0000_0000
	pipeWaitMode              = 0x0000_0000
	pipeRejectRemoteClients   = 0x0000_0008
	pipeOutboundBufferBytes   = 1024

	pipeSetupLockTimeoutMillis = 5000
	sddlRevision1              = 1
)

var (
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procConvertStringSecurityDescriptor = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")

	loadInstallation     = windowssandbox.LoadInstallation
	ensureSandboxUserSID = func() ([]byte, error) {
		if err := setupwin.EnsureSandboxUsersGroup(io.Discard); err != nil {
			return nil, err
		}
		return setupwin.ResolveSandboxUsersGroupSID()
	}
	resolveSandboxSID = setupwin.ResolveSandboxUsersGroupSID
)

// ProvisioningListener owns the one-instance provisioning pipe and the sandbox
// group generation carried by its deny rule (Rust ipc::listener).
type ProvisioningListener struct {
	Pipe       windows.Handle
	SandboxSID []byte
	descriptor *windows.SECURITY_DESCRIPTOR
}

// OpenProvisioningListener serves the active package family after refusing an
// interrupted registered-Core cleanup.
func OpenProvisioningListener() (*ProvisioningListener, error) {
	pipeName, err := provisionservice.ServicePipeName()
	if err != nil {
		return nil, err
	}
	lock, err := windowssandbox.AcquireSandboxSetupLock(pipeSetupLockTimeoutMillis)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	record, err := loadInstallation()
	if err != nil {
		return nil, err
	}
	if record != nil && record.Runtime != nil && record.Runtime.Retiring != nil {
		return nil, errors.New("interrupted sandbox cleanup requires repair")
	}
	sandboxSID, err := ensureSandboxUserSID()
	if err != nil {
		return nil, err
	}
	return createProvisioningPipe(pipeName, sandboxSID)
}

// Refresh reuses the listener while the sandbox group generation is unchanged
// and rebuilds the descriptor otherwise (Rust ProvisioningListener::refresh).
func (listener *ProvisioningListener) Refresh() (*ProvisioningListener, error) {
	if listener == nil {
		return nil, errors.New("missing provisioning listener")
	}
	lock, err := windowssandbox.AcquireSandboxSetupLock(pipeSetupLockTimeoutMillis)
	if err != nil {
		return nil, err
	}
	current, resolveErr := resolveSandboxSID()
	lock.Release()
	if resolveErr == nil && bytes.Equal(current, listener.SandboxSID) {
		// The same generation only needs the connected instance released so the
		// next client can be accepted on this handler.
		_ = windows.DisconnectNamedPipe(listener.Pipe)
		return listener, nil
	}
	// A stale deny SID was refused before dispatch: close this first instance
	// before rebuilding the descriptor for the current group generation.
	listener.Close()
	return OpenProvisioningListener()
}

// Close releases the pipe and the descriptor that backed its DACL.
func (listener *ProvisioningListener) Close() {
	if listener == nil {
		return
	}
	if listener.Pipe != 0 {
		_ = windows.CloseHandle(listener.Pipe)
		listener.Pipe = 0
	}
	if listener.descriptor != nil {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(listener.descriptor)))
		listener.descriptor = nil
	}
}

func createProvisioningPipe(pipeName string, sandboxSID []byte) (*ProvisioningListener, error) {
	sddl, err := windows.UTF16PtrFromString(PipeSecurityDescriptor(windowssandbox.StringFromSIDBytes(sandboxSID)))
	if err != nil {
		return nil, err
	}
	var descriptor *windows.SECURITY_DESCRIPTOR
	created, _, callErr := procConvertStringSecurityDescriptor.Call(
		uintptr(unsafe.Pointer(sddl)),
		sddlRevision1,
		uintptr(unsafe.Pointer(&descriptor)),
		0,
	)
	if created == 0 {
		return nil, fmt.Errorf("create provisioning pipe DACL: %w", callErr)
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(descriptor)))
		return nil, err
	}
	pipe, err := windows.CreateNamedPipe(
		name,
		pipeAccessDuplex|fileFlagFirstPipeInstance,
		pipeTypeByte|pipeReadModeByte|pipeWaitMode|pipeRejectRemoteClients,
		1,
		pipeOutboundBufferBytes,
		MaxRequestBytes,
		0,
		&attributes,
	)
	if err != nil {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(descriptor)))
		return nil, fmt.Errorf("create provisioning pipe: %w", err)
	}
	return &ProvisioningListener{
		Pipe:       pipe,
		SandboxSID: append([]byte(nil), sandboxSID...),
		descriptor: descriptor,
	}, nil
}

// AcceptPipeConnection waits for one client. A client that connected and then
// closed is reported as a disconnected wakeup so the caller refreshes without
// dispatching (Rust accept_pipe_connection).
func AcceptPipeConnection(pipe windows.Handle) (bool, error) {
	connectErr := windows.ConnectNamedPipe(pipe, nil)
	if connectErr == nil || errors.Is(connectErr, windows.ERROR_PIPE_CONNECTED) {
		return true, nil
	}
	if !errors.Is(connectErr, windows.ERROR_NO_DATA) && !errors.Is(connectErr, windows.ERROR_BROKEN_PIPE) {
		return false, fmt.Errorf("accept provisioning client: %w", connectErr)
	}
	if resetErr := windows.DisconnectNamedPipe(pipe); resetErr != nil && !errors.Is(resetErr, windows.ERROR_PIPE_NOT_CONNECTED) {
		return false, fmt.Errorf("reset disconnected provisioning client: %w", resetErr)
	}
	return false, nil
}

// WakeProvisioningPipe connects a zero-byte client so a blocked accept observes
// shutdown (Rust ipc::wake).
func WakeProvisioningPipe(pipeName string, stopped func() bool) {
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return
	}
	for stopped == nil || !stopped() {
		handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			_ = windows.CloseHandle(handle)
			return
		}
		// A disconnected instance cannot accept the wakeup until ConnectNamedPipe
		// runs again.
		time.Sleep(25 * time.Millisecond)
	}
}
