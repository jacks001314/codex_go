package appserver

import (
	promptctx "codex_go/prompt"
	"codex_go/telemetry"
)

// Rust parity: codex-rs/ext/skills/src/render_observability.rs
// (record_catalog_metrics): one histogram per rendered skill catalog, tagged by
// the catalog surface.

// Catalog surfaces mirror RenderObservability's CatalogSurface values that Go
// renders. Go renders the host catalog and the executor (plus orchestrator and
// custom) catalog at one turn-instructions site - the host catalog is Rust's
// TurnInput catalog (the full catalog minus executor/orchestrator entries) and
// the executor catalog is Rust's ExecutorWorldState - so it has no separate
// ThreadContext, OrchestratorWorldState, or HostWorldState renders.
const (
	skillCatalogSurfaceTurnInput     = "turn_input"
	skillCatalogSurfaceExecutorWorld = "executor_world_state"
)

// recordSkillCatalogRender mirrors record_catalog_metrics: the enabled total,
// the kept total, whether anything was truncated, and the truncated description
// characters, all tagged by the catalog surface. A skipped render records the
// zero report (Rust's SkillRenderReport::default()).
func (r *RuntimeRouter) recordSkillCatalogRender(surface string, report *promptctx.SkillRenderReport) {
	if r == nil || r.services.SkillShadowMetrics == nil {
		return
	}
	sink := r.services.SkillShadowMetrics
	tags := map[string]string{"catalog_surface": surface}
	totalCount, includedCount, omittedCount, truncatedChars := 0, 0, 0, 0
	if report != nil {
		totalCount = report.TotalCount
		includedCount = report.IncludedCount
		omittedCount = report.OmittedCount
		truncatedChars = report.TruncatedDescriptionChars
	}
	truncated := 0
	if omittedCount > 0 {
		truncated = 1
	}
	// Rust #48819 records these with explicit boundaries: integer counts over
	// 0..512, a 0/1 truncation flag, and logarithmic removed-description
	// characters, so each family keeps its zero bucket and an overflow bucket
	// above its range (Rust render_observability.rs `histogram_with_boundaries`).
	sink.HistogramWithBounds(telemetry.ThreadSkillsEnabledTotalMetric, totalCount, telemetry.ThreadSkillsCountMetricBoundaries, tags)
	sink.HistogramWithBounds(telemetry.ThreadSkillsKeptTotalMetric, includedCount, telemetry.ThreadSkillsCountMetricBoundaries, tags)
	sink.HistogramWithBounds(telemetry.ThreadSkillsTruncatedMetric, truncated, telemetry.ThreadSkillsTruncatedBoundaries, tags)
	sink.HistogramWithBounds(telemetry.ThreadSkillsDescriptionTruncatedCharsMetric, truncatedChars, telemetry.ThreadSkillsDescriptionTruncatedCharsBoundaries, tags)
}

// skillRenderReportOf returns the render report when the catalog rendered.
func skillRenderReportOf(available *promptctx.AvailableSkills) *promptctx.SkillRenderReport {
	if available == nil {
		return nil
	}
	return available.Report
}
