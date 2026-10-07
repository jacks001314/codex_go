package mcp

import (
	"encoding/json"
	"strings"
	"sync"

	"codex_go/metrics"
)

// Privacy-safe size telemetry for raw MCP tool catalogs, mirroring Rust
// `codex-mcp/src/connection_manager/catalog_telemetry.rs` (upstream
// `a811c72969`, #51215). Go measures the same raw definitions at the point the
// MCP service materializes a server's catalog, and reports them through an
// observer because the codex_otel global client is not importable from here:
// `codex_go/telemetry` imports `codex_go/turn`, which imports `codex_go/mcp`, so
// a direct dependency would be an import cycle. The observer keeps the metric
// name and bucket boundaries identical to Rust, and callers supply the sink
// exactly like `SetProtocolDiscoveryMetricsObserver`.

// MCPBindingCatalogRawDefinitionJSONBytesMetric is Rust's
// RAW_DEFINITION_JSON_BYTES_METRIC: the total serialized JSON size of the raw
// MCP tool definitions for one catalog, measured before filtering or model
// preparation.
const MCPBindingCatalogRawDefinitionJSONBytesMetric = "codex.mcp.binding_catalog.raw_definition_json_bytes"

// DefaultCodexAppsMCPProductSKU mirrors Rust's DEFAULT_CODEX_APPS_MCP_PRODUCT_SKU
// (`codex-mcp/src/mcp/mod.rs`), which `a811c72969` widened to `pub(crate)` so the
// catalog telemetry can attribute the catalog when no SKU is configured.
const DefaultCodexAppsMCPProductSKU = "codex"

// Product SKU attribution values. Rust's `bounded_product_sku` returns None for
// an absent or empty SKU and "other" for an unrecognized one; the catalog
// telemetry call site then substitutes "unknown".
const (
	bindingCatalogUnknownProductSKU = "unknown"
	bindingCatalogOtherProductSKU   = "other"
)

// Catalog source values (Rust's `catalog_source` field): a catalog served from
// an already-materialized cache or one read live from the server.
const (
	BindingCatalogSourceCached = "cached"
	BindingCatalogSourceLive   = "live"
)

// MCP server kind values (Rust's inline classifier): the trusted codex_apps
// registration, a plugin-contributed server, or a plain configured server.
const (
	BindingCatalogServerKindCodexApps = "codex_apps"
	BindingCatalogServerKindPlugin    = "plugin"
	BindingCatalogServerKindConfig    = "configured"
)

const (
	bindingCatalogKiB = 1024.0
	bindingCatalogMiB = 1024.0 * bindingCatalogKiB
)

// mcpBindingCatalogRawDefinitionJSONBytesBuckets mirrors Rust's
// RAW_DEFINITION_JSON_BYTES_BUCKETS (64 KiB through 512 MiB).
var mcpBindingCatalogRawDefinitionJSONBytesBuckets = []float64{
	64.0 * bindingCatalogKiB,
	128.0 * bindingCatalogKiB,
	256.0 * bindingCatalogKiB,
	512.0 * bindingCatalogKiB,
	768.0 * bindingCatalogKiB,
	bindingCatalogMiB,
	1.5 * bindingCatalogMiB,
	2.0 * bindingCatalogMiB,
	3.0 * bindingCatalogMiB,
	4.0 * bindingCatalogMiB,
	6.0 * bindingCatalogMiB,
	8.0 * bindingCatalogMiB,
	12.0 * bindingCatalogMiB,
	16.0 * bindingCatalogMiB,
	24.0 * bindingCatalogMiB,
	32.0 * bindingCatalogMiB,
	48.0 * bindingCatalogMiB,
	64.0 * bindingCatalogMiB,
	96.0 * bindingCatalogMiB,
	128.0 * bindingCatalogMiB,
	192.0 * bindingCatalogMiB,
	256.0 * bindingCatalogMiB,
	384.0 * bindingCatalogMiB,
	512.0 * bindingCatalogMiB,
}

// BindingCatalogRawDefinitionJSONBytesBuckets returns a copy of the histogram
// bucket boundaries used for the raw catalog size metric.
func BindingCatalogRawDefinitionJSONBytesBuckets() []float64 {
	return append([]float64(nil), mcpBindingCatalogRawDefinitionJSONBytesBuckets...)
}

// BoundedProductSKU bounds product SKU attribution before it reaches telemetry
// dimensions, mirroring Rust's `bounded_product_sku` followed by the call
// site's `.unwrap_or("unknown")`: an absent or empty SKU reports "unknown",
// the known "codex" SKU is kept, and any other value collapses to "other".
func BoundedProductSKU(productSKU string) string {
	sku := strings.TrimSpace(productSKU)
	if sku == "" {
		return bindingCatalogUnknownProductSKU
	}
	if sku == DefaultCodexAppsMCPProductSKU {
		return sku
	}
	return bindingCatalogOtherProductSKU
}

// BindingCatalogServerKindFor classifies the catalog owner the way Rust's
// catalog telemetry does: the trusted codex_apps registration, a
// plugin-contributed server (one carrying a plugin id), or a configured server.
func BindingCatalogServerKindFor(serverName string, pluginID string) string {
	if IsCodexAppsMCPServerName(serverName) {
		return BindingCatalogServerKindCodexApps
	}
	if strings.TrimSpace(pluginID) != "" {
		return BindingCatalogServerKindPlugin
	}
	return BindingCatalogServerKindConfig
}

// RawMCPToolDefinitionJSONBytes sums the serialized JSON size of the raw tool
// definitions, mirroring Rust's `tool_definition_json_bytes`. A definition that
// cannot be serialized contributes zero, like Rust's `serialized_json_bytes`.
func RawMCPToolDefinitionJSONBytes(tools []MCPToolInfo) int {
	total := 0
	for i := range tools {
		encoded, err := json.Marshal(tools[i])
		if err != nil {
			continue
		}
		total += len(encoded)
	}
	return total
}

// BindingCatalogTelemetry is one server's raw catalog measurement (Rust's
// `mcp.binding_catalog` trace event plus the per-server half of the histogram
// aggregation).
type BindingCatalogTelemetry struct {
	// ThreadID is the thread whose catalog listing materialized this catalog.
	// The per-server trace event attaches to that thread's live span (Rust reads
	// the same span from the ambient tracing context).
	ThreadID                string
	ProductSKU              string
	ServerKind              string
	PluginID                string
	CatalogSource           string
	ServerName              string
	ToolCount               int
	ToolDefinitionJSONBytes int
}

// BindingCatalogTelemetrySink receives the measurements of one materialized
// catalog. The sink is responsible for emitting Rust's trace event per server
// and the aggregate histogram sample, because only the telemetry layer can
// reach the metrics client.
type BindingCatalogTelemetrySink func(events []BindingCatalogTelemetry)

var bindingCatalogTelemetry struct {
	sync.Mutex
	sink BindingCatalogTelemetrySink
}

// SetBindingCatalogTelemetryObserver installs the sink used to report raw MCP
// catalog sizes. A nil sink disables reporting, which is the default so that
// unconfigured runtimes (and tests) record nothing.
func SetBindingCatalogTelemetryObserver(sink BindingCatalogTelemetrySink) {
	bindingCatalogTelemetry.Lock()
	defer bindingCatalogTelemetry.Unlock()
	bindingCatalogTelemetry.sink = sink
}

// BindingCatalogTelemetrySinkInstalled reports whether a host installed the
// catalog telemetry sink. The host installs it when its trace pipeline is
// enabled (Rust's `tracing::enabled!(target: "codex_otel.trace_safe")` gate), so
// callers can assert the wiring and tests can pin it.
func BindingCatalogTelemetrySinkInstalled() bool {
	bindingCatalogTelemetry.Lock()
	defer bindingCatalogTelemetry.Unlock()
	return bindingCatalogTelemetry.sink != nil
}

func recordBindingCatalogTelemetry(events []BindingCatalogTelemetry) {
	if len(events) == 0 {
		return
	}
	// Rust records the aggregate sample on the process-global metrics client
	// (`codex_otel::global()`) and the per-server trace event separately.
	if recorder := globalBindingCatalogHistogramRecorder(); recorder != nil {
		recorder.HistogramWithBounds(
			MCPBindingCatalogRawDefinitionJSONBytesMetric,
			TotalBindingCatalogDefinitionJSONBytes(events),
			mcpBindingCatalogRawDefinitionJSONBytesBuckets,
			BindingCatalogMetricTags(events[0].ProductSKU),
		)
	}
	bindingCatalogTelemetry.Lock()
	sink := bindingCatalogTelemetry.sink
	bindingCatalogTelemetry.Unlock()
	if sink == nil {
		return
	}
	sink(append([]BindingCatalogTelemetry(nil), events...))
}

// BindingCatalogMetricTags builds the histogram tags for one catalog
// measurement (Rust's `&[("product_sku", product_sku)]`).
func BindingCatalogMetricTags(productSKU string) map[string]string {
	return map[string]string{"product_sku": productSKU}
}

// TotalBindingCatalogDefinitionJSONBytes saturating-summs the per-server
// measurements, mirroring Rust's `saturating_add` aggregation before the
// histogram sample is recorded.
func TotalBindingCatalogDefinitionJSONBytes(events []BindingCatalogTelemetry) int {
	total := 0
	for i := range events {
		bytes := events[i].ToolDefinitionJSONBytes
		if bytes < 0 {
			continue
		}
		if total > int(^uint(0)>>1)-bytes {
			return int(^uint(0) >> 1)
		}
		total += bytes
	}
	return total
}

// bindingCatalogHistogramRecorder is the bounded-histogram capability of the
// process-global metrics recorder (Rust's
// `codex_otel::MetricsClient::histogram_with_boundaries`). The `codex_go/metrics`
// leaf package cannot name that method without importing telemetry, so this
// service asserts the capability structurally, the same way
// `telemetry.StartGlobalTimer` narrows its global recorder.
type bindingCatalogHistogramRecorder interface {
	HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string)
}

// globalBindingCatalogHistogramRecorder returns the process-global recorder when
// it can record explicit bucket boundaries, mirroring Rust's
// `codex_otel::global()` (`a811c72969` reads the global client directly).
func globalBindingCatalogHistogramRecorder() bindingCatalogHistogramRecorder {
	recorder, ok := metrics.Global().(bindingCatalogHistogramRecorder)
	if !ok {
		return nil
	}
	return recorder
}

// BindingCatalogTelemetryEnabled reports whether any catalog-size telemetry
// output is configured, mirroring Rust's
// `catalog_log_enabled || catalog_metrics.is_some()`: when it is false callers
// skip measuring raw definition sizes entirely. Go's stand-ins are the observer
// (the per-server event half) and a global recorder able to record the bounded
// histogram.
func BindingCatalogTelemetryEnabled() bool {
	bindingCatalogTelemetry.Lock()
	sink := bindingCatalogTelemetry.sink
	bindingCatalogTelemetry.Unlock()
	return sink != nil || globalBindingCatalogHistogramRecorder() != nil
}

// bindingCatalogProductSKU returns the bounded product SKU used for this
// service's telemetry dimensions.
func (s *MCPService) bindingCatalogProductSKU() string {
	sku := ""
	if s != nil {
		sku = strings.TrimSpace(s.appsMCPProductSKU)
	}
	if sku == "" {
		sku = DefaultCodexAppsMCPProductSKU
	}
	return BoundedProductSKU(sku)
}

// recordBindingCatalogTelemetry reports the raw tool-definition sizes of the
// servers whose catalog this call materialized, mirroring #51215: one
// `mcp.binding_catalog` event per server plus the aggregate
// `codex.mcp.binding_catalog.raw_definition_json_bytes` histogram sample.
// Servers whose catalog this call did not touch carry no source and are
// skipped, and nothing is measured when no telemetry output is configured.
func (s *MCPService) recordBindingCatalogTelemetry(servers []MCPServerStatus, threadID string) {
	if !BindingCatalogTelemetryEnabled() {
		return
	}
	productSKU := s.bindingCatalogProductSKU()
	events := make([]BindingCatalogTelemetry, 0, len(servers))
	for i := range servers {
		status := servers[i]
		if status.catalogSource == "" {
			continue
		}
		pluginID := ""
		if status.PluginID != nil {
			pluginID = strings.TrimSpace(*status.PluginID)
		}
		serverName := status.effectiveName()
		events = append(events, BindingCatalogTelemetry{
			ThreadID:                threadID,
			ServerName:              serverName,
			ProductSKU:              productSKU,
			ServerKind:              BindingCatalogServerKindFor(serverName, pluginID),
			PluginID:                pluginID,
			CatalogSource:           status.catalogSource,
			ToolCount:               len(status.Tools),
			ToolDefinitionJSONBytes: RawMCPToolDefinitionJSONBytes(status.Tools),
		})
	}
	if len(events) == 0 {
		return
	}
	recordBindingCatalogTelemetry(events)
}
