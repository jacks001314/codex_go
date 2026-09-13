package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

// Rust's log export filter: only Codex telemetry targets that are not
// trace-safe are exported as log records.
func TestIsLogExportTargetLikeRust(t *testing.T) {
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{target: "codex_otel.log_only", want: true},
		{target: "codex_otel", want: true},
		{target: "codex_otel.something_else", want: true},
		{target: "codex_otel.trace_safe", want: false},
		{target: "codex_otel.trace_safe.extra", want: false},
		{target: "codex_core", want: false},
		{target: "hyper_util", want: false},
		{target: "", want: false},
	} {
		if got := IsLogExportTarget(tc.target); got != tc.want {
			t.Fatalf("IsLogExportTarget(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

type recordingHandler struct {
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// The handler exports Codex telemetry records to the OTLP client and delegates
// every record to the wrapped handler.
func TestLogsSlogHandlerExportsTelemetryTargetsLikeRust(t *testing.T) {
	var bodies []map[string]any
	contentTypes := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
		payload, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("log batch json error = %v payload=%s", err, payload)
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewLogsClient(LogsClientOptions{
		ServiceName:    "codex-app-server",
		ServiceVersion: "1.2.3",
		Environment:    "dev",
		Endpoint:       server.URL + "/v1/logs",
		ExportInterval: -1,
		Timeout:        5 * time.Second,
	})
	if !client.Enabled() {
		t.Fatal("logs client is disabled")
	}
	delegate := &recordingHandler{}
	logger := slog.New(NewLogsSlogHandler(client, delegate))

	logger.Info("exported", "target", "codex_otel.log_only", "thread_id", "thread-1")
	logger.Warn("trace safe", "target", "codex_otel.trace_safe")
	logger.Info("local only", "target", "codex_home")
	logger.Warn("other telemetry", "target", "codex_otel.other", "k", "v")

	if len(delegate.records) != 4 {
		t.Fatalf("delegated records = %d, want every record", len(delegate.records))
	}
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("exported batches = %d, want 1", len(bodies))
	}
	if contentTypes[0] != "application/json" {
		t.Fatalf("content type = %q", contentTypes[0])
	}
	resourceLogs := bodies[0]["resourceLogs"].([]any)
	batch := resourceLogs[0].(map[string]any)
	resource := batch["resource"].(map[string]any)
	if !strings.Contains(string(mustJSON(t, resource)), `"service.name"`) {
		t.Fatalf("resource = %#v", resource)
	}
	scopeLogs := batch["scopeLogs"].([]any)[0].(map[string]any)
	scope := scopeLogs["scope"].(map[string]any)
	// The appender bridge reports the tracing target as the instrumentation
	// scope, so the first record's own target names the scope.
	if scope["name"] != "codex_otel.log_only" {
		t.Fatalf("scope = %#v", scope)
	}
	records := scopeLogs["logRecords"].([]any)
	if len(records) != 1 {
		t.Fatalf("exported records = %d, want one per scope: %#v", len(records), records)
	}
	// The second codex_otel target opens its own scope.
	second := batch["scopeLogs"].([]any)[1].(map[string]any)
	if second["scope"].(map[string]any)["name"] != "codex_otel.other" {
		t.Fatalf("second scope = %#v", second["scope"])
	}
	if len(second["logRecords"].([]any)) != 1 {
		t.Fatalf("second scope records = %#v", second["logRecords"])
	}
	first := records[0].(map[string]any)
	if first["severityText"] != "INFO" || first["severityNumber"] != float64(otlpSeverityInfo) {
		t.Fatalf("first record severity = %#v", first)
	}
	body := first["body"].(map[string]any)
	if body["stringValue"] != "exported" {
		t.Fatalf("first record body = %#v", first)
	}
	attributes := first["attributes"].([]any)
	keys := map[string]string{}
	for _, attribute := range attributes {
		entry := attribute.(map[string]any)
		value := entry["value"].(map[string]any)
		keys[entry["key"].(string)] = value["stringValue"].(string)
	}
	if keys["thread_id"] != "thread-1" {
		t.Fatalf("attributes = %#v", keys)
	}
	if _, ok := keys["target"]; ok {
		t.Fatalf("the target attribute must become the record scope: %#v", keys)
	}
	if ts, _ := first["timeUnixNano"].(string); ts == "" {
		t.Fatalf("record time missing: %#v", first)
	}
}

// The OTLP/HTTP binary protocol exports the official protobuf payload, and the
// gRPC transport mirrors the metrics one (the client only builds when the
// endpoint parses).
func TestLogsExporterProtocols(t *testing.T) {
	var contentType string
	var payload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		payload, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter := NewOTLPLogsExporter(OTLPLogsExporterOptions{
		Endpoint: server.URL + "/v1/logs",
		Protocol: OtelHTTPProtocolBinary,
		Timeout:  5 * time.Second,
	})
	if exporter == nil {
		t.Fatal("binary exporter is nil")
	}
	request := OTLPExportLogsRequest{ResourceLogs: []OTLPResourceLogs{{
		Resource: OTLPResource{Attributes: []MetricTagValue{{Key: "service.name", Value: "codex-app-server"}}},
		ScopeLogs: []OTLPScopeLogs{{
			Scope: OTLPScope{Name: LogsScopeName},
			LogRecords: []OTLPLogRecord{{
				TimeUnixNano:   "1700000000000000000",
				SeverityNumber: otlpSeverityWarn,
				SeverityText:   "WARN",
				Body:           "binary body",
				Attributes:     []MetricTagValue{{Key: "k", Value: "v"}},
			}},
		}},
	}}}
	if err := exporter.Export(context.Background(), request); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if contentType != "application/x-protobuf" {
		t.Fatalf("content type = %q", contentType)
	}
	var decoded collectorlogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("protobuf decode error = %v", err)
	}
	if len(decoded.ResourceLogs) != 1 || len(decoded.ResourceLogs[0].ScopeLogs) != 1 {
		t.Fatalf("decoded batch = %#v", decoded.ResourceLogs)
	}
	records := decoded.ResourceLogs[0].ScopeLogs[0].LogRecords
	if len(records) != 1 || records[0].Body.GetStringValue() != "binary body" ||
		records[0].SeverityNumber.String() != "SEVERITY_NUMBER_WARN" {
		t.Fatalf("decoded records = %#v", records)
	}

	// A queue overflow drops records rather than growing without bound.
	client := NewLogsClient(LogsClientOptions{Endpoint: server.URL, QueueSize: 2, ExportInterval: -1})
	for _, body := range []string{"a", "b", "c"} {
		client.Emit(OTLPLogRecord{Body: body})
	}
	if client.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", client.Dropped())
	}

	// A provider with only the log exporter builds the log pipeline alone.
	provider, err := NewOtelProvider(OtelSettings{
		ServiceName: "codex-app-server",
		Exporter:    OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: server.URL + "/v1/logs"},
	})
	if err != nil {
		t.Fatalf("NewOtelProvider() error = %v", err)
	}
	if provider == nil || provider.Logs() == nil || provider.Metrics() != nil {
		t.Fatalf("provider = %#v", provider)
	}
	if handler := provider.LogsHandler(nil); handler == nil {
		t.Fatal("log handler is nil for an enabled log pipeline")
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	// A provider with every exporter disabled stays nil.
	if disabled, err := NewOtelProvider(OtelSettings{}); err != nil || disabled != nil {
		t.Fatalf("disabled provider = %#v err=%v", disabled, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return encoded
}
