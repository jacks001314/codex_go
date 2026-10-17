package appserver

import (
	contextfrag "codex_go/context"
	"codex_go/telemetry"
)

// recordDeferredToolsFragmentMetrics mirrors Rust #47873
// (core/src/context/world_state/tools.rs record_fragment_metrics): the deferred
// namespace block is measured before and after the 4 KiB cap, tagged by stage
// and by whether the fragment is a full snapshot or a delta. Unchanged state
// never reaches this function.
func (r *RuntimeRouter) recordDeferredToolsFragmentMetrics(report *contextfrag.DeferredToolsFragmentReport) {
	if r == nil || report == nil || r.services.SkillShadowMetrics == nil {
		return
	}
	kind := "delta"
	if report.Snapshot {
		kind = "snapshot"
	}
	sink := r.services.SkillShadowMetrics
	for _, stage := range []struct {
		name       string
		namespaces int
		bytes      int
	}{
		{"before", report.BeforeNamespaces, report.BeforeBytes},
		{"after", report.AfterNamespaces, report.AfterBytes},
	} {
		tags := map[string]string{"stage": stage.name, "kind": kind}
		sink.HistogramWithBounds(telemetry.ThreadToolsNamespacesTotalMetric, stage.namespaces, telemetry.ThreadToolsMetricBoundaries, tags)
		sink.HistogramWithBounds(telemetry.ThreadToolsFragmentBytesMetric, stage.bytes, telemetry.ThreadToolsMetricBoundaries, tags)
	}
}
