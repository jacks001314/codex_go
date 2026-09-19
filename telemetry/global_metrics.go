package telemetry

import "codex_go/metrics"

// InstallGlobalMetrics installs the built metrics client as the process-global
// recorder (Rust OtelProvider::try_new installs its client globally), so library
// code without a client handle can still record global metrics. A nil client
// clears the global, which tests use to avoid leaking a client between cases.
func InstallGlobalMetrics(client *MetricsClient) *MetricsClient {
	if client == nil {
		metrics.InstallGlobal(nil)
		return nil
	}
	metrics.InstallGlobal(client)
	return client
}

// StartGlobalTimer starts a timer on the process-global metrics client
// (Rust `codex_otel::start_global_timer`). It returns nil when no client is
// installed; Timer.Stop is nil-safe, so a caller may always defer Stop.
func StartGlobalTimer(name string, tags map[string]string) *Timer {
	recorder := metrics.Global()
	if recorder == nil {
		return nil
	}
	client, ok := recorder.(*MetricsClient)
	if !ok {
		return nil
	}
	return client.StartTimer(name, tags)
}
