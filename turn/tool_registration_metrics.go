package turn

import (
	"strings"

	"codex_go/metrics"
	"codex_go/model"
	"codex_go/tool"
)

// ToolRegistrationMetricSink receives the per-sampling-request registration
// histogram. telemetry.TurnMetricSink and state.TaskMetrics satisfy it.
type ToolRegistrationMetricSink interface {
	HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string)
}

const (
	toolRegistrationExposureDirect            = "direct"
	toolRegistrationExposureDirectModelOnly   = "direct_model_only"
	toolRegistrationExposureDeferred          = "deferred"
	toolRegistrationExposureDeferredModelOnly = "deferred_model_only"
	toolRegistrationExposureCodeModeOnly      = "code_mode_only"
	toolRegistrationExposureHidden            = "hidden"
)

// recordToolRegistrationMetrics records the finalized registration inventory
// once per sampling request. Zero counts are emitted for categories with no
// registrations, and only bounded exposure/tool-mode/incremental-tools labels
// are attached (Rust #55692).
func recordToolRegistrationMetrics(sink ToolRegistrationMetricSink, counts tool.RegistrationCounts, toolMode string, incrementalTools bool) {
	if sink == nil {
		return
	}
	toolMode = normalizeToolRegistrationMode(toolMode)
	incremental := "false"
	if incrementalTools {
		incremental = "true"
	}
	samples := [...]struct {
		exposure string
		count    int
	}{
		{toolRegistrationExposureDirect, counts.Direct},
		{toolRegistrationExposureDirectModelOnly, counts.DirectModelOnly},
		{toolRegistrationExposureDeferred, counts.Deferred},
		{toolRegistrationExposureDeferredModelOnly, counts.DeferredModelOnly},
		{toolRegistrationExposureCodeModeOnly, counts.CodeModeOnly},
		{toolRegistrationExposureHidden, counts.Hidden},
	}
	for _, sample := range samples {
		sink.HistogramWithBounds(
			metrics.ToolRegistrationsMetric,
			sample.count,
			metrics.ToolRegistrationsMetricBoundaries,
			map[string]string{
				"exposure":          sample.exposure,
				"tool_mode":         toolMode,
				"incremental_tools": incremental,
			},
		)
	}
}

func normalizeToolRegistrationMode(toolMode string) string {
	switch strings.ToLower(strings.TrimSpace(toolMode)) {
	case model.ToolModeCodeMode:
		return model.ToolModeCodeMode
	case model.ToolModeCodeModeOnly:
		return model.ToolModeCodeModeOnly
	default:
		return model.ToolModeDirect
	}
}
