package tea

import (
	"context"
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
)

// Mirrors codex-rs/tui/src/statusline_metrics.rs and statusline_metrics_tests.rs:
// the counters only reach the launch metrics client when the exporter that
// built it is still eligible, and the built-in Statsig exporter additionally
// demands the default environment, a bounded originator, and a launch without
// custom OTEL resource attributes or collector headers (Rust #62041).

type statuslineMetricCall struct {
	name    string
	inc     int
	tags    map[string]string
	consent bool
}

type recordingStatuslineSink struct {
	calls []statuslineMetricCall
}

func (s *recordingStatuslineSink) CounterWithAnalyticsConsent(name string, inc int, tags map[string]string, analyticsEnabled bool) {
	s.calls = append(s.calls, statuslineMetricCall{name: name, inc: inc, tags: tags, consent: analyticsEnabled})
}

func statuslineMetricsConfig(exporter any, environment string, analytics any) *config.Config {
	otel := map[string]any{"metrics_exporter": exporter}
	if environment != "" {
		otel["environment"] = environment
	}
	values := map[string]any{"otel": otel}
	if analytics != nil {
		values["analytics"] = map[string]any{"enabled": analytics}
	}
	return &config.Config{Values: values}
}

func cleanStatuslineEnv(string) (string, bool) { return "", false }

func TestStatuslineMetricsExporterEligibilityLikeRust(t *testing.T) {
	bounded := "codex-tui"
	cases := []struct {
		name        string
		exporter    any
		environment string
		originator  string
		getenv      func(string) (string, bool)
		want        bool
	}{
		{name: "disabled exporter", exporter: config.OtelExporterKindNone, originator: bounded},
		{name: "statsig default launch", exporter: config.OtelExporterKindStatsig, originator: bounded, want: true},
		{
			name:        "statsig custom environment",
			exporter:    config.OtelExporterKindStatsig,
			environment: "staging",
			originator:  bounded,
		},
		{name: "statsig unbounded originator", exporter: config.OtelExporterKindStatsig, originator: "secret agent"},
		{
			name:       "statsig resource attributes",
			exporter:   config.OtelExporterKindStatsig,
			originator: bounded,
			getenv: func(key string) (string, bool) {
				if key == "OTEL_RESOURCE_ATTRIBUTES" {
					return "installation=abc", true
				}
				return "", false
			},
		},
		{
			name:       "statsig metrics headers",
			exporter:   config.OtelExporterKindStatsig,
			originator: bounded,
			getenv: func(key string) (string, bool) {
				if key == "OTEL_EXPORTER_OTLP_METRICS_HEADERS" {
					return "authorization=token", true
				}
				return "", false
			},
		},
		{
			name:       "statsig empty header value is allowed",
			exporter:   config.OtelExporterKindStatsig,
			originator: bounded,
			getenv: func(key string) (string, bool) {
				if key == "OTEL_EXPORTER_OTLP_HEADERS" {
					return "", true
				}
				return "", false
			},
			want: true,
		},
		{
			name:       "explicit otlp http collector",
			exporter:   map[string]any{config.OtelExporterKindOtlpHTTP: map[string]any{"endpoint": "http://localhost:4318/v1/metrics", "protocol": "binary"}},
			originator: bounded,
			want:       true,
		},
		{
			name:       "explicit otlp grpc collector",
			exporter:   map[string]any{config.OtelExporterKindOtlpGRPC: map[string]any{"endpoint": "http://localhost:4317"}},
			originator: bounded,
			want:       true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := tc.getenv
			if getenv == nil {
				getenv = cleanStatuslineEnv
			}
			sink := &recordingStatuslineSink{}
			cfg := statuslineMetricsConfig(tc.exporter, tc.environment, nil)
			metrics := newStatuslineMetricsWithEnv(cfg, sink, tc.originator, getenv)
			metrics.Record("codex.statusline.picker_opened", nil)
			if got := len(sink.calls) == 1; got != tc.want {
				t.Fatalf("recorded = %t, want %t (%d calls)", got, tc.want, len(sink.calls))
			}
		})
	}
}

func TestStatuslineMetricsAppliesAnalyticsConsentLikeRust(t *testing.T) {
	sink := &recordingStatuslineSink{}
	cfg := statuslineMetricsConfig(config.OtelExporterKindStatsig, "", false)
	metrics := newStatuslineMetricsWithEnv(cfg, sink, "codex-tui", cleanStatuslineEnv)
	metrics.Record("codex.statusline.provider_changed", map[string]string{"change": "enabled_custom"})
	if len(sink.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(sink.calls))
	}
	if sink.calls[0].consent {
		t.Fatal("analytics.enabled = false was not forwarded as withheld consent")
	}

	// An absent analytics setting keeps the default consent Rust treats as on.
	sink.calls = nil
	metrics = newStatuslineMetricsWithEnv(statuslineMetricsConfig(config.OtelExporterKindStatsig, "", nil), sink, "codex-tui", cleanStatuslineEnv)
	metrics.Record("codex.statusline.picker_opened", nil)
	if len(sink.calls) != 1 || !sink.calls[0].consent {
		t.Fatalf("default consent calls = %#v", sink.calls)
	}

	// No metrics client is a no-op, not a panic.
	metrics = newStatuslineMetricsWithEnv(statuslineMetricsConfig(config.OtelExporterKindStatsig, "", nil), nil, "codex-tui", cleanStatuslineEnv)
	metrics.Record("codex.statusline.picker_opened", nil)
}

func TestStatuslineMetricsRecordsPickerOpenedLikeRust(t *testing.T) {
	sink := &recordingStatuslineSink{}
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.statuslineFeatureEnabled = true
	model.statuslineMetrics = newStatuslineMetricsWithEnv(
		statuslineMetricsConfig(config.OtelExporterKindStatsig, "", nil), sink, "codex-tui", cleanStatuslineEnv)

	_ = model.openStatuslineProviderPicker()
	if len(sink.calls) != 1 || sink.calls[0].name != "codex.statusline.picker_opened" {
		t.Fatalf("picker open calls = %#v", sink.calls)
	}

	// The feature gate stays in front of the counters: a disabled statusline
	// never opens the picker and never records.
	sink.calls = nil
	model.statuslineFeatureEnabled = false
	_ = model.openStatuslineProviderPicker()
	if len(sink.calls) != 0 {
		t.Fatalf("disabled feature recorded %#v", sink.calls)
	}
}

func TestStatuslineProviderChangeKindsLikeRust(t *testing.T) {
	defaultSelection := &config.StatusLineProviderSelection{Default: true}
	first := &config.StatusLineProviderSelection{Server: "alpha", Tool: "show"}
	second := &config.StatusLineProviderSelection{Server: "beta", Tool: "show"}

	cases := []struct {
		name     string
		previous *config.StatusLineProviderSelection
		next     *config.StatusLineProviderSelection
		want     string
	}{
		{name: "builtin to custom", previous: defaultSelection, next: first, want: "enabled_custom"},
		{name: "unset to custom", previous: nil, next: first, want: "enabled_custom"},
		{name: "custom to builtin", previous: first, next: defaultSelection, want: "returned_to_default"},
		{name: "custom to custom", previous: first, next: second, want: "switched_custom"},
		{name: "same custom", previous: first, next: &config.StatusLineProviderSelection{Server: "alpha", Tool: "show"}},
		{name: "builtin to builtin", previous: defaultSelection, next: defaultSelection},
		{name: "tool change", previous: first, next: &config.StatusLineProviderSelection{Server: "alpha", Tool: "other"}, want: "switched_custom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statuslineProviderChangeKind(tc.previous, tc.next); got != tc.want {
				t.Fatalf("change kind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatuslineProviderChangeRecordsOnlyTransitionsLikeRust(t *testing.T) {
	sink := &recordingStatuslineSink{}
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.statuslineMetrics = newStatuslineMetricsWithEnv(
		statuslineMetricsConfig(config.OtelExporterKindStatsig, "", nil), sink, "codex-tui", cleanStatuslineEnv)

	model.statuslineProviderChange = &statuslineProviderChange{
		from: &config.StatusLineProviderSelection{Default: true},
		to:   config.StatusLineProviderSelection{Server: "alpha", Tool: "show"},
	}
	model.recordStatuslineProviderChange()
	if len(sink.calls) != 1 || sink.calls[0].name != "codex.statusline.provider_changed" ||
		sink.calls[0].tags["change"] != "enabled_custom" {
		t.Fatalf("provider change calls = %#v", sink.calls)
	}
	if model.statuslineProviderChange != nil {
		t.Fatal("pending provider change was not consumed")
	}

	// Re-saving the same selection is not a change (Rust records no label).
	sink.calls = nil
	model.statuslineProviderChange = &statuslineProviderChange{
		from: &config.StatusLineProviderSelection{Server: "alpha", Tool: "show"},
		to:   config.StatusLineProviderSelection{Server: "alpha", Tool: "show"},
	}
	model.recordStatuslineProviderChange()
	if len(sink.calls) != 0 {
		t.Fatalf("unchanged selection recorded %#v", sink.calls)
	}
}

// Rust app/user_turn_submission.rs records the visit's first primary-thread
// prompt with the feature/builtin/custom mode; Rust begin_visit re-arms the
// counter when the TUI attaches another thread.
func TestStatuslineMetricsFirstPromptModesLikeRust(t *testing.T) {
	sink := &recordingStatuslineSink{}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	model := NewModel(state, Options{
		Width:           80,
		Height:          24,
		FeatureSettings: map[string]bool{"tui_extensions_statusline": true},
		OnResolveStatuslineProvider: func(context.Context, StatuslineProviderReadRequest) (StatuslineProvider, error) {
			return StatuslineProvider{}, nil
		},
		OnReadStatuslineProvider: func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return nil, nil
		},
	})
	model.statuslineMetrics = newStatuslineMetricsWithEnv(
		statuslineMetricsConfig(config.OtelExporterKindStatsig, "", nil), sink, "codex-tui", cleanStatuslineEnv)

	model.submitRequest(SubmitRequest{Prompt: "first"}, false)
	if len(sink.calls) != 1 || sink.calls[0].name != "codex.statusline.first_prompt" ||
		sink.calls[0].tags["mode"] != "builtin" {
		t.Fatalf("first prompt calls = %#v", sink.calls)
	}
	model.submitRequest(SubmitRequest{Prompt: "second"}, false)
	if len(sink.calls) != 1 {
		t.Fatalf("second prompt in the same visit recorded %#v", sink.calls)
	}

	// A different thread is a new visit, so the next prompt counts again.
	model.switchThreadID("thread-other")
	sink.calls = nil
	model.submitRequest(SubmitRequest{Prompt: "third"}, false)
	if len(sink.calls) != 1 || sink.calls[0].tags["mode"] != "builtin" {
		t.Fatalf("new visit calls = %#v", sink.calls)
	}

	// A saved MCP provider reports the custom mode.
	sink.calls = nil
	model.statusLineProvider = &config.StatusLineProviderSelection{Server: "fixture", Tool: "line"}
	model.switchThreadID("thread-custom")
	model.submitRequest(SubmitRequest{Prompt: "custom"}, false)
	if len(sink.calls) != 1 || sink.calls[0].tags["mode"] != "custom" {
		t.Fatalf("custom mode calls = %#v", sink.calls)
	}

	// Without the feature the counter still reports, labelled feature_off.
	sink.calls = nil
	model.statuslineFeatureEnabled = false
	model.switchThreadID("thread-off")
	model.submitRequest(SubmitRequest{Prompt: "off"}, false)
	if len(sink.calls) != 1 || sink.calls[0].tags["mode"] != "feature_off" {
		t.Fatalf("feature_off calls = %#v", sink.calls)
	}

	// An ineligible sink still consumes the visit, exactly like Rust setting
	// first_prompt_recorded before the exporter check.
	sink.calls = nil
	ineligible := newStatuslineMetricsWithEnv(
		statuslineMetricsConfig(config.OtelExporterKindNone, "", nil), sink, "codex-tui", cleanStatuslineEnv)
	ineligible.RecordFirstPrompt("builtin")
	ineligible.RecordFirstPrompt("builtin")
	if len(sink.calls) != 0 || !ineligible.firstPromptRecorded {
		t.Fatalf("ineligible metrics calls = %#v, recorded = %t", sink.calls, ineligible.firstPromptRecorded)
	}
}

// Prompts typed into a side chat or a subagent view never count as the primary
// thread's first prompt (Rust checks primary_thread_id == thread_id).
func TestStatuslineMetricsFirstPromptSkipsSideAndSubagentThreadsLikeRust(t *testing.T) {
	sink := &recordingStatuslineSink{}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-side")
	model := NewModel(state, Options{
		Width:           80,
		Height:          24,
		FeatureSettings: map[string]bool{"tui_extensions_statusline": true},
	})
	model.statuslineMetrics = newStatuslineMetricsWithEnv(
		statuslineMetricsConfig(config.OtelExporterKindStatsig, "", nil), sink, "codex-tui", cleanStatuslineEnv)
	model.activeSide = &activeSideConversation{
		ParentThreadID: "thread-main",
		SideThreadID:   "thread-side",
		ShowingSide:    true,
	}
	model.submitRequest(SubmitRequest{Prompt: "side question"}, false)
	if len(sink.calls) != 0 {
		t.Fatalf("side chat recorded the primary first prompt: %#v", sink.calls)
	}

	model.activeSide = nil
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "thread-main", IsPrimary: true},
		{ThreadID: "thread-child"},
	}
	model.switchThreadID("thread-child")
	model.submitRequest(SubmitRequest{Prompt: "subagent task"}, false)
	if len(sink.calls) != 0 {
		t.Fatalf("subagent view recorded the primary first prompt: %#v", sink.calls)
	}

	// The primary thread of the same agent tree still counts.
	model.switchThreadID("thread-main")
	model.submitRequest(SubmitRequest{Prompt: "main task"}, false)
	if len(sink.calls) != 1 || sink.calls[0].name != "codex.statusline.first_prompt" {
		t.Fatalf("primary thread calls = %#v", sink.calls)
	}
}

// Rust chatwidget/statusline_reader.rs records provider_started when the read
// starts and provider_failed with Failure::metric_kind for lookup, transport,
// deadline, and malformed-response failures.
func TestStatuslineProviderMetricsRecordStartAndFailuresLikeRust(t *testing.T) {
	resolve := func(context.Context, StatuslineProviderReadRequest) (StatuslineProvider, error) {
		return StatuslineProvider{Found: true, Title: "line", URI: "ui://fixture/line"}, nil
	}
	read := func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
		return nil, nil
	}
	newModel := func() (*Model, *recordingStatuslineSink) {
		sink := &recordingStatuslineSink{}
		model := statuslineProviderFooterModel(t, resolve, read)
		model.statuslineMetrics = newStatuslineMetricsWithEnv(
			statuslineMetricsConfig(config.OtelExporterKindStatsig, "", nil), sink, "codex-tui", cleanStatuslineEnv)
		model.refreshStatusControls()
		return model, sink
	}

	model, sink := newModel()
	if model.ensureProviderFooterCmd() == nil {
		t.Fatal("provider read did not start")
	}
	if len(sink.calls) != 1 || sink.calls[0].name != "codex.statusline.provider_started" {
		t.Fatalf("started calls = %#v", sink.calls)
	}
	started := model.statuslineProviderRead
	model.applyStatuslineProviderResolve(statuslineProviderResolveMsg{
		token:    started.token,
		threadID: started.threadID,
		server:   started.server,
		tool:     started.tool,
		err:      context.DeadlineExceeded,
	})
	if len(sink.calls) != 2 || sink.calls[1].name != "codex.statusline.provider_failed" ||
		sink.calls[1].tags["kind"] != "timeout" {
		t.Fatalf("lookup timeout calls = %#v", sink.calls)
	}

	// A provider that cannot be resolved is an error, not a timeout.
	model, sink = newModel()
	_ = model.ensureProviderFooterCmd()
	started = model.statuslineProviderRead
	model.applyStatuslineProviderResolve(statuslineProviderResolveMsg{
		token:    started.token,
		threadID: started.threadID,
		server:   started.server,
		tool:     started.tool,
		err:      errStatuslineProviderLookupUnavailable,
	})
	if len(sink.calls) != 2 || sink.calls[1].tags["kind"] != "error" {
		t.Fatalf("lookup unavailable calls = %#v", sink.calls)
	}

	// A read that misses its deadline reports a timeout.
	model, sink = newModel()
	_ = model.ensureProviderFooterCmd()
	started = model.statuslineProviderRead
	model.applyStatuslineProviderRead(statuslineProviderReadMsg{
		token:    started.token,
		threadID: started.threadID,
		server:   started.server,
		err:      errStatuslineProviderReadTimeout,
	})
	if len(sink.calls) != 2 || sink.calls[1].tags["kind"] != "timeout" {
		t.Fatalf("read timeout calls = %#v", sink.calls)
	}

	// A malformed envelope is a generic error.
	model, sink = newModel()
	_ = model.ensureProviderFooterCmd()
	started = model.statuslineProviderRead
	model.applyStatuslineProviderRead(statuslineProviderReadMsg{
		token:    started.token,
		threadID: started.threadID,
		server:   started.server,
		contents: []StatuslineProviderContent{{
			URI:      started.uri,
			MIMEType: "text/plain",
			Text:     "null",
			IsText:   true,
		}},
	})
	if len(sink.calls) != 2 || sink.calls[1].tags["kind"] != "error" {
		t.Fatalf("malformed read calls = %#v", sink.calls)
	}
}
