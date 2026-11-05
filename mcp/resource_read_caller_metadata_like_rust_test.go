package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestMCPResourceReadCallerMetadataLikeRust ports #59509: caller `_meta` is
// forwarded on resources/read, non-codex_apps servers keep the connector-like
// keys, codex_apps reserves routing keys, and metadata-bearing reads bypass the
// resource cache so each call reaches the server.
func TestMCPResourceReadCallerMetadataLikeRust(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request mcpHTTPEnvelopeForTargetTest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "caller-meta-session")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "metadata", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/read":
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Errorf("decode params: %v", err)
				return
			}
			mu.Lock()
			received = append(received, params)
			mu.Unlock()
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"contents": []map[string]string{{"uri": "file://caller-meta", "text": "ok"}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	t.Cleanup(server.Close)

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"custom": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	for sequence := 1; sequence <= 2; sequence++ {
		if _, err := service.ReadResource(&MCPResourceReadParams{
			ServerName: "custom",
			URI:        "file://caller-meta",
			Meta: map[string]any{
				"example/trace": map[string]any{"sequence": sequence},
				"connector_id":  "caller-connector",
				"link_id":       "caller-link",
				"_codex_apps":   map[string]any{"resource_uri": "custom://route"},
			},
		}); err != nil {
			t.Fatalf("ReadResource(%d) error = %v", sequence, err)
		}
	}
	mu.Lock()
	reads := append([]map[string]any(nil), received...)
	mu.Unlock()
	if len(reads) != 2 {
		t.Fatalf("metadata-bearing reads reached the server %d times, want 2 (cache bypass)", len(reads))
	}
	meta, _ := reads[1]["_meta"].(map[string]any)
	trace, _ := meta["example/trace"].(map[string]any)
	if trace["sequence"] != float64(2) || meta["connector_id"] != "caller-connector" || meta["link_id"] != "caller-link" {
		t.Fatalf("forwarded caller metadata = %#v", meta)
	}
	if route, _ := meta["_codex_apps"].(map[string]any); route["resource_uri"] != "custom://route" {
		t.Fatalf("nested caller metadata = %#v", meta)
	}

	appService := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		RuntimeCodexAppsMCPServerName: {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	for _, test := range []struct {
		key   string
		value any
	}{
		{key: "threadId", value: "spoofed"},
		{key: MCPToolTurnMetadataMetaKey, value: map[string]any{}},
		{key: ConnectionLeaseMetaKey, value: "spoofed"},
		{key: "connector_id", value: "spoofed"},
		{key: "link_id", value: "spoofed"},
		{key: "_codex_apps", value: map[string]any{"resource_uri": "spoofed"}},
	} {
		_, err := appService.ReadResource(&MCPResourceReadParams{
			ServerName: RuntimeCodexAppsMCPServerName,
			URI:        "file://caller-meta",
			Meta:       map[string]any{test.key: test.value},
		})
		if err == nil || !strings.Contains(err.Error(), "reserved resource read metadata key") {
			t.Fatalf("reserved key %q error = %v", test.key, err)
		}
	}
}

func TestMCPResourceReadParamsCallerMetadataWireShapeLikeRust(t *testing.T) {
	encoded, err := json.Marshal(MCPResourceReadParams{
		Server: "custom",
		URI:    "file://caller-meta",
		Meta:   map[string]any{"example/trace": []any{1.0, 2.0}},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	meta, _ := decoded["_meta"].(map[string]any)
	if _, ok := meta["example/trace"]; !ok {
		t.Fatalf("wire metadata = %#v", decoded)
	}
}
