//go:build windows

package provisionservice

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestWindowsTransportReportsUnavailableWithoutRunningService exercises the real
// SCM/pipe lookup: an absent or stopped service must map to ErrServiceUnavailable
// so the caller can fall back (except registered Core).
func TestWindowsTransportReportsUnavailableWithoutRunningService(t *testing.T) {
	status, err := queryServiceStatus()
	if err == nil && status.CurrentState == windows.SERVICE_RUNNING {
		t.Skip("a running Windows sandbox provisioning service is installed")
	}
	conn, connectErr := NewWindowsTransport().Connect(time.Now().Add(2 * time.Second))
	if conn != nil {
		_ = conn.Close()
		t.Fatalf("Connect() returned a connection: %v", conn)
	}
	if !errors.Is(connectErr, ErrServiceUnavailable) {
		t.Fatalf("Connect() error = %v, want ErrServiceUnavailable", connectErr)
	}
}
