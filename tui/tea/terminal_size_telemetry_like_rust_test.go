package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/config"
	"codex_go/protocol"
	codextui "codex_go/tui"
)

type terminalSizeObservation struct {
	context          map[string]string
	analyticsEnabled bool
	reason           string
	width            int
	height           int
}

func newTerminalSizeTestModel(t *testing.T, exporter config.OtelExporterKind, analytics *bool, reader func() (int, int)) (*Model, *[]terminalSizeObservation) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	observations := &[]terminalSizeObservation{}
	model := NewModel(state, Options{
		Width:                        80,
		Height:                       24,
		TerminalSizeContext:          map[string]string{"app_server_mode": "in_process", "daemon_selection_reason": "auto_start_disabled"},
		TerminalSizeExporter:         exporter,
		TerminalSizeAnalyticsEnabled: analytics,
		TerminalSizeReader:           reader,
		TerminalSizeSettleDelay:      0,
		OnTerminalSizeObservation: func(context map[string]string, analyticsEnabled bool, reason string, width int, height int) {
			*observations = append(*observations, terminalSizeObservation{
				context:          context,
				analyticsEnabled: analyticsEnabled,
				reason:           reason,
				width:            width,
				height:           height,
			})
		},
	})
	return model, observations
}

// TestTerminalSizeTurnStartLikeRust mirrors Rust #53859 thread routing: a new
// turn in the active thread records turn_start, repeated notifications for the
// same turn do not, and another thread's turn does not.
func TestTerminalSizeTurnStartLikeRust(t *testing.T) {
	reader := func() (int, int) { return 100, 30 }
	model, observations := newTerminalSizeTestModel(t, config.OtelExporterKind{Kind: config.OtelExporterKindOtlpHTTP}, nil, reader)

	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"}})
	if len(*observations) != 1 {
		t.Fatalf("observations = %#v", *observations)
	}
	got := (*observations)[0]
	if got.reason != "turn_start" || got.width != 100 || got.height != 30 || !got.analyticsEnabled {
		t.Fatalf("observation = %#v", got)
	}
	if got.context["app_server_mode"] != "in_process" {
		t.Fatalf("context = %#v", got.context)
	}

	// A repeated notification for the current turn is suppressed.
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"}})
	if len(*observations) != 1 {
		t.Fatalf("duplicate turn recorded: %#v", *observations)
	}

	// A turn in another thread is ignored.
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-2", TurnID: "turn-2"}})
	if len(*observations) != 1 {
		t.Fatalf("foreign thread recorded: %#v", *observations)
	}

	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-3"}})
	if len(*observations) != 2 || (*observations)[1].reason != "turn_start" {
		t.Fatalf("next turn observations = %#v", *observations)
	}
}

// TestTerminalSizeResizeSettlesLikeRust mirrors Rust's 15-second settle: a
// resize records only after the geometry stays stable, and only when the
// buckets changed.
func TestTerminalSizeResizeSettlesLikeRust(t *testing.T) {
	size := [2]int{80, 24}
	reader := func() (int, int) { return size[0], size[1] }
	model, observations := newTerminalSizeTestModel(t, config.OtelExporterKind{Kind: config.OtelExporterKindOtlpHTTP}, nil, reader)

	// Resize to a new bucket range; the settle check records it.
	size = [2]int{120, 40}
	model.Update(bubbletea.WindowSizeMsg{Width: 120, Height: 40})
	if model.terminalSizePending == nil {
		t.Fatal("resize did not arm the settle window")
	}
	if len(*observations) != 0 {
		t.Fatalf("recorded before settling: %#v", *observations)
	}
	model.applyTerminalSizeSettle(model.terminalSizeGeneration)
	if len(*observations) != 1 {
		t.Fatalf("observations = %#v", *observations)
	}
	if got := (*observations)[0]; got.reason != "resize" || got.width != 120 || got.height != 40 {
		t.Fatalf("resize observation = %#v", got)
	}

	// A resize within the same buckets updates the settled tags silently.
	size = [2]int{130, 45}
	model.Update(bubbletea.WindowSizeMsg{Width: 130, Height: 45})
	model.applyTerminalSizeSettle(model.terminalSizeGeneration)
	if len(*observations) != 1 {
		t.Fatalf("same-bucket resize recorded: %#v", *observations)
	}

	// A geometry that moved again before the deadline re-arms instead of
	// recording the stale size.
	size = [2]int{200, 60}
	model.Update(bubbletea.WindowSizeMsg{Width: 200, Height: 60})
	stale := model.terminalSizeGeneration
	size = [2]int{500, 130}
	if cmd := model.applyTerminalSizeSettle(stale); cmd == nil {
		t.Fatal("moved geometry did not re-arm the settle window")
	}
	if len(*observations) != 1 {
		t.Fatalf("stale geometry recorded: %#v", *observations)
	}
	model.applyTerminalSizeSettle(model.terminalSizeGeneration)
	if len(*observations) != 2 || (*observations)[1].width != 500 || (*observations)[1].height != 130 {
		t.Fatalf("settled geometry observations = %#v", *observations)
	}
}

// TestTerminalSizeTelemetryEligibilityLikeRust pins the exporter/consent gate:
// a disabled Statsig route records nothing, while an explicit OTLP collector
// records even with analytics disabled.
func TestTerminalSizeTelemetryEligibilityLikeRust(t *testing.T) {
	disabled := false
	statsigModel, statsigObservations := newTerminalSizeTestModel(t,
		config.OtelExporterKind{Kind: config.OtelExporterKindStatsig}, &disabled, func() (int, int) { return 100, 30 })
	statsigModel.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"}})
	if len(*statsigObservations) != 0 {
		t.Fatalf("consentless Statsig recorded: %#v", *statsigObservations)
	}

	otlpModel, otlpObservations := newTerminalSizeTestModel(t,
		config.OtelExporterKind{Kind: config.OtelExporterKindOtlpHTTP}, &disabled, func() (int, int) { return 100, 30 })
	otlpModel.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"}})
	if len(*otlpObservations) != 1 || (*otlpObservations)[0].analyticsEnabled {
		t.Fatalf("explicit OTLP observations = %#v", *otlpObservations)
	}

	// With no launch context there is nothing to observe, even when enabled.
	noneModel, noneObservations := newTerminalSizeTestModel(t,
		config.OtelExporterKind{Kind: config.OtelExporterKindOtlpHTTP}, nil, func() (int, int) { return 100, 30 })
	noneModel.terminalSizeContext = nil
	noneModel.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-1"}})
	if len(*noneObservations) != 0 {
		t.Fatalf("empty context recorded: %#v", *noneObservations)
	}
}
