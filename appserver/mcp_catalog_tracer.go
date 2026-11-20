package appserver

import (
	"codex_go/mcp"
	"codex_go/telemetry"
)

// mcpCatalogTracer adapts the app-server's request tracer to the MCP catalog
// span seam (Rust #62320 instruments `mcp.catalog.*` from the ambient tracing
// subscriber; package mcp cannot import the telemetry client because telemetry
// reaches back through the turn package).
type mcpCatalogTracer struct {
	tracer *telemetry.Tracer
}

func (t mcpCatalogTracer) StartCatalogSpan(name string) func() {
	if t.tracer == nil {
		return nil
	}
	span := t.tracer.StartSpan(name, nil)
	return span.End
}

// mcpCatalogTracerFor wraps the installed tracer, or reports no tracer so the
// catalog spans stay no-ops when tracing is off.
func mcpCatalogTracerFor(tracer *telemetry.Tracer) mcp.CatalogTracer {
	if tracer == nil {
		return nil
	}
	return mcpCatalogTracer{tracer: tracer}
}
