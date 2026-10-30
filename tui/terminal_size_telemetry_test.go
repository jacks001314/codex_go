package tui

import (
	"testing"
	"time"

	"codex_go/config"
	"codex_go/metrics"
	"codex_go/shell"
)

type consentRecorder struct {
	observations []recordedTerminalSize
}

type recordedTerminalSize struct {
	name             string
	inc              int
	tags             map[string]string
	analyticsEnabled bool
}

func (r *consentRecorder) Counter(string, int, map[string]string)                  {}
func (r *consentRecorder) Histogram(string, int, map[string]string)                {}
func (r *consentRecorder) RecordDuration(string, time.Duration, map[string]string) {}

func (r *consentRecorder) CounterWithAnalyticsConsent(name string, inc int, tags map[string]string, analyticsEnabled bool) {
	r.observations = append(r.observations, recordedTerminalSize{name: name, inc: inc, tags: tags, analyticsEnabled: analyticsEnabled})
}

// TestTerminalSizeTagsLikeRust pins the bucket boundaries from
// codex-rs/tui/src/terminal_size_telemetry.rs::tags.
func TestTerminalSizeTagsLikeRust(t *testing.T) {
	columns := []struct {
		width int
		want  string
	}{
		{0, "unknown"}, {1, "under-40"}, {39, "under-40"}, {40, "40-79"}, {79, "40-79"},
		{80, "80-119"}, {119, "80-119"}, {120, "120-239"}, {239, "120-239"},
		{240, "240-479"}, {479, "240-479"}, {480, "480-and-up"},
	}
	for _, tc := range columns {
		if got := TerminalSizeTags(tc.width, 30)[TerminalColumnsTag]; got != tc.want {
			t.Fatalf("width %d = %q, want %q", tc.width, got, tc.want)
		}
	}
	rows := []struct {
		height int
		want   string
	}{
		{0, "unknown"}, {1, "under-15"}, {14, "under-15"}, {15, "15-23"}, {23, "15-23"},
		{24, "24-39"}, {39, "24-39"}, {40, "40-79"}, {79, "40-79"}, {80, "80-119"},
		{119, "80-119"}, {120, "120-and-up"},
	}
	for _, tc := range rows {
		if got := TerminalSizeTags(100, tc.height)[TerminalRowsTag]; got != tc.want {
			t.Fatalf("height %d = %q, want %q", tc.height, got, tc.want)
		}
	}
}

// TestTerminalSizeTelemetryEnabledLikeRust pins the exporter/consent matrix.
func TestTerminalSizeTelemetryEnabledLikeRust(t *testing.T) {
	enabled := true
	disabled := false
	none := config.OtelExporterKind{Kind: config.OtelExporterKindNone}
	statsig := config.OtelExporterKind{Kind: config.OtelExporterKindStatsig}
	otlp := config.OtelExporterKind{Kind: config.OtelExporterKindOtlpHTTP}
	cases := []struct {
		name      string
		exporter  config.OtelExporterKind
		analytics *bool
		want      bool
	}{
		{"none", none, &enabled, false},
		{"statsig unset", statsig, nil, true},
		{"statsig enabled", statsig, &enabled, true},
		{"statsig disabled", statsig, &disabled, false},
		{"otlp unset", otlp, nil, true},
		{"otlp disabled", otlp, &disabled, true},
	}
	for _, tc := range cases {
		if got := TerminalSizeTelemetryEnabled(tc.exporter, tc.analytics); got != tc.want {
			t.Fatalf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestRecordTerminalSizeUsesLaunchContextLikeRust pins the observation tags.
func TestRecordTerminalSizeUsesLaunchContextLikeRust(t *testing.T) {
	recorder := &consentRecorder{}
	metrics.InstallGlobal(recorder)
	t.Cleanup(func() { metrics.InstallGlobal(nil) })

	RecordTerminalSize(map[string]string{"app_server_mode": "in_process"}, true, "turn_start", 100, 30)
	if len(recorder.observations) != 1 {
		t.Fatalf("observations = %#v", recorder.observations)
	}
	got := recorder.observations[0]
	if got.name != TerminalSizeMetric || got.inc != 1 || !got.analyticsEnabled {
		t.Fatalf("observation = %#v", got)
	}
	want := map[string]string{
		"app_server_mode":    "in_process",
		TerminalColumnsTag:   "80-119",
		TerminalRowsTag:      "24-39",
		"observation_reason": "turn_start",
	}
	if len(got.tags) != len(want) {
		t.Fatalf("tags = %#v", got.tags)
	}
	for key, value := range want {
		if got.tags[key] != value {
			t.Fatalf("tag %q = %q, want %q (all %#v)", key, got.tags[key], value, got.tags)
		}
	}

	// An empty launch context suppresses the observation (Rust's
	// `!context.is_empty()` guard).
	RecordTerminalSize(nil, true, "resize", 100, 30)
	if len(recorder.observations) != 1 {
		t.Fatalf("empty context recorded %#v", recorder.observations)
	}
}

// TestTerminalTelemetryDetectionsLikeRust pins the bounded terminal and
// multiplexer categories.
func TestTerminalTelemetryDetectionsLikeRust(t *testing.T) {
	info := &shell.TerminalInfo{Name: shell.TerminalWezTerm, Multiplexer: &shell.Multiplexer{Name: shell.MultiplexerTmux}}
	if got := TerminalTelemetryName(info); got != "wezterm" {
		t.Fatalf("terminal name = %q", got)
	}
	if got := TerminalMultiplexerTag(info); got != "tmux" {
		t.Fatalf("multiplexer = %q", got)
	}
	if got := TerminalTelemetryName(&shell.TerminalInfo{Name: shell.TerminalUnknown}); got != "unknown" {
		t.Fatalf("unknown terminal = %q", got)
	}
	// Rust #58270: Rex records the fixed "rex" category.
	if got := TerminalTelemetryName(&shell.TerminalInfo{Name: shell.TerminalRex}); got != "rex" {
		t.Fatalf("rex terminal = %q", got)
	}
	if got := TerminalMultiplexerTag(&shell.TerminalInfo{}); got != "none" {
		t.Fatalf("no multiplexer = %q", got)
	}
}
