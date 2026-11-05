package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"codex_go/mcp"
)

func TestRuntimeRouterMCPResourceReadForwardsCallerMetadataLikeRust(t *testing.T) {
	var mu sync.Mutex
	var reads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode MCP request error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-resource-meta")
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "resource-meta", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/read":
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Errorf("Decode resources/read params error = %v", err)
				return
			}
			mu.Lock()
			reads = append(reads, params)
			mu.Unlock()
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"contents": []map[string]string{{"uri": "file://caller-meta", "text": "ok"}},
			})
		default:
			writeRuntimeRouterMCPError(t, w, request.ID, -32601, "not found", nil)
		}
	}))
	defer server.Close()

	router := NewRuntimeRouter(RuntimeServices{
		MCP: mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
			"custom": {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
		}}),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodMCPServerResourceRead, mcp.MCPResourceReadParams{
		Server: "custom",
		URI:    "file://caller-meta",
		Meta: map[string]any{
			"example/trace": map[string]any{"sequence": []any{1.0, 2.0}},
		},
	}))
	if response.Error != nil {
		t.Fatalf("resource read error = %+v", response.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reads) != 1 {
		t.Fatalf("resources/read calls = %d, want 1", len(reads))
	}
	meta, _ := reads[0]["_meta"].(map[string]any)
	trace, _ := meta["example/trace"].(map[string]any)
	sequence, _ := trace["sequence"].([]any)
	if len(sequence) != 2 || sequence[0] != float64(1) || sequence[1] != float64(2) {
		t.Fatalf("forwarded _meta = %#v", meta)
	}

	rejected := router.Handle(requestWithParams(t, IntID(2), MethodMCPServerResourceRead, mcp.MCPResourceReadParams{
		Server: "custom",
		URI:    "file://caller-meta",
		Meta:   map[string]any{"threadId": "spoofed"},
	}))
	if rejected.Error == nil || rejected.Error.Code != -32600 || !strings.Contains(rejected.Error.Message, "threadId") {
		t.Fatalf("reserved metadata error = %+v", rejected.Error)
	}
	if len(reads) != 1 {
		t.Fatalf("reserved metadata reached the server: calls=%d", len(reads))
	}
}
