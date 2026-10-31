//go:build !windows

package provisionservice

import "time"

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
