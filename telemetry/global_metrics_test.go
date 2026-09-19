package telemetry

import (
	"context"
	"testing"
)

// Mirrors Rust's `start_global_timer`: the timer records through the globally
// installed metrics client, and is a no-op when none is installed.
func TestStartGlobalTimerUsesInstalledMetricsClientLikeRust(t *testing.T) {
	InstallGlobalMetrics(nil)
	t.Cleanup(func() { InstallGlobalMetrics(nil) })

	if timer := StartGlobalTimer("codex.remote_models.fetch_update.duration_ms", nil); timer != nil {
		t.Fatal("StartGlobalTimer returned a timer without an installed client")
	}

	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	if installed := InstallGlobalMetrics(client); installed != client {
		t.Fatalf("InstallGlobalMetrics returned %#v", installed)
	}
	timer := StartGlobalTimer("codex.remote_models.fetch_update.duration_ms", map[string]string{"auth_mode": "api_key"})
	if timer == nil {
		t.Fatal("StartGlobalTimer returned nil with an installed client")
	}
	timer.Stop()
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	metrics := metricsByName(t, doer.snapshot()[0])
	histogram, ok := metrics["codex.remote_models.fetch_update.duration_ms"]["histogram"].(map[string]any)
	if !ok {
		t.Fatalf("metrics = %#v", metrics)
	}
	point := histogram["dataPoints"].([]any)[0].(map[string]any)
	if attributes := attributeMap(point["attributes"]); attributes["auth_mode"] != "api_key" {
		t.Fatalf("timer attributes = %#v", attributes)
	}
}
