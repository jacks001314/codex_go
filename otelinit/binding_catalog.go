package otelinit

import (
	"codex_go/mcp"
	"codex_go/telemetry"
)

// Rust parity: codex-rs/codex-mcp/src/connection_manager/tool_catalog.rs gates
// the catalog telemetry on `tracing::enabled!(target: "codex_otel.trace_safe",
// Level::INFO)` (the log half) and on `codex_otel::global()` (the histogram
// half). Go maps the first gate onto the host's trace pipeline and installs the
// adapter from the host initialization, at the same boundary where the
// process-global metrics client is installed.

// BindingCatalogTelemetryObserver returns the host sink for the MCP service's
// raw catalog measurements: one `mcp.binding_catalog` trace event per
// materialized catalog, carrying Rust's fields (product_sku, server_kind,
// plugin_id, catalog_source, tool_count, tool_definition_json_bytes) on the
// `codex_otel.trace_safe` target.
func BindingCatalogTelemetryObserver() mcp.BindingCatalogTelemetrySink {
	return bindingCatalogTelemetrySink
}

// bindingCatalogTelemetrySink is the single adapter instance the host installs,
// so callers can compare the installed sink with BindingCatalogTelemetryObserver.
var bindingCatalogTelemetrySink mcp.BindingCatalogTelemetrySink = func(events []mcp.BindingCatalogTelemetry) {
	telemetry.EmitBindingCatalogTraceEvents(bindingCatalogTraceEvents(events))
}

// InstallBindingCatalogTelemetry installs the observer when the host has an
// enabled trace pipeline, and returns the installed sink (nil otherwise).
// Without a trace pipeline
// the events would be dropped after the measurement was taken, so the sink stays
// unset and the MCP service measures nothing, like a Rust build whose
// `codex_otel.trace_safe` target is disabled.
func InstallBindingCatalogTelemetry(provider *telemetry.OtelProvider) mcp.BindingCatalogTelemetrySink {
	if provider == nil {
		return nil
	}
	traces := provider.Traces()
	if traces == nil || !traces.Enabled() {
		return nil
	}
	sink := BindingCatalogTelemetryObserver()
	mcp.SetBindingCatalogTelemetryObserver(sink)
	return sink
}

// bindingCatalogTraceEvents maps the MCP service's measurements onto the
// trace-safe event shape.
func bindingCatalogTraceEvents(events []mcp.BindingCatalogTelemetry) []telemetry.BindingCatalogTraceEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]telemetry.BindingCatalogTraceEvent, 0, len(events))
	for _, event := range events {
		out = append(out, telemetry.BindingCatalogTraceEvent{
			ThreadID:                event.ThreadID,
			ProductSKU:              event.ProductSKU,
			ServerKind:              event.ServerKind,
			PluginID:                event.PluginID,
			CatalogSource:           event.CatalogSource,
			ToolCount:               event.ToolCount,
			ToolDefinitionJSONBytes: event.ToolDefinitionJSONBytes,
		})
	}
	return out
}
