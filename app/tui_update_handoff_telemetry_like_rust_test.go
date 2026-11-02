package app

import (
	"testing"

	"codex_go/cli"
	"codex_go/config"
	codextui "codex_go/tui"
)

// Rust #58766: accepting the startup update prompt records exactly one launch
// observation with app_server_mode=update_requested, and no embedded-start
// observation because no app server was started.
func TestInteractiveUpdateHandoffTelemetryLikeRust(t *testing.T) {
	writeTUITelemetryConfig(t)
	sink := &recordingTUISink{}
	previousSink := openTUIMetricSink
	openTUIMetricSink = func(cfg *config.Config) (tuiMetricSink, func(), bool) {
		if cfg == nil {
			t.Fatal("nil config")
		}
		return sink, func() {}, true
	}
	t.Cleanup(func() { openTUIMetricSink = previousSink })

	recordInteractiveUpdateHandoffTelemetry(&cli.RootOptions{})

	starts := 0
	for index := range sink.all {
		call := sink.all[index]
		switch call.name {
		case codextui.TUIStartMetric:
			starts++
			if call.inc != 1 {
				t.Fatalf("start increment = %d, want 1", call.inc)
			}
			if call.tags["app_server_mode"] != "update_requested" {
				t.Fatalf("app_server_mode = %q, want update_requested", call.tags["app_server_mode"])
			}
			for _, key := range []string{"daemon_selection_reason", "terminal_name", "multiplexer"} {
				if _, ok := call.tags[key]; !ok {
					t.Fatalf("start tags missing %q: %#v", key, call.tags)
				}
			}
		case codextui.TUIEmbeddedStartMetric:
			t.Fatalf("update handoff recorded an embedded start: %#v", call)
		}
	}
	if starts != 1 {
		t.Fatalf("launch observations = %d, want 1", starts)
	}
}
