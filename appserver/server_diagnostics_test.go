package appserver

import (
	"testing"
)

func TestServerDiagnosticsRegistryTracksAndSnapshots(t *testing.T) {
	registry := newServerDiagnosticsGaugeRegistry()
	release := registry.track("app.requests.in_flight")
	registry.track("app.requests.in_flight")
	if value := registry.gauge("app.requests.in_flight").Load(); value != 2 {
		t.Fatalf("in_flight = %d, want 2", value)
	}
	release()
	if value := registry.gauge("app.requests.in_flight").Load(); value != 1 {
		t.Fatalf("in_flight after release = %d, want 1", value)
	}
	snapshot := registry.snapshot()
	found := false
	for _, gauge := range snapshot {
		if gauge.Name == "app.requests.in_flight" && gauge.Value == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot = %+v, want in_flight=1", snapshot)
	}
}

func TestReadServerDiagnosticsReturnsProcessMetrics(t *testing.T) {
	registry := newServerDiagnosticsGaugeRegistry()
	response := readServerDiagnostics(registry)
	if response.Process.ID == 0 {
		t.Fatalf("process id = 0, want current pid")
	}
	if response.Process.ResidentMemoryBytes == nil || *response.Process.ResidentMemoryBytes == 0 {
		t.Fatalf("resident memory = %v, want non-zero", response.Process.ResidentMemoryBytes)
	}
	if response.Process.PhysicalFootprintBytes == nil {
		t.Fatalf("physical footprint = nil, want value (platform-dependent)")
	}
	if response.Gauges == nil {
		t.Fatalf("gauges = nil, want at least empty list")
	}
}

func TestServerDiagnosticsRequiresExperimentalAPI(t *testing.T) {
	router := newTestRuntimeRouter()
	reason := experimentalAPIReasonForRequest(&Request{Method: MethodServerDiagnostics})
	if reason != string(MethodServerDiagnostics) {
		t.Fatalf("experimental reason = %q, want %q", reason, MethodServerDiagnostics)
	}
	if !experimentalAPIMethod(MethodServerDiagnostics) {
		t.Fatal("MethodServerDiagnostics should be an experimental method")
	}
	router.rememberConnectionExperimentalAPI("conn-1", nil)
	if !router.connectionExperimentalAPIDisabled("conn-1") {
		t.Fatal("connection without experimentalApi should be disabled")
	}
	err := router.rejectExperimentalAPIDisabled(&Request{Method: MethodServerDiagnostics, ConnectionID: "conn-1"})
	if err == nil {
		t.Fatal("rejectExperimentalAPIDisabled() = nil, want error")
	}
}

func newTestRuntimeRouter() *RuntimeRouter {
	router := NewRuntimeRouter(RuntimeServices{})
	return router
}
