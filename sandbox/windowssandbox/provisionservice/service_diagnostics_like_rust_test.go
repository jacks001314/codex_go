//go:build windows

package provisionservice

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"codex_go/metrics"
)

type counterCall struct {
	name string
	inc  int
	tags map[string]string
}

type recordingRecorder struct {
	counters []counterCall
}

func (r *recordingRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *recordingRecorder) Counter(name string, inc int, tags map[string]string) {
	r.counters = append(r.counters, counterCall{name: name, inc: inc, tags: tags})
}

func (r *recordingRecorder) Histogram(string, int, map[string]string) {}

func TestServiceStopRecordValuePacksReasonAndHResult(t *testing.T) {
	for _, testCase := range []struct {
		reason  ServiceStopReason
		hresult uint32
		want    uint64
	}{
		{reason: ServiceStopReasonStarting, hresult: 0, want: 1 << 32},
		{reason: ServiceStopReasonStopRequested, hresult: 0, want: 2 << 32},
		{reason: ServiceStopReasonBrokerFailed, hresult: 0x80070005, want: 6<<32 | 0x80070005},
	} {
		if got := serviceStopRecordValue(testCase.reason, testCase.hresult); got != testCase.want {
			t.Fatalf("serviceStopRecordValue(%d, %#x) = %#x, want %#x", testCase.reason, testCase.hresult, got, testCase.want)
		}
	}
}

func TestServiceStopReasonLabels(t *testing.T) {
	for reason, want := range map[uint32]string{
		0: "unknown",
		1: "no_stop_recorded",
		2: "stop_requested",
		3: "shutdown",
		4: "owner_removed",
		5: "startup_failed",
		6: "broker_failed",
		7: "registration_interrupted",
		9: "unknown",
	} {
		if got := serviceStopReasonLabel(reason); got != want {
			t.Fatalf("serviceStopReasonLabel(%d) = %q, want %q", reason, got, want)
		}
	}
}

func TestServiceStopKeyPathTargetsTheServiceKey(t *testing.T) {
	want := "SYSTEM\\CurrentControlSet\\Services\\CodexSandboxService"
	if got := serviceStopKeyPath("CodexSandboxService"); got != want {
		t.Fatalf("serviceStopKeyPath() = %q, want %q", got, want)
	}
}

func TestServiceStopReasonRecordWritesCodexLastStop(t *testing.T) {
	const testPath = "Software\\CodexGoTests\\ServiceStopRecord"
	key, _, err := registry.CreateKey(registry.CURRENT_USER, testPath, registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	_ = key.Close()
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, testPath) })
	previous := openServiceStopKey
	openServiceStopKey = func(string) (registry.Key, error) {
		return registry.OpenKey(registry.CURRENT_USER, testPath, registry.SET_VALUE|registry.QUERY_VALUE)
	}
	t.Cleanup(func() { openServiceStopKey = previous })

	ServiceStopReasonOwnerRemoved.Record(0x8007000e)

	read, err := registry.OpenKey(registry.CURRENT_USER, testPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("OpenKey() error = %v", err)
	}
	defer func() { _ = read.Close() }()
	got, _, err := read.GetIntegerValue(CodexLastStopValue)
	if err != nil {
		t.Fatalf("GetIntegerValue(%s) error = %v", CodexLastStopValue, err)
	}
	if want := serviceStopRecordValue(ServiceStopReasonOwnerRemoved, 0x8007000e); got != want {
		t.Fatalf("CodexLastStop = %#x, want %#x", got, want)
	}
}

func TestServiceStopReasonRecordIgnoresAMissingServiceKey(t *testing.T) {
	previous := openServiceStopKey
	openServiceStopKey = func(string) (registry.Key, error) {
		return registry.Key(0), errors.New("the service key does not exist")
	}
	t.Cleanup(func() { openServiceStopKey = previous })
	ServiceStopReasonStartupFailed.Record(0)
}

func recordServiceStopStub(t *testing.T, value uint64) {
	t.Helper()
	previous := readServiceStopRecordValue
	readServiceStopRecordValue = func(string) uint64 { return value }
	t.Cleanup(func() { readServiceStopRecordValue = previous })
}

func TestRecordServiceStoppedReportsAMatchingObservation(t *testing.T) {
	recordServiceStopStub(t, serviceStopRecordValue(ServiceStopReasonOwnerRemoved, 0x80070005))
	status := windows.SERVICE_STATUS_PROCESS{
		CurrentState:            windows.SERVICE_STOPPED,
		Win32ExitCode:           1066,
		ServiceSpecificExitCode: 7,
	}
	recorder := &recordingRecorder{}
	previous := metrics.InstallGlobal(recorder)
	t.Cleanup(func() { metrics.InstallGlobal(previous) })
	RecordServiceStopped(status, func() (windows.SERVICE_STATUS_PROCESS, error) { return status, nil })
	if len(recorder.counters) != 1 {
		t.Fatalf("recorded %d counters, want 1", len(recorder.counters))
	}
	call := recorder.counters[0]
	if call.name != "codex.windows_sandbox.service_stopped" || call.inc != 1 {
		t.Fatalf("counter = %+v", call)
	}
	if call.tags["last_reported_reason"] != "owner_removed" ||
		call.tags["last_reported_hresult"] != "0x80070005" ||
		call.tags["win32_exit_code"] != "1066" ||
		call.tags["service_exit_code"] != "7" {
		t.Fatalf("counter tags = %+v", call.tags)
	}
}

func TestRecordServiceStoppedSkipsAnInconsistentObservation(t *testing.T) {
	status := windows.SERVICE_STATUS_PROCESS{
		CurrentState:            windows.SERVICE_STOPPED,
		Win32ExitCode:           1066,
		ServiceSpecificExitCode: 7,
	}
	for _, testCase := range []struct {
		name    string
		requery func() (windows.SERVICE_STATUS_PROCESS, error)
		reads   []uint64
	}{
		{
			name: "running again",
			requery: func() (windows.SERVICE_STATUS_PROCESS, error) {
				return windows.SERVICE_STATUS_PROCESS{CurrentState: windows.SERVICE_RUNNING}, nil
			},
		},
		{
			name: "different exit code",
			requery: func() (windows.SERVICE_STATUS_PROCESS, error) {
				changed := status
				changed.Win32ExitCode = 0
				return changed, nil
			},
		},
		{
			name: "query failed",
			requery: func() (windows.SERVICE_STATUS_PROCESS, error) {
				return windows.SERVICE_STATUS_PROCESS{}, errors.New("service is gone")
			},
		},
		{
			name:    "record replaced",
			requery: func() (windows.SERVICE_STATUS_PROCESS, error) { return status, nil },
			reads:   []uint64{serviceStopRecordValue(ServiceStopReasonOwnerRemoved, 0), serviceStopRecordValue(ServiceStopReasonStopRequested, 0)},
		},
	} {
		recorder := &recordingRecorder{}
		previousMetrics := metrics.InstallGlobal(recorder)
		reads := testCase.reads
		if len(reads) == 0 {
			reads = []uint64{serviceStopRecordValue(ServiceStopReasonOwnerRemoved, 0)}
		}
		index := 0
		previousValue := readServiceStopRecordValue
		readServiceStopRecordValue = func(string) uint64 {
			if index < len(reads) {
				index++
				return reads[index-1]
			}
			return reads[len(reads)-1]
		}
		RecordServiceStopped(status, testCase.requery)
		readServiceStopRecordValue = previousValue
		metrics.InstallGlobal(previousMetrics)
		if len(recorder.counters) != 0 {
			t.Fatalf("%s: recorded %d counters, want 0", testCase.name, len(recorder.counters))
		}
	}
}
