package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/protocol"
)

// Rust #60506: mcpServer/resource/read forwards the inbound W3C carrier to the
// MCP server even when no tracing layer produced an active span.
func TestMCPHTTPResourceReadForwardsRequestTraceLikeRust(t *testing.T) {
	const (
		wantTraceparent = "00-0000000000000000000000000000000e-000000000000000f-01"
		wantTracestate  = "vendor=value"
	)
	var (
		readTraceparent string
		readTracestate  string
		readCount       int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-trace")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "trace", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/read":
			readCount++
			readTraceparent = r.Header.Get("traceparent")
			readTracestate = r.Header.Get("tracestate")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"contents": []map[string]string{{"uri": "file://demo", "text": "demo"}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	response, err := service.ReadResource(&MCPResourceReadParams{
		Server: "http",
		URI:    "file://demo",
		Context: protocol.WithTraceContext(context.Background(), &protocol.W3CTraceContext{
			Traceparent: wantTraceparent,
			Tracestate:  wantTracestate,
		}),
	})
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if len(response.Contents) != 1 {
		t.Fatalf("contents = %#v", response.Contents)
	}
	if readCount != 1 {
		t.Fatalf("resources/read count = %d, want 1", readCount)
	}
	if readTraceparent != wantTraceparent || readTracestate != wantTracestate {
		t.Fatalf("resources/read trace headers = %q/%q, want %q/%q", readTraceparent, readTracestate, wantTraceparent, wantTracestate)
	}
}

// Without a request carrier and without a tracing layer, no trace headers are
// invented.
func TestMCPHTTPResourceReadWithoutTraceSendsNoTraceHeadersLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-plain")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "plain", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/read":
			if got := r.Header.Get("traceparent"); got != "" {
				t.Fatalf("traceparent = %q, want empty", got)
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"contents": []map[string]string{{"uri": "file://plain", "text": "plain"}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	if _, err := service.ReadResource(&MCPResourceReadParams{Server: "http", URI: "file://plain"}); err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
}
