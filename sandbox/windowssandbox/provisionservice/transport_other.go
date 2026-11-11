//go:build !windows

package provisionservice

import (
	"path/filepath"
	"time"
)

// WindowsTransport is inert outside Windows: there is no provisioning service.
type WindowsTransport struct{}

// NewWindowsTransport returns a transport that always reports unavailability.
func NewWindowsTransport() *WindowsTransport {
	return &WindowsTransport{}
}

func (t *WindowsTransport) Connect(deadline time.Time) (Conn, error) {
	return nil, ErrServiceUnavailable
}

func (t *WindowsTransport) ConnectAfterRestart(previousPID uint32, deadline time.Time) (Conn, error) {
	return nil, ErrServiceUnavailable
}

// StartServiceForSetup is a no-op outside Windows.
func StartServiceForSetup() error {
	return nil
}

// canonicalizeProvisioningHome resolves the request home the way the Windows
// client does. Other platforms have no provisioning service, so this only keeps
// the client's shared code compiling with the same signature.
func canonicalizeProvisioningHome(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return path
	}
	return filepath.Clean(resolved)
}
