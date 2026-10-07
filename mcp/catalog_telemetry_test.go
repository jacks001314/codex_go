package mcp

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
)

// TestBoundedProductSKULikeRust mirrors Rust #51215 (a811c72969), which moved
// `bounded_product_sku` into codex-otel so both the metric and the trace event
// reuse one bounded attribution: an absent or empty SKU yields None (the caller
// then reports "unknown"), the known "codex" SKU is kept, and any other value
// collapses to "other".
func TestBoundedProductSKULikeRust(t *testing.T) {
	for _, tc := range []struct {
		productSKU string
		want       string
	}{
		{"", "unknown"},
		{"   ", "unknown"},
		{"codex", "codex"},
		{" codex ", "codex"},
		{"chatgpt", "other"},
		{"CODEX", "other"},
	} {
		if got := BoundedProductSKU(tc.productSKU); got != tc.want {
			t.Fatalf("BoundedProductSKU(%q) = %q, want %q", tc.productSKU, got, tc.want)
		}
	}
}

// TestBindingCatalogServerKindLikeRust mirrors Rust #51215's inline classifier
// in tool_catalog.rs: the codex_apps registration, then any server carrying a
// plugin id, then plain configured servers.
func TestBindingCatalogServerKindLikeRust(t *testing.T) {
	for _, tc := range []struct {
		serverName string
		pluginID   string
		want       string
	}{
		{"codex_apps", "", "codex_apps"},
		{"codex_apps", "plug-1", "codex_apps"},
		{"github", "plug-1", "plugin"},
		{"github", "", "configured"},
	} {
		if got := BindingCatalogServerKindFor(tc.serverName, tc.pluginID); got != tc.want {
			t.Fatalf("BindingCatalogServerKindFor(%q, %q) = %q, want %q", tc.serverName, tc.pluginID, got, tc.want)
		}
	}
}

// TestBindingCatalogBucketsLikeRust pins the histogram boundaries to Rust's
// RAW_DEFINITION_JSON_BYTES_BUCKETS (64 KiB through 512 MiB).
func TestBindingCatalogBucketsLikeRust(t *testing.T) {
	want := []float64{
		64 * 1024, 128 * 1024, 256 * 1024, 512 * 1024, 768 * 1024,
		1024 * 1024, 1.5 * 1024 * 1024, 2 * 1024 * 1024, 3 * 1024 * 1024, 4 * 1024 * 1024,
		6 * 1024 * 1024, 8 * 1024 * 1024, 12 * 1024 * 1024, 16 * 1024 * 1024, 24 * 1024 * 1024,
		32 * 1024 * 1024, 48 * 1024 * 1024, 64 * 1024 * 1024, 96 * 1024 * 1024, 128 * 1024 * 1024,
		192 * 1024 * 1024, 256 * 1024 * 1024, 384 * 1024 * 1024, 512 * 1024 * 1024,
	}
	got := BindingCatalogRawDefinitionJSONBytesBuckets()
	if len(got) != len(want) {
		t.Fatalf("bucket count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bucket[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if MCPBindingCatalogRawDefinitionJSONBytesMetric != "codex.mcp.binding_catalog.raw_definition_json_bytes" {
		t.Fatalf("metric name = %q", MCPBindingCatalogRawDefinitionJSONBytesMetric)
	}
}

// TestRawMCPToolDefinitionJSONBytesLikeRust mirrors Rust's
// `tool_definition_json_bytes`: the summed serialized size of the raw tool
// definitions, where a definition that cannot be serialized contributes zero.
func TestRawMCPToolDefinitionJSONBytesLikeRust(t *testing.T) {
	tools := []MCPToolInfo{
		{Name: "search", Description: "Search", InputSchema: map[string]any{"type": "object"}},
		{Name: "fetch"},
	}
	want := 0
	for _, tool := range tools {
		encoded, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		want += len(encoded)
	}
	if got := RawMCPToolDefinitionJSONBytes(tools); got != want {
		t.Fatalf("RawMCPToolDefinitionJSONBytes() = %d, want %d", got, want)
	}

	unserializable := []MCPToolInfo{{Name: "bad", Meta: make(chan int)}}
	if got := RawMCPToolDefinitionJSONBytes(unserializable); got != 0 {
		t.Fatalf("unserializable definition bytes = %d, want 0", got)
	}
	if got := RawMCPToolDefinitionJSONBytes(nil); got != 0 {
		t.Fatalf("nil definitions bytes = %d, want 0", got)
	}
}

// TestListStatusRecordsBindingCatalogTelemetryLikeRust mirrors Rust #51215: one
// measurement per server whose catalog the call materialized (live when the
// server is queried, cached when an already-materialized catalog is reused),
// aggregated into one sample. The server kind, plugin id and product SKU
// attribution match the Rust event fields.
func TestListStatusRecordsBindingCatalogTelemetryLikeRust(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"codex_apps": {Config: ServerConfig{Command: "codex-go-missing-catalog-apps", Enabled: true}},
		"github":     {Config: ServerConfig{Command: "codex-go-missing-catalog-github", Enabled: true}},
		"plugged":    {Config: ServerConfig{Command: "codex-go-missing-catalog-plugged", Enabled: true, Required: true}},
	}})
	defer service.Close()

	pluginID := "plug-1"
	service.SetServer(MCPServerStatus{
		Name:     "plugged",
		State:    MCPServerReady,
		PluginID: &pluginID,
		Tools:    []MCPToolInfo{{Name: "greet"}, {Name: "wave"}},
	})

	var batches [][]BindingCatalogTelemetry
	SetBindingCatalogTelemetryObserver(func(events []BindingCatalogTelemetry) {
		batches = append(batches, events)
	})
	defer SetBindingCatalogTelemetryObserver(nil)

	response, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
		NonBlockingOptional:  true,
		OptionalStartupGrace: 0,
	})
	if err != nil || response == nil {
		t.Fatalf("ListStatusChecked() response=%#v err=%v", response, err)
	}
	if len(batches) != 1 {
		t.Fatalf("telemetry batches = %d, want 1", len(batches))
	}
	byName := map[string]BindingCatalogTelemetry{}
	for _, event := range batches[0] {
		byName[event.ServerName] = event
	}
	if len(byName) != 3 {
		t.Fatalf("telemetry events = %#v", batches[0])
	}
	apps := byName["codex_apps"]
	if apps.ServerKind != BindingCatalogServerKindCodexApps || apps.CatalogSource != BindingCatalogSourceLive {
		t.Fatalf("codex_apps telemetry = %#v", apps)
	}
	configured := byName["github"]
	if configured.ServerKind != BindingCatalogServerKindConfig || configured.CatalogSource != BindingCatalogSourceLive {
		t.Fatalf("github telemetry = %#v", configured)
	}
	plugged := byName["plugged"]
	if plugged.ServerKind != BindingCatalogServerKindPlugin || plugged.CatalogSource != BindingCatalogSourceCached {
		t.Fatalf("plugged telemetry = %#v", plugged)
	}
	if plugged.PluginID != "plug-1" || plugged.ToolCount != 2 {
		t.Fatalf("plugged telemetry = %#v", plugged)
	}
	wantPluggedBytes := RawMCPToolDefinitionJSONBytes([]MCPToolInfo{{Name: "greet"}, {Name: "wave"}})
	if plugged.ToolDefinitionJSONBytes != wantPluggedBytes || wantPluggedBytes == 0 {
		t.Fatalf("plugged bytes = %d, want %d", plugged.ToolDefinitionJSONBytes, wantPluggedBytes)
	}
	if plugged.ProductSKU != DefaultCodexAppsMCPProductSKU {
		t.Fatalf("product sku = %q, want %q", plugged.ProductSKU, DefaultCodexAppsMCPProductSKU)
	}
	if total := TotalBindingCatalogDefinitionJSONBytes(batches[0]); total != wantPluggedBytes {
		t.Fatalf("aggregate bytes = %d, want %d", total, wantPluggedBytes)
	}
	tags := BindingCatalogMetricTags(plugged.ProductSKU)
	if tags["product_sku"] != DefaultCodexAppsMCPProductSKU || len(tags) != 1 {
		t.Fatalf("metric tags = %#v", tags)
	}
}

// TestListStatusCatalogTelemetrySkipsWhenDisabledLikeRust mirrors Rust #51215's
// "skip size measurement when neither telemetry output is enabled": with no
// observer the catalog is still materialized but nothing is measured.
func TestListStatusCatalogTelemetrySkipsWhenDisabledLikeRust(t *testing.T) {
	previous := metrics.Global()
	metrics.InstallGlobal(nil)
	defer metrics.InstallGlobal(previous)
	SetBindingCatalogTelemetryObserver(nil)
	if BindingCatalogTelemetryEnabled() {
		t.Fatal("BindingCatalogTelemetryEnabled() = true with no telemetry output configured")
	}
	var calls int
	SetBindingCatalogTelemetryObserver(func(events []BindingCatalogTelemetry) { calls++ })
	defer SetBindingCatalogTelemetryObserver(nil)
	if !BindingCatalogTelemetryEnabled() {
		t.Fatal("BindingCatalogTelemetryEnabled() = false with an observer installed")
	}

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"missing": {Config: ServerConfig{Command: "codex-go-missing-catalog-skip", Enabled: true}},
	}})
	defer service.Close()
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("observer calls = %d, want 1", calls)
	}
}

type bindingCatalogTestSample struct {
	name       string
	value      int
	boundaries []float64
	tags       map[string]string
}

// bindingCatalogTestRecorder implements both codex_go/metrics.Recorder and the
// bounded-histogram capability telemetry.MetricsClient provides in production.
type bindingCatalogTestRecorder struct {
	mu     sync.Mutex
	scrape []bindingCatalogTestSample
}

func (r *bindingCatalogTestRecorder) RecordDuration(string, time.Duration, map[string]string) {}
func (r *bindingCatalogTestRecorder) Counter(string, int, map[string]string)                  {}
func (r *bindingCatalogTestRecorder) Histogram(string, int, map[string]string)                {}

func (r *bindingCatalogTestRecorder) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scrape = append(r.scrape, bindingCatalogTestSample{
		name:       name,
		value:      value,
		boundaries: append([]float64(nil), boundaries...),
		tags:       cloneStringMap(tags),
	})
}

func (r *bindingCatalogTestRecorder) samples() []bindingCatalogTestSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bindingCatalogTestSample(nil), r.scrape...)
}

// TestListStatusRecordsBindingCatalogHistogramOnGlobalRecorderLikeRust mirrors
// Rust #51215's metrics half: `build_binding` reads `codex_otel::global()` and
// records one aggregate `codex.mcp.binding_catalog.raw_definition_json_bytes`
// sample with the Rust bucket boundaries and the bounded product SKU tag.
func TestListStatusRecordsBindingCatalogHistogramOnGlobalRecorderLikeRust(t *testing.T) {
	previous := metrics.Global()
	recorder := &bindingCatalogTestRecorder{}
	metrics.InstallGlobal(recorder)
	defer metrics.InstallGlobal(previous)
	SetBindingCatalogTelemetryObserver(nil)
	defer SetBindingCatalogTelemetryObserver(nil)

	if !BindingCatalogTelemetryEnabled() {
		t.Fatal("BindingCatalogTelemetryEnabled() = false with a bounded-histogram global recorder")
	}

	service := NewMCPService(&RuntimeConfig{
		AppsMCPProductSKU: "chatgpt",
		Servers: map[string]ServerRegistration{
			"missing": {Config: ServerConfig{Command: "codex-go-missing-catalog-global", Enabled: true}},
		},
	})
	defer service.Close()
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}

	samples := recorder.samples()
	if len(samples) != 1 {
		t.Fatalf("histogram samples = %#v, want 1", samples)
	}
	sample := samples[0]
	if sample.name != MCPBindingCatalogRawDefinitionJSONBytesMetric {
		t.Fatalf("metric name = %q", sample.name)
	}
	wantBoundaries := BindingCatalogRawDefinitionJSONBytesBuckets()
	if len(sample.boundaries) != len(wantBoundaries) {
		t.Fatalf("boundaries = %#v, want %d entries", sample.boundaries, len(wantBoundaries))
	}
	for i := range wantBoundaries {
		if sample.boundaries[i] != wantBoundaries[i] {
			t.Fatalf("boundary[%d] = %v, want %v", i, sample.boundaries[i], wantBoundaries[i])
		}
	}
	// The failed live discovery has no tools, so the aggregate size is zero and
	// an unrecognized configured SKU is bounded to "other".
	if sample.value != 0 {
		t.Fatalf("sample value = %d, want 0", sample.value)
	}
	if sample.tags["product_sku"] != "other" || len(sample.tags) != 1 {
		t.Fatalf("sample tags = %#v", sample.tags)
	}
}
