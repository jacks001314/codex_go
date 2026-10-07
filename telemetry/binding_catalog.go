package telemetry

import (
	"strconv"
	"time"
)

// Rust parity: codex-rs/codex-mcp/src/connection_manager/catalog_telemetry.rs
// (`emit_binding_catalog`). The connection manager reports one event per
// materialized MCP catalog through the process-global tracing subscriber, with
// the trace-safe target and these fields.
const (
	// BindingCatalogEventName is Rust's `event.name` for the per-server catalog
	// record.
	BindingCatalogEventName = "mcp.binding_catalog"
	// BindingCatalogLogMessage is Rust's event message, appended by the tracing
	// macro (the record's body).
	BindingCatalogLogMessage = "MCP binding catalog materialized"
)

// BindingCatalogTraceEvent is one materialized MCP catalog, mirroring the
// arguments of Rust's `emit_binding_catalog`. ThreadID is Go's addition: the
// catalog path is context-free, so the receiving thread names the span the
// event attaches to (Rust resolves the same span from the ambient tracing
// context instead).
type BindingCatalogTraceEvent struct {
	// ThreadID is the thread whose catalog was materialized; the event lands on
	// that thread's live span.
	ThreadID string
	// ProductSKU is Rust's bounded `product_sku` field.
	ProductSKU string
	// ServerKind is one of codex_apps, plugin or configured.
	ServerKind string
	// PluginID is the contributing plugin, empty for a server without one
	// (Rust passes `plugin_id.unwrap_or("")`).
	PluginID string
	// CatalogSource is cached or live.
	CatalogSource string
	// ToolCount is the number of tools the catalog holds.
	ToolCount int
	// ToolDefinitionJSONBytes is the serialized size of those raw definitions.
	ToolDefinitionJSONBytes int
}

// EmitBindingCatalogTraceEvents emits Rust's `mcp.binding_catalog` trace event
// for each catalog. The event attaches to the emitting thread's live span, which
// is how Rust's tracing layer finds the span an event belongs to; an event whose
// thread has no live span is dropped, exactly like a Rust event emitted outside
// any span. It returns the number of events emitted.
func EmitBindingCatalogTraceEvents(events []BindingCatalogTraceEvent) int {
	if len(events) == 0 {
		return 0
	}
	emitted := 0
	for _, event := range events {
		span := LiveThreadSpan(event.ThreadID)
		if span == nil {
			continue
		}
		span.AddEvent(BindingCatalogEventName, bindingCatalogEventAttributes(event), time.Time{})
		emitted++
	}
	return emitted
}

// bindingCatalogEventAttributes renders the event fields Rust's
// `emit_binding_catalog` records: the trace-safe target and level every
// session-telemetry record carries, plus the six catalog fields.
func bindingCatalogEventAttributes(event BindingCatalogTraceEvent) map[string]string {
	return map[string]string{
		"level":                      "INFO",
		"target":                     OtelTraceSafeTarget,
		"event.name":                 BindingCatalogEventName,
		"product_sku":                event.ProductSKU,
		"server_kind":                event.ServerKind,
		"plugin_id":                  event.PluginID,
		"catalog_source":             event.CatalogSource,
		"tool_count":                 itoa(event.ToolCount),
		"tool_definition_json_bytes": itoa(event.ToolDefinitionJSONBytes),
	}
}

// itoa renders a numeric event field the way the OTLP attributes carry it.
func itoa(value int) string { return strconv.Itoa(value) }
