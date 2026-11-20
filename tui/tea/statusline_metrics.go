package tea

import (
	"os"
	"strings"

	"codex_go/config"
	"codex_go/telemetry"
)

// Rust parity: codex-rs/tui/src/statusline_metrics.rs.
//
// StatuslineMetrics captures the launch exporter and the analytics consent once
// so later config edits cannot widen what an installation reports. The Go TUI
// builds its OTEL provider at launch and never rebuilds it, so the exporter
// captured here is also the live exporter Rust re-checks on every record.

// StatuslineMetricSink is the metrics client surface the statusline counters
// use (Rust `MetricsClient::counter_with_analytics_consent`).
type StatuslineMetricSink interface {
	CounterWithAnalyticsConsent(name string, inc int, tags map[string]string, analyticsEnabled bool)
}

// statuslineMetricEnvKeys are the OTEL variables that would identify a launch
// to a custom collector. Statsig counters stay off while any is non-empty
// because the exporter captured its resource attributes at launch.
var statuslineMetricEnvKeys = []string{
	"OTEL_RESOURCE_ATTRIBUTES",
	"OTEL_EXPORTER_OTLP_METRICS_HEADERS",
	"OTEL_EXPORTER_OTLP_HEADERS",
}

// StatuslineMetrics records the `/statusline` adoption counters.
type StatuslineMetrics struct {
	firstPromptRecorded bool
	sink                StatuslineMetricSink
	analyticsEnabled    bool
}

// NewStatuslineMetrics resolves once, at startup, whether the launch exporter
// may carry statusline counters (Rust `StatuslineMetrics::new`).
func NewStatuslineMetrics(cfg *config.Config, sink StatuslineMetricSink, originator string) *StatuslineMetrics {
	return newStatuslineMetricsWithEnv(cfg, sink, originator, os.LookupEnv)
}

func newStatuslineMetricsWithEnv(cfg *config.Config, sink StatuslineMetricSink, originator string, getenv func(string) (string, bool)) *StatuslineMetrics {
	metrics := &StatuslineMetrics{}
	if cfg == nil || sink == nil {
		return metrics
	}
	otel := cfg.Otel()
	if !statuslineMetricsExporterEligible(otel.MetricsExporter, otel.Environment, originator, getenv) {
		return metrics
	}
	metrics.sink = sink
	metrics.analyticsEnabled = true
	if value := cfg.AnalyticsEnabledValue(); value != nil {
		metrics.analyticsEnabled = *value
	}
	return metrics
}

// statuslineMetricsExporterEligible mirrors Rust's exporter match: a disabled
// exporter never records, the built-in Statsig exporter only records for the
// default environment, a bounded originator, and a launch without custom OTEL
// resource attributes or headers, and explicit OTLP collectors always record.
func statuslineMetricsExporterEligible(exporter config.OtelExporterKind, environment string, originator string, getenv func(string) (string, bool)) bool {
	switch exporter.Kind {
	case config.OtelExporterKindNone, "":
		return false
	case config.OtelExporterKindStatsig:
		if environment != config.DefaultOtelEnvironment {
			return false
		}
		if telemetry.BoundedOriginatorTagValue(originator) != originator {
			return false
		}
		for _, key := range statuslineMetricEnvKeys {
			if value, ok := getenv(key); ok && value != "" {
				return false
			}
		}
		return true
	case config.OtelExporterKindOtlpHTTP, config.OtelExporterKindOtlpGRPC:
		return true
	default:
		return false
	}
}

// Record increments one statusline counter (Rust `StatuslineMetrics::record`).
// A statusline that is not eligible, or a host without a metrics client, is a
// no-op.
func (s *StatuslineMetrics) Record(name string, labels map[string]string) {
	if s == nil || s.sink == nil {
		return
	}
	s.sink.CounterWithAnalyticsConsent(name, 1, labels, s.analyticsEnabled)
}

// BeginVisit re-arms the per-visit first-prompt counter when the TUI switches
// back to its primary thread (Rust `StatuslineMetrics::begin_visit`).
func (s *StatuslineMetrics) BeginVisit() {
	if s == nil {
		return
	}
	s.firstPromptRecorded = false
}

// RecordFirstPrompt counts the visit's first user prompt with Rust's
// `feature_off`/`builtin`/`custom` mode label. The recorded flag is set even
// when the counter is ineligible, so one visit never reports twice.
func (s *StatuslineMetrics) RecordFirstPrompt(mode string) {
	if s == nil || s.firstPromptRecorded {
		return
	}
	s.firstPromptRecorded = true
	s.Record("codex.statusline.first_prompt", map[string]string{"mode": mode})
}

// statuslineFirstPromptMode mirrors Rust's record_first_prompt mode match.
func (m *Model) statuslineFirstPromptMode() string {
	if !m.customStatuslineEnabled() {
		return "feature_off"
	}
	if m.selectedStatuslineProvider() != nil {
		return "custom"
	}
	return "builtin"
}

// recordStatuslineFirstPrompt mirrors Rust app/user_turn_submission.rs: the
// counter fires once per visit and only for prompts submitted on the primary
// thread, so side chats and subagent views never count their own prompts.
func (m *Model) recordStatuslineFirstPrompt() {
	if m == nil || m.State == nil || m.statuslineMetrics == nil {
		return
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" || m.inSideConversation() || !m.sideParentIsMain(threadID) {
		return
	}
	m.statuslineMetrics.RecordFirstPrompt(m.statuslineFirstPromptMode())
}
