package appserver

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"codex_go/mcp"
)

// ServerDiagnosticsResponse mirrors Rust v2::ServerDiagnosticsResponse
// (5729546839): content-free, process-local diagnostics.
type ServerDiagnosticsResponse struct {
	Process ServerDiagnosticsProcess `json:"process"`
	Gauges  []ServerDiagnosticsGauge `json:"gauges"`
}

type ServerDiagnosticsProcess struct {
	ID                     uint32  `json:"id"`
	ResidentMemoryBytes    *uint64 `json:"residentMemoryBytes"`
	PhysicalFootprintBytes *uint64 `json:"physicalFootprintBytes"`
}

type ServerDiagnosticsGauge struct {
	Name  string `json:"name"`
	Value uint64 `json:"value"`
}

// serverDiagnosticsGaugeRegistry mirrors Rust codex_diagnostics::snapshot:
// named gauges registered on first use and read on demand.
type serverDiagnosticsGaugeRegistry struct {
	gauges map[string]*atomic.Uint64
}

func newServerDiagnosticsGaugeRegistry() *serverDiagnosticsGaugeRegistry {
	return &serverDiagnosticsGaugeRegistry{gauges: map[string]*atomic.Uint64{}}
}

func (r *serverDiagnosticsGaugeRegistry) gauge(name string) *atomic.Uint64 {
	if r == nil {
		value := &atomic.Uint64{}
		return value
	}
	value, ok := r.gauges[name]
	if !ok {
		value = &atomic.Uint64{}
		r.gauges[name] = value
	}
	return value
}

func (r *serverDiagnosticsGaugeRegistry) track(name string) func() {
	value := r.gauge(name)
	value.Add(1)
	return func() { value.Add(^uint64(0)) }
}

func (r *serverDiagnosticsGaugeRegistry) snapshot() []ServerDiagnosticsGauge {
	if r == nil {
		return nil
	}
	out := make([]ServerDiagnosticsGauge, 0, len(r.gauges))
	for name, value := range r.gauges {
		out = append(out, ServerDiagnosticsGauge{Name: name, Value: value.Load()})
	}
	return out
}

// readServerDiagnostics mirrors Rust request_processors::read_server_diagnostics.
func readServerDiagnostics(gauges *serverDiagnosticsGaugeRegistry) ServerDiagnosticsResponse {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	var footprint uint64
	if workingSet, ok := processWorkingSetBytes(); ok {
		footprint = workingSet
	}
	resident := mem.Sys
	response := ServerDiagnosticsResponse{
		Process: ServerDiagnosticsProcess{
			ID:                     uint32(os.Getpid()),
			ResidentMemoryBytes:    &resident,
			PhysicalFootprintBytes: &footprint,
		},
	}
	if gauges != nil {
		response.Gauges = gauges.snapshot()
	}
	return response
}

func (r *RuntimeRouter) handleServerDiagnostics(request *Request) (*ServerDiagnosticsResponse, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: runtime router is nil", ErrInvalidRequest)
	}
	var params ServerDiagnosticsParams
	if request != nil {
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
	}
	// The diagnostics request itself is in flight.
	var gauges *serverDiagnosticsGaugeRegistry
	if r.diagnosticsGauges != nil {
		gauges = r.diagnosticsGauges
		defer gauges.track("app.requests.in_flight")()
		gauges.gauge("mcp.connections.live").Store(uint64(r.liveMCPConnectionCount()))
	}
	response := readServerDiagnostics(gauges)
	return &response, nil
}

func (r *RuntimeRouter) liveMCPConnectionCount() int {
	if r == nil || r.services.MCP == nil {
		return 0
	}
	status, err := r.services.MCP.ListStatusChecked(&mcp.MCPListServerStatusParams{})
	if err != nil || status == nil {
		return 0
	}
	count := 0
	for i := range status.Data {
		if status.Data[i].State == mcp.MCPServerReady {
			count++
		}
	}
	return count
}

// ServerDiagnosticsParams mirrors Rust v2::ServerDiagnosticsParams (empty).
type ServerDiagnosticsParams struct{}
