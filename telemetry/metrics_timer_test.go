package telemetry

import (
	"context"
	"testing"
	"time"
)

// A stopped timer records the elapsed milliseconds under the timer's tags, with
// the timer's own tags overriding an identical additional tag (Rust's
// Timer::record merges the timer tags last).
func TestTimerRecordsElapsedMillisecondsLikeRust(t *testing.T) {
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	timer := client.StartTimer("codex.turn.e2e_duration_ms", map[string]string{
		"source": "timer",
		"turn":   "t1",
	})
	time.Sleep(2 * time.Millisecond)
	timer.Record(map[string]string{"source": "additional", "tool": "shell"})

	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	metrics := metricsByName(t, doer.snapshot()[0])
	histogram, ok := metrics["codex.turn.e2e_duration_ms"]["histogram"].(map[string]any)
	if !ok {
		t.Fatalf("metrics = %#v", metrics)
	}
	point := histogram["dataPoints"].([]any)[0].(map[string]any)
	if point["count"] != "1" || point["sum"].(float64) <= 0 {
		t.Fatalf("timer point = %#v", point)
	}
	attributes := attributeMap(point["attributes"])
	if attributes["turn"] != "t1" || attributes["tool"] != "shell" || attributes["source"] != "timer" {
		t.Fatalf("timer attributes = %#v", attributes)
	}
}

// Stop records with the timer's tags only, mirroring Rust's Drop impl.
func TestTimerStopRecordsWithoutAdditionalTags(t *testing.T) {
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	timer := client.StartTimer("codex.turn.e2e_duration_ms", map[string]string{"turn": "t1"})
	timer.Stop()
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	metrics := metricsByName(t, doer.snapshot()[0])
	histogram := metrics["codex.turn.e2e_duration_ms"]["histogram"].(map[string]any)
	point := histogram["dataPoints"].([]any)[0].(map[string]any)
	if attributes := attributeMap(point["attributes"]); attributes["turn"] != "t1" {
		t.Fatalf("timer attributes = %#v", attributes)
	}
}
