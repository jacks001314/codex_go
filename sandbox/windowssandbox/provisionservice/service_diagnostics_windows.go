//go:build windows

package provisionservice

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"codex_go/metrics"
)

// CodexLastStopValue is the SCM service-key value holding the last recorded
// stop reason and HRESULT (Rust service_diagnostics).
const CodexLastStopValue = "CodexLastStop"

// ServiceStopReason identifies why the sandbox service stopped (Rust
// ServiceStopReason).
type ServiceStopReason uint32

const (
	ServiceStopReasonStarting                ServiceStopReason = 1
	ServiceStopReasonStopRequested           ServiceStopReason = 2
	ServiceStopReasonShutdown                ServiceStopReason = 3
	ServiceStopReasonOwnerRemoved            ServiceStopReason = 4
	ServiceStopReasonStartupFailed           ServiceStopReason = 5
	ServiceStopReasonBrokerFailed            ServiceStopReason = 6
	ServiceStopReasonRegistrationInterrupted ServiceStopReason = 7
)

var (
	// openServiceStopKey opens the SCM service key for writing. The key must
	// already exist: Windows removes it while uninstalling and the record must
	// never recreate it.
	openServiceStopKey = func(serviceName string) (registry.Key, error) {
		return registry.OpenKey(registry.LOCAL_MACHINE, serviceStopKeyPath(serviceName), registry.SET_VALUE)
	}
	// readServiceStopRecordValue reads the advisory stop record, reporting 0 when
	// the value is missing or malformed (Rust RRF_ZEROONFAILURE).
	readServiceStopRecordValue = func(serviceName string) uint64 {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, serviceStopKeyPath(serviceName), registry.QUERY_VALUE)
		if err != nil {
			return 0
		}
		defer func() { _ = key.Close() }()
		value, _, err := key.GetIntegerValue(CodexLastStopValue)
		if err != nil {
			return 0
		}
		return value
	}
)

func serviceStopKeyPath(serviceName string) string {
	return "SYSTEM\\CurrentControlSet\\Services\\" + serviceName
}

// Record stores (reason<<32)|hresult as CodexLastStop. The service owns the key
// and a failure must not affect its lifecycle, so every error is ignored.
func (reason ServiceStopReason) Record(hresult uint32) {
	serviceName, err := ServiceName()
	if err != nil {
		return
	}
	key, err := openServiceStopKey(serviceName)
	if err != nil {
		return
	}
	defer func() { _ = key.Close() }()
	_ = key.SetQWordValue(CodexLastStopValue, serviceStopRecordValue(reason, hresult))
}

// RecordServiceStopped reports one stopped-service observation
// (Rust service_diagnostics::record_stopped). A concurrent restart can replace
// the registry record after the first query, so it is only paired with the SCM
// evidence when the stopped state and both exit codes still match.
func RecordServiceStopped(status windows.SERVICE_STATUS_PROCESS, requery func() (windows.SERVICE_STATUS_PROCESS, error)) {
	if requery == nil {
		return
	}
	serviceName, err := ServiceName()
	if err != nil {
		return
	}
	value := readServiceStopRecordValue(serviceName)
	current, err := requery()
	if err != nil ||
		current.CurrentState != windows.SERVICE_STOPPED ||
		current.Win32ExitCode != status.Win32ExitCode ||
		current.ServiceSpecificExitCode != status.ServiceSpecificExitCode ||
		readServiceStopRecordValue(serviceName) != value {
		return
	}
	// This counts observations, not unique outages. The SCM exit code also
	// covers crashes and kills that cannot write a final record.
	metrics.Counter("codex.windows_sandbox.service_stopped", 1, map[string]string{
		"last_reported_reason":  serviceStopReasonLabel(uint32(value >> 32)),
		"last_reported_hresult": fmt.Sprintf("0x%08x", uint32(value)),
		"win32_exit_code":       strconv.FormatUint(uint64(status.Win32ExitCode), 10),
		"service_exit_code":     strconv.FormatUint(uint64(status.ServiceSpecificExitCode), 10),
	})
}

func serviceStopRecordValue(reason ServiceStopReason, hresult uint32) uint64 {
	return uint64(reason)<<32 | uint64(hresult)
}

func serviceStopReasonLabel(reason uint32) string {
	switch ServiceStopReason(reason) {
	case ServiceStopReasonStarting:
		return "no_stop_recorded"
	case ServiceStopReasonStopRequested:
		return "stop_requested"
	case ServiceStopReasonShutdown:
		return "shutdown"
	case ServiceStopReasonOwnerRemoved:
		return "owner_removed"
	case ServiceStopReasonStartupFailed:
		return "startup_failed"
	case ServiceStopReasonBrokerFailed:
		return "broker_failed"
	case ServiceStopReasonRegistrationInterrupted:
		return "registration_interrupted"
	default:
		return "unknown"
	}
}
