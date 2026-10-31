//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

// ServiceBroker serves provisioning requests until the service stops. The SCM
// host reports running and the listener loop belongs here.
type ServiceBroker func(state *ServiceState) error

// ServiceState is the process-wide service record (Rust service::ServiceState).
type ServiceState struct {
	serviceName   string
	pipeName      string
	shutdown      atomic.Bool
	stopRequested atomic.Bool
	ownerRemoved  atomic.Bool
	currentStatus atomic.Uint32
	statusHandle  windows.Handle
}

// ServiceName returns the SCM name of this package family.
func (state *ServiceState) ServiceName() string { return state.serviceName }

// PipeName returns the provisioning pipe this service owns.
func (state *ServiceState) PipeName() string { return state.pipeName }

// Shutdown reports whether a stop or shutdown control was handled.
func (state *ServiceState) Shutdown() bool { return state.shutdown.Load() }

// StopRequested reports whether the SCM asked for a stop rather than a shutdown.
func (state *ServiceState) StopRequested() bool { return state.stopRequested.Load() }

// OwnerRemoved reports whether the service retired the sandbox because its
// owner uninstalled the app (Rust ServiceStopReason::OwnerRemoved).
func (state *ServiceState) OwnerRemoved() bool { return state.ownerRemoved.Load() }

// CurrentStatus returns the last state reported to the SCM.
func (state *ServiceState) CurrentStatus() uint32 { return state.currentStatus.Load() }

var (
	serviceState  atomic.Pointer[ServiceState]
	serviceBroker ServiceBroker

	serviceMainCallback    = windows.NewCallback(serviceMainProcedure)
	serviceControlCallback = windows.NewCallback(serviceControlProcedure)
)

// RunService registers the SCM dispatcher for the active package family and
// blocks until the service stops (Rust service::run).
func RunService(broker ServiceBroker) error {
	if broker == nil {
		return errors.New("the sandbox provisioning broker is required")
	}
	serviceName, err := provisionservice.ServiceName()
	if err != nil {
		return err
	}
	pipeName, err := provisionservice.ServicePipeName()
	if err != nil {
		return err
	}
	state := &ServiceState{serviceName: serviceName, pipeName: pipeName}
	state.currentStatus.Store(ServiceStartPending)
	if !serviceState.CompareAndSwap(nil, state) {
		return errors.New("the service dispatcher was already initialized")
	}
	serviceBroker = broker
	name, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		serviceState.Store(nil)
		return err
	}
	// The table and its name buffer stay alive until the dispatcher returns.
	table := []windows.SERVICE_TABLE_ENTRY{
		{ServiceName: name, ServiceProc: serviceMainCallback},
		{ServiceName: nil, ServiceProc: 0},
	}
	if err := windows.StartServiceCtrlDispatcher(&table[0]); err != nil {
		serviceState.Store(nil)
		return fmt.Errorf("start the Windows service dispatcher: %w", err)
	}
	return nil
}

func serviceMainProcedure(_ uintptr, _ uintptr) uintptr {
	state := serviceState.Load()
	if state == nil {
		return 0
	}
	provisionservice.ServiceStopReasonStarting.Record(0)
	if err := runServiceMain(state); err != nil {
		reason := provisionservice.ServiceStopReasonBrokerFailed
		if state.CurrentStatus() == ServiceStartPending {
			reason = provisionservice.ServiceStopReasonStartupFailed
		}
		reason.Record(FatalErrorHResult(err))
		LogError(EventServiceFailed, "The Codex sandbox service encountered a fatal error: "+FatalErrorMessage(err))
		_, _ = fmt.Fprintf(os.Stderr, "%s failed: %v\n", state.serviceName, err)
		if state.statusHandle != 0 {
			_ = state.ReportStatus(ServiceStopped, ErrorServiceSpecificError)
		}
	}
	return 0
}

func runServiceMain(state *ServiceState) error {
	name, err := windows.UTF16PtrFromString(state.serviceName)
	if err != nil {
		return err
	}
	// The service controller only reads the name during registration.
	handle, err := windows.RegisterServiceCtrlHandlerEx(name, serviceControlCallback, 0)
	if err != nil {
		return fmt.Errorf("register the service control handler: %w", err)
	}
	state.statusHandle = handle
	if err := state.ReportStatus(ServiceStartPending, NoError); err != nil {
		return err
	}
	broker := serviceBroker
	if broker == nil {
		return errors.New("the sandbox provisioning broker is not wired")
	}
	if err := broker(state); err != nil {
		return err
	}
	stopped := provisionservice.ServiceStopReasonShutdown
	switch {
	case state.OwnerRemoved():
		stopped = provisionservice.ServiceStopReasonOwnerRemoved
	case state.StopRequested():
		stopped = provisionservice.ServiceStopReasonStopRequested
	}
	stopped.Record(0)
	LogInformation(EventServiceStopped, "The Codex sandbox service has stopped.")
	return state.ReportStatus(ServiceStopped, NoError)
}

func serviceControlProcedure(control uintptr, _ uintptr, _ uintptr, _ uintptr) uintptr {
	state := serviceState.Load()
	if state == nil {
		return uintptr(ErrorCallNotImplemented)
	}
	switch uint32(control) {
	case ServiceControlStop, ServiceControlShutdown:
		if uint32(control) == ServiceControlStop {
			state.stopRequested.Store(true)
		}
		if !state.shutdown.Swap(true) {
			if err := state.ReportStatus(ServiceStopPending, NoError); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "unable to report service shutdown: %v\n", err)
			}
			LogInformation(EventServiceStopRequested, "The Codex sandbox service was asked to stop.")
			WakeListener()
		}
		return uintptr(NoError)
	case ServiceControlInterrogate:
		if err := state.ReportStatus(state.CurrentStatus(), NoError); err != nil {
			LogError(EventServiceFailed, "The Codex sandbox service could not report its current status.")
			_, _ = fmt.Fprintf(os.Stderr, "unable to report the current service status: %v\n", err)
		}
		return uintptr(NoError)
	case ServiceControlSessionChange:
		WakeListener()
		return uintptr(NoError)
	default:
		return uintptr(ErrorCallNotImplemented)
	}
}

// ReportStatus publishes one SCM status frame and records it as current.
func (state *ServiceState) ReportStatus(currentState, win32ExitCode uint32) error {
	if state.statusHandle == 0 {
		return errors.New("the service status handle was not registered")
	}
	reported := NewReportedServiceStatus(currentState, win32ExitCode)
	status := windows.SERVICE_STATUS{
		ServiceType:             ServiceWin32OwnProcess,
		CurrentState:            reported.CurrentState,
		ControlsAccepted:        reported.ControlsAccepted,
		Win32ExitCode:           reported.Win32ExitCode,
		ServiceSpecificExitCode: reported.ServiceSpecificExitCode,
		CheckPoint:              reported.CheckPoint,
		WaitHint:                reported.WaitHint,
	}
	if err := windows.SetServiceStatus(state.statusHandle, &status); err != nil {
		return fmt.Errorf("update the Windows service status: %w", err)
	}
	state.currentStatus.Store(currentState)
	return nil
}

// WakeListener unblocks the listener so it can observe shutdown (Rust
// service::wake_listener).
func WakeListener() {
	state := serviceState.Load()
	if state == nil {
		return
	}
	go WakeProvisioningPipe(state.pipeName, func() bool {
		return state.CurrentStatus() == ServiceStopped
	})
}

// LogInformation writes one informational provisioning event.
func LogInformation(eventID uint32, message string) {
	logEvent(windows.EVENTLOG_INFORMATION_TYPE, eventID, message)
}

// LogError writes one error provisioning event.
func LogError(eventID uint32, message string) {
	logEvent(windows.EVENTLOG_ERROR_TYPE, eventID, message)
}

func logEvent(eventType uint16, eventID uint32, message string) {
	sourceName := "CodexSandboxService"
	if state := serviceState.Load(); state != nil {
		sourceName = state.serviceName
	}
	source, err := windows.UTF16PtrFromString(sourceName)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "unable to open the Windows event log: %s\n", message)
		return
	}
	eventLog, err := windows.RegisterEventSource(nil, source)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "unable to open the Windows event log: %s\n", message)
		return
	}
	defer windows.DeregisterEventSource(eventLog)
	text, err := windows.UTF16PtrFromString(EventMessage(message))
	if err != nil {
		return
	}
	if err := windows.ReportEvent(eventLog, eventType, 0, eventID, 0, 1, 0, &text, nil); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "unable to write a Windows event-log entry (event %d)\n", eventID)
	}
}

// FatalErrorMessage is the printable form used for the fatal service event.
func FatalErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return EventMessage(err.Error())
}

// FatalErrorHResult extracts a Win32 code from the error chain as an HRESULT
// without stringifying the error (Rust service::fatal_error_hresult).
func FatalErrorHResult(err error) uint32 {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if code := uint32(errno); code != 0 {
			return code | 0x80070000
		}
	}
	return 0
}
