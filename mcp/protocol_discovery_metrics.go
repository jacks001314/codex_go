package mcp

import (
	"sync"
	"time"
)

// ProtocolDiscoveryMetrics mirrors Rust codex_otel observations recorded when
// an MCP client discovers the server protocol (Rust #38634): a counter and
// duration tagged with the configured mode ("legacy" or "auto") and the
// classified outcome ("modern", "legacy", or "failure").
type ProtocolDiscoveryMetrics struct {
	Mode       string
	Outcome    string
	DurationMS int64
}

type protocolDiscoveryMetricsObserverFunc func(ProtocolDiscoveryMetrics)

var protocolDiscoveryMetrics struct {
	sync.Mutex
	observer protocolDiscoveryMetricsObserverFunc
}

// SetProtocolDiscoveryMetricsObserver installs the observer used to record
// MCP protocol discovery metrics. A nil observer disables recording. Go has
// no codex_otel global, so callers (tests or telemetry wiring) supply the sink.
func SetProtocolDiscoveryMetricsObserver(observer func(ProtocolDiscoveryMetrics)) {
	protocolDiscoveryMetrics.Lock()
	defer protocolDiscoveryMetrics.Unlock()
	protocolDiscoveryMetrics.observer = observer
}

func recordProtocolDiscoveryMetrics(mode string, outcome string, duration time.Duration) {
	protocolDiscoveryMetrics.Lock()
	observer := protocolDiscoveryMetrics.observer
	protocolDiscoveryMetrics.Unlock()
	if observer == nil {
		return
	}
	observer(ProtocolDiscoveryMetrics{
		Mode:       mode,
		Outcome:    outcome,
		DurationMS: duration.Milliseconds(),
	})
}

func mcpProtocolDiscoveryModeLabel(mode MCPProtocolMode) string {
	if mode == MCPProtocol20260728 {
		return "auto"
	}
	return "legacy"
}
