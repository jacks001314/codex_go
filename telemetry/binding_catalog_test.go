package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Rust #51215 (`emit_binding_catalog`): the per-server catalog record is a
// trace-safe event, so it lands on the emitting thread's live span and is
// dropped when the thread has no span open. The exported span event carries the
// event name, the trace-safe target, and Rust's six catalog fields.
func TestEmitBindingCatalogTraceEventsTargetsThreadSpanLikeRust(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("span batch json error = %v payload=%s", err, payload)
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
		Now:            func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if !client.Enabled() {
		t.Fatal("traces client is disabled")
	}
	tracer := client.Tracer()
	if tracer == nil {
		t.Fatal("tracer is nil")
	}

	session := tracer.StartSpan("session_loop", map[string]string{ThreadIDAttribute: "thread-1"})
	if session == nil {
		t.Fatal("session span is nil")
	}

	// A thread without a live span records nothing, the way Rust's tracing layer
	// drops an event emitted outside any span.
	if got := EmitBindingCatalogTraceEvents([]BindingCatalogTraceEvent{{ThreadID: "other-thread", ToolCount: 1}}); got != 0 {
		t.Fatalf("events on a span-less thread = %d, want 0", got)
	}
	// The thread's live span receives the event.
	emitted := EmitBindingCatalogTraceEvents([]BindingCatalogTraceEvent{{
		ThreadID:                "thread-1",
		ProductSKU:              "codex",
		ServerKind:              "plugin",
		PluginID:                "plug-1",
		CatalogSource:           "live",
		ToolCount:               3,
		ToolDefinitionJSONBytes: 4096,
	}})
	if emitted != 1 {
		t.Fatalf("emitted = %d, want 1", emitted)
	}

	// Once the span ends the thread has no live span again.
	session.End()
	if got := EmitBindingCatalogTraceEvents([]BindingCatalogTraceEvent{{ThreadID: "thread-1", ToolCount: 1}}); got != 0 {
		t.Fatalf("events after the span ended = %d, want 0", got)
	}

	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("span batches = %d, want 1", len(bodies))
	}
	span := firstExportedSpan(t, bodies[0])
	if span["name"] != "session_loop" {
		t.Fatalf("span name = %v, want session_loop", span["name"])
	}
	events, _ := span["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("span events = %#v, want exactly the catalog record", span["events"])
	}
	event := events[0].(map[string]any)
	if event["name"] != BindingCatalogEventName {
		t.Fatalf("event name = %v, want %s", event["name"], BindingCatalogEventName)
	}
	attributes := spanEventAttributes(t, event)
	for key, want := range map[string]string{
		"level":                      "INFO",
		"target":                     OtelTraceSafeTarget,
		"event.name":                 BindingCatalogEventName,
		"product_sku":                "codex",
		"server_kind":                "plugin",
		"plugin_id":                  "plug-1",
		"catalog_source":             "live",
		"tool_count":                 "3",
		"tool_definition_json_bytes": "4096",
	} {
		if got := attributes[key]; got != want {
			t.Fatalf("event attribute %q = %q, want %q (attributes=%v)", key, got, want, attributes)
		}
	}
}

// A span is only a thread's live span while it is open, and the innermost span
// of a thread wins, so a nested session span does not lose the record to its
// parent.
func TestLiveThreadSpanTracksOpenSpansLikeRust(t *testing.T) {
	client := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       "https://traces.test/v1/traces",
		ExportInterval: -1,
	})
	tracer := client.Tracer()
	if tracer == nil {
		t.Fatal("tracer is nil")
	}
	if got := LiveThreadSpan(""); got != nil {
		t.Fatalf("LiveThreadSpan(empty) = %#v, want nil", got)
	}
	outer := tracer.StartSpan("session_loop", map[string]string{ThreadIDAttribute: "thread-1"})
	inner := tracer.StartSpanWithParent(outer, "run_sampling_request", map[string]string{ThreadIDAttribute: "thread-1"})
	if got := LiveThreadSpan("thread-1"); got != inner {
		t.Fatalf("live span = %#v, want the innermost span", got)
	}
	inner.End()
	if got := LiveThreadSpan("thread-1"); got != outer {
		t.Fatalf("live span after the inner span ended = %#v, want the outer span", got)
	}
	outer.End()
	if got := LiveThreadSpan("thread-1"); got != nil {
		t.Fatalf("live span after every span ended = %#v, want nil", got)
	}
	// A span without the thread attribute never becomes a thread's live span.
	unkeyed := tracer.StartSpan("instructions_load", nil)
	if got := LiveThreadSpan("thread-1"); got != nil {
		t.Fatalf("unkeyed span became a live thread span: %#v", got)
	}
	unkeyed.End()
}

func firstExportedSpan(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	resourceSpans, _ := body["resourceSpans"].([]any)
	if len(resourceSpans) != 1 {
		t.Fatalf("resourceSpans = %#v", body["resourceSpans"])
	}
	scopeSpans, _ := resourceSpans[0].(map[string]any)["scopeSpans"].([]any)
	if len(scopeSpans) != 1 {
		t.Fatalf("scopeSpans = %#v", resourceSpans[0])
	}
	spans, _ := scopeSpans[0].(map[string]any)["spans"].([]any)
	if len(spans) != 1 {
		t.Fatalf("spans = %#v", scopeSpans[0])
	}
	return spans[0].(map[string]any)
}
