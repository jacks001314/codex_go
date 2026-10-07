package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

// TestMCPServerStatusListEmitsBindingCatalogEventOnThreadSpanLikeRust covers the
// app-server half of Rust #51215: the per-server catalog telemetry
// (`codex-mcp` connection_manager/catalog_telemetry.rs::emit_binding_catalog) is
// recorded on the ambient span of the `mcpServer/status/list` RPC. Rust's
// handler runs inside the request's `app_server.request` span and the tracing
// subscriber resolves that ambient span; Go resolves trace-safe records from the
// thread's innermost live span (telemetry.LiveThreadSpan), which the generic
// request span does not satisfy (it carries no thread_id), so the listing must
// open a thread-stamped child of the request span for the events to land.
//
// The test drives the real RPC through router.Handle, a real HTTP MCP server and
// a real OTLP/HTTP trace endpoint, so it pins the whole chain: the event and its
// six catalog fields, and the trace hierarchy (the catalog span's parent is the
// request span itself).
func TestMCPServerStatusListEmitsBindingCatalogEventOnThreadSpanLikeRust(t *testing.T) {
	router, provider, traceBodies, threadID, statusList := newCatalogTelemetryRPC(t, true)
	defer func() { _ = router.Close() }()
	_ = provider

	_ = threadID
	statuses := statusList.Result.(*mcp.MCPListServerStatusResponse)
	status := findMCPStatus(t, statuses, "docs")
	if len(status.Tools) == 0 {
		t.Fatalf("the listing did not materialize the server catalog: %#v", status)
	}
	wantBytes := mcp.RawMCPToolDefinitionJSONBytes(status.Tools)

	method := string(MethodMCPServerStatusList)
	catalogSpan := waitForCatalogSpan(t, traceBodies, threadID, method)
	if catalogSpan == nil {
		t.Fatalf("no exported span carried %s = %q with an %s event", telemetry.ThreadIDAttribute, threadID, telemetry.BindingCatalogEventName)
	}
	if catalogSpan.name != method {
		t.Fatalf("catalog span name = %q, want the RPC method %q", catalogSpan.name, method)
	}
	if catalogSpan.parentSpanID == "" || catalogSpan.parentSpanID != catalogSpan.requestSpanID {
		t.Fatalf("catalog span parent = %q, want the request span %q", catalogSpan.parentSpanID, catalogSpan.requestSpanID)
	}
	attributes := catalogSpan.event
	for key, want := range map[string]string{
		"level":                      "INFO",
		"target":                     telemetry.OtelTraceSafeTarget,
		"event.name":                 telemetry.BindingCatalogEventName,
		"product_sku":                mcp.DefaultCodexAppsMCPProductSKU,
		"server_kind":                mcp.BindingCatalogServerKindConfig,
		"plugin_id":                  "",
		"catalog_source":             mcp.BindingCatalogSourceLive,
		"tool_count":                 strconv.Itoa(len(status.Tools)),
		"tool_definition_json_bytes": strconv.Itoa(wantBytes),
	} {
		if got := attributes[key]; got != want {
			t.Fatalf("event attribute %q = %q, want %q (attributes=%v)", key, got, want, attributes)
		}
	}
}

// TestMCPServerStatusListWithoutThreadIDDropsCatalogEventLikeRust documents the
// one place where Go cannot mirror Rust #51215. Rust records the event on the
// request span, which exists for a listing without a thread too, while the Go
// catalog path is context-free and resolves its span from the thread id: the
// same listing measures the catalog (the observer still receives the batch) and
// then the telemetry adapter drops the events, leaving the request span without
// an mcp.binding_catalog event.
func TestMCPServerStatusListWithoutThreadIDDropsCatalogEventLikeRust(t *testing.T) {
	router, provider, traceBodies, _, statusList := newCatalogTelemetryRPC(t, false)
	defer func() { _ = router.Close() }()

	if statusList.Error != nil {
		t.Fatalf("mcpServer/status/list error: %+v", statusList.Error)
	}
	if err := provider.Traces().Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	method := string(MethodMCPServerStatusList)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case body := <-traceBodies:
			spans := parseExportedSpans(t, body)
			var sawRequestSpan bool
			for _, span := range spans {
				if span.name == method {
					sawRequestSpan = true
				}
				if len(span.events) > 0 {
					t.Fatalf("a listing without a thread id emitted catalog telemetry: span=%q events=%#v", span.name, span.events)
				}
			}
			if sawRequestSpan {
				return
			}
		case <-deadline:
			t.Fatal("the request span of the thread-less listing was not exported")
		}
	}
}

// newCatalogTelemetryRPC wires the app-server with an OTLP trace endpoint, one
// live HTTP MCP server and (optionally) a started thread, then lists the MCP
// servers over the real RPC.
func newCatalogTelemetryRPC(t *testing.T, withThread bool) (*RuntimeRouter, *telemetry.OtelProvider, chan map[string]any, string, *Response) {
	t.Helper()
	mcp.SetBindingCatalogTelemetryObserver(nil)
	t.Cleanup(func() { mcp.SetBindingCatalogTelemetryObserver(nil) })

	traceBodies := make(chan map[string]any, 16)
	traceServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/v1/traces") {
			payload := map[string]any{}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			select {
			case traceBodies <- payload:
			default:
			}
		}
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(traceServer.Close)

	home := t.TempDir()
	configToml := fmt.Sprintf("[otel.trace_exporter.otlp-http]\nendpoint = %q\nprotocol = \"json\"\n", traceServer.URL+"/v1/traces")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configToml), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml error = %v", err)
	}

	mcpServer := newThreadBoundMCPTestServer(t, "catalog-docs")
	t.Cleanup(mcpServer.Close)
	service := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"docs": {Config: mcp.ServerConfig{URL: mcpServer.URL, Enabled: true}},
	}})
	t.Cleanup(func() { _ = service.Close() })

	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("Done"),
		ThreadStatus: NewThreadStatusManager(),
		MCP:          service,
	})
	router.configureOtelMetrics(home, nil, state.NewTaskMetrics())
	provider := router.currentOtelProvider()
	if provider == nil || provider.Traces() == nil || !provider.Traces().Enabled() {
		t.Fatalf("the trace pipeline was not built from config.toml: %#v", provider)
	}
	if !mcp.BindingCatalogTelemetrySinkInstalled() {
		t.Fatal("the host did not install the binding catalog telemetry sink")
	}

	threadID := ""
	if withThread {
		threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{Model: "gpt-5.3-codex"}))
		if threadStart.Error != nil {
			t.Fatalf("thread start error: %+v", threadStart.Error)
		}
		threadID = threadStart.Result.(*ThreadStartResponse).Thread.ID
	}
	params := mcp.MCPListServerStatusParams{
		Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailToolsAndAuthOnly},
	}
	if threadID != "" {
		params.ThreadID = &threadID
	}
	statusList := router.Handle(requestWithParams(t, IntID(2), MethodMCPServerStatusList, params))
	if err := provider.Traces().Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	return router, provider, traceBodies, threadID, statusList
}

// exportedSpan is one span of an OTLP/HTTP JSON trace batch, plus the resolved
// request-span id of the RPC it belongs to.
type exportedSpan struct {
	name          string
	spanID        string
	parentSpanID  string
	attributes    map[string]string
	events        []map[string]string
	event         map[string]string
	requestSpanID string
}

// waitForCatalogSpan reads exported batches until it finds the thread-stamped
// span whose mcp.binding_catalog event was exported, resolving the id of the
// request span it must hang under.
func waitForCatalogSpan(t *testing.T, bodies chan map[string]any, threadID string, method string) *exportedSpan {
	t.Helper()
	deadline := time.After(10 * time.Second)
	var pending []exportedSpan
	for {
		select {
		case body := <-bodies:
			pending = append(pending, parseExportedSpans(t, body)...)
			requestSpanID := ""
			var catalog *exportedSpan
			for i := range pending {
				span := pending[i]
				if span.name == method && span.attributes[telemetry.ThreadIDAttribute] == "" && span.attributes["rpc.method"] == method {
					requestSpanID = span.spanID
				}
			}
			for i := range pending {
				span := &pending[i]
				if span.attributes[telemetry.ThreadIDAttribute] != threadID {
					continue
				}
				for _, event := range span.events {
					if event["event.name"] != telemetry.BindingCatalogEventName {
						continue
					}
					span.event = event
					span.requestSpanID = requestSpanID
					catalog = span
				}
			}
			if catalog != nil && requestSpanID != "" {
				return catalog
			}
		case <-deadline:
			return nil
		}
	}
}

func findMCPStatus(t *testing.T, response *mcp.MCPListServerStatusResponse, name string) *mcp.MCPServerStatus {
	t.Helper()
	if response == nil {
		t.Fatal("the MCP status listing returned nothing")
	}
	for i := range response.Data {
		current := &response.Data[i]
		if current.Name == name || current.Server.Name == name {
			return current
		}
	}
	t.Fatalf("the listing has no %q server: %#v", name, response.Data)
	return nil
}

func parseExportedSpans(t *testing.T, body map[string]any) []exportedSpan {
	t.Helper()
	var spans []exportedSpan
	resourceSpans, _ := body["resourceSpans"].([]any)
	for _, resourceEntry := range resourceSpans {
		resourceSpan, _ := resourceEntry.(map[string]any)
		scopeSpans, _ := resourceSpan["scopeSpans"].([]any)
		for _, scopeEntry := range scopeSpans {
			scopeSpan, _ := scopeEntry.(map[string]any)
			rawSpans, _ := scopeSpan["spans"].([]any)
			for _, rawEntry := range rawSpans {
				raw, _ := rawEntry.(map[string]any)
				span := exportedSpan{attributes: otlpAttributeStrings(raw["attributes"])}
				span.name, _ = raw["name"].(string)
				span.spanID, _ = raw["spanId"].(string)
				span.parentSpanID, _ = raw["parentSpanId"].(string)
				if events, ok := raw["events"].([]any); ok {
					for _, eventEntry := range events {
						event, _ := eventEntry.(map[string]any)
						span.events = append(span.events, otlpAttributeStrings(event["attributes"]))
					}
				}
				spans = append(spans, span)
			}
		}
	}
	return spans
}

func otlpAttributeStrings(raw any) map[string]string {
	attributes := map[string]string{}
	entries, _ := raw.([]any)
	for _, entry := range entries {
		attribute, _ := entry.(map[string]any)
		key, _ := attribute["key"].(string)
		value, _ := attribute["value"].(map[string]any)
		if text, ok := value["stringValue"].(string); ok {
			attributes[key] = text
		}
	}
	return attributes
}
