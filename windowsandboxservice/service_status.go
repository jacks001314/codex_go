package windowsandboxservice

import (
	"strings"
	"unicode/utf16"
)

// Service control, state and event identifiers shared with the SCM host
// (Rust service.rs).
const (
	ServiceStopped      uint32 = 1
	ServiceStartPending uint32 = 2
	ServiceStopPending  uint32 = 3
	ServiceRunning      uint32 = 4

	ServiceAcceptStop          uint32 = 1
	ServiceAcceptShutdown      uint32 = 4
	ServiceAcceptSessionChange uint32 = 128

	ServiceControlStop          uint32 = 1
	ServiceControlInterrogate   uint32 = 4
	ServiceControlShutdown      uint32 = 5
	ServiceControlSessionChange uint32 = 14

	ServiceWin32OwnProcess uint32 = 16

	NoError                   uint32 = 0
	ErrorCallNotImplemented   uint32 = 120
	ErrorServiceSpecificError uint32 = 1066

	servicePendingWaitHint = 10_000
)

// Provisioning event-log identifiers (Rust service.rs EVENT_*).
const (
	EventServiceStarted        uint32 = 1000
	EventServiceStopRequested  uint32 = 1001
	EventServiceStopped        uint32 = 1002
	EventServiceFailed         uint32 = 1003
	EventProvisioningSucceeded uint32 = 2000
	EventProvisioningFailed    uint32 = 2001
	EventRequestRejected       uint32 = 2002
	EventCleanupStarted        uint32 = 3002
	EventCleanupFinished       uint32 = 3003
	EventCleanupDetail         uint32 = 3004

	// MaxEventMessageUnits bounds one event-log message in UTF-16 units.
	MaxEventMessageUnits = 1024
)

// ReportedServiceStatus is the SCM status frame derived from one state
// transition (Rust ServiceState::report_status).
type ReportedServiceStatus struct {
	CurrentState            uint32
	ControlsAccepted        uint32
	Win32ExitCode           uint32
	ServiceSpecificExitCode uint32
	CheckPoint              uint32
	WaitHint                uint32
}

// NewReportedServiceStatus builds the status reported for currentState: only a
// running service accepts stop, shutdown and session changes, a pending state
// advertises a 10 s wait hint with a non-zero checkpoint, and a service-specific
// exit code is flagged by reporting it as 1.
func NewReportedServiceStatus(currentState, win32ExitCode uint32) ReportedServiceStatus {
	status := ReportedServiceStatus{CurrentState: currentState, Win32ExitCode: win32ExitCode}
	if currentState == ServiceRunning {
		status.ControlsAccepted = ServiceAcceptStop | ServiceAcceptShutdown | ServiceAcceptSessionChange
	}
	if win32ExitCode == ErrorServiceSpecificError {
		status.ServiceSpecificExitCode = 1
	}
	if currentState == ServiceStartPending || currentState == ServiceStopPending {
		status.CheckPoint = 1
		status.WaitHint = servicePendingWaitHint
	}
	return status
}

// EventMessage makes an event-log message printable and bounded to
// MaxEventMessageUnits UTF-16 units (Rust service::log_event).
func EventMessage(message string) string {
	var builder strings.Builder
	units := 0
	for _, character := range message {
		if character < 0x20 || character == 0x7f {
			character = ' '
		}
		length := utf16.RuneLen(character)
		if length < 0 || units+length > MaxEventMessageUnits {
			break
		}
		builder.WriteRune(character)
		units += length
	}
	return builder.String()
}
