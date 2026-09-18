package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// captureReadOnlyToolRequests records the JSON-RPC params of every tool request
// so the read-only policy can be asserted on the wire.
type captureReadOnlyToolRequests struct {
	list      []map[string]any
	call      []map[string]any
	resources []map[string]any
	listCalls int
}

func newReadOnlyToolTestServer(t *testing.T, capture *captureReadOnlyToolRequests) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		var params map[string]any
		if len(request.Params) > 0 {
			_ = json.Unmarshal(request.Params, &params)
		}
		switch request.Method {
		case "server/discover":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"error":   map[string]any{"code": mcpJSONRPCMethodNotFoundCode, "message": "method not found"},
			})
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"protocolVersion": defaultMCPProtocol, "capabilities": map[string]any{}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			capture.list = append(capture.list, params)
			capture.listCalls++
			result := map[string]any{"tools": []any{}}
			if capture.listCalls == 1 {
				// Force a second page so the policy is asserted with a cursor.
				result["nextCursor"] = "next"
			}
			writeHTTPMCPResponse(t, w, request.ID, result)
		case "resources/list":
			capture.resources = append(capture.resources, params)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		case "tools/call":
			capture.call = append(capture.call, params)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"content": []any{}})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
}

// TestMCPReadOnlyToolsRequestMetadataLikeRust mirrors Rust #46042's
// read_only_tool_requests_preserve_host_policy_and_caller_metadata: a
// read-only connection marks tools/list and tools/call with
// `openai/readOnly: true`, overriding a caller-supplied false while preserving
// pagination, other metadata, and arguments. Other requests are untouched.
func TestMCPReadOnlyToolsRequestMetadataLikeRust(t *testing.T) {
	for _, mode := range []MCPProtocolMode{MCPProtocolLegacy, MCPProtocol20260728} {
		for _, readOnly := range []bool{false, true} {
			name := fmt.Sprintf("mode=%d/readOnly=%t", mode, readOnly)
			t.Run(name, func(t *testing.T) {
				capture := &captureReadOnlyToolRequests{}
				server := newReadOnlyToolTestServer(t, capture)
				defer server.Close()

				config := &ServerConfig{URL: server.URL, ProtocolMode: mode, RequiresReadOnlyTools: readOnly}
				client := newMCPHTTPClient(config)
				defer client.Close()

				if _, err := listMCPHTTPTools(client, &httpClientCallOptions{}); err != nil {
					t.Fatalf("listMCPHTTPTools() error = %v", err)
				}
				if _, err := listMCPHTTPResources(client, &httpClientCallOptions{}); err != nil {
					t.Fatalf("listMCPHTTPResources() error = %v", err)
				}
				callerMeta := map[string]any{mcpReadOnlyToolsMetaKey: false, "callId": "preserved"}
				if _, err := callMCPHTTPToolWithClient(client, "write", "", "", "", nil, nil, nil, "write", map[string]any{"message": "hello"}, callerMeta); err != nil {
					t.Fatalf("callMCPHTTPToolWithClient() error = %v", err)
				}

				if len(capture.list) != 2 {
					t.Fatalf("tools/list requests = %d, want 2 pages", len(capture.list))
				}
				wantReadOnly := any(nil)
				if readOnly {
					wantReadOnly = true
				}
				if got := metaValue(capture.list[0]); got != wantReadOnly {
					t.Fatalf("first tools/list readOnly = %#v, want %#v (params=%#v)", got, wantReadOnly, capture.list[0])
				}
				if _, ok := capture.list[0]["cursor"]; ok {
					t.Fatalf("first tools/list must not carry a cursor: %#v", capture.list[0])
				}
				if got := metaValue(capture.list[1]); got != wantReadOnly {
					t.Fatalf("second tools/list readOnly = %#v, want %#v", got, wantReadOnly)
				}
				if got := capture.list[1]["cursor"]; got != "next" {
					t.Fatalf("second tools/list cursor = %#v, want next", got)
				}

				// Resources are not part of the read-only policy.
				if len(capture.resources) != 1 {
					t.Fatalf("resources/list requests = %d, want 1", len(capture.resources))
				}
				if meta, ok := capture.resources[0]["_meta"]; ok {
					t.Fatalf("resources/list must not carry the read-only marker: %#v", meta)
				}

				if len(capture.call) != 1 {
					t.Fatalf("tools/call requests = %d, want 1", len(capture.call))
				}
				if got := metaValue(capture.call[0]); got != readOnly {
					t.Fatalf("tools/call readOnly = %#v, want %t", got, readOnly)
				}
				callMeta, _ := capture.call[0]["_meta"].(map[string]any)
				if callMeta["callId"] != "preserved" {
					t.Fatalf("tools/call dropped caller metadata: %#v", callMeta)
				}
				if !reflect.DeepEqual(capture.call[0]["arguments"], map[string]any{"message": "hello"}) {
					t.Fatalf("tools/call arguments = %#v", capture.call[0]["arguments"])
				}
			})
		}
	}
}

// metaValue returns the read-only marker from a request's `_meta` object, or nil
// when the request carries no marker.
func metaValue(params map[string]any) any {
	meta, _ := params["_meta"].(map[string]any)
	if meta == nil {
		return nil
	}
	return meta[mcpReadOnlyToolsMetaKey]
}

// TestMCPReadOnlyToolsIsolateConnectionsAndCatalogsLikeRust mirrors Rust
// #46042's connection-reuse and catalog-isolation rules: the policy is part of
// the connection identity, it is applied to every server from the runtime, and
// a read-only server stays out of the shared tool catalog.
func TestMCPReadOnlyToolsIsolateConnectionsAndCatalogsLikeRust(t *testing.T) {
	readOnly := ServerConfig{URL: "https://example.test/mcp", Enabled: true, RequiresReadOnlyTools: true}
	writable := cloneServerConfig(&readOnly)
	writable.RequiresReadOnlyTools = false

	if mcpConnectionCacheKey(&readOnly, false) == mcpConnectionCacheKey(&writable, false) {
		t.Fatal("a read-only connection must not share an identity with an unrestricted one")
	}
	if _, eligible := mcpToolCatalogGraceKey("docs", &readOnly, false, false, false); eligible {
		t.Fatal("a read-only server must stay out of the shared tool catalog")
	}
	if _, eligible := mcpToolCatalogGraceKey("docs", &writable, false, false, false); !eligible {
		t.Fatal("an unrestricted server must remain catalog-eligible")
	}

	// The runtime policy is published on every server it builds.
	policy := &RuntimeConfig{
		RequiresReadOnlyMCPTools: true,
		Servers: map[string]ServerRegistration{
			"docs": {Config: ServerConfig{URL: "https://example.test/mcp", Enabled: true}},
		},
	}
	service := NewMCPService(policy)
	config, ok := service.serverConfig("docs")
	if !ok || !config.RequiresReadOnlyTools {
		t.Fatalf("runtime read-only policy was not applied to the server config: %#v", config)
	}
	unrestricted := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: "https://example.test/mcp", Enabled: true}},
	}})
	if config, ok := unrestricted.serverConfig("docs"); !ok || config.RequiresReadOnlyTools {
		t.Fatalf("the read-only policy must stay disabled by default: %#v", config)
	}

	// Turning the policy on replaces a writable connection.
	service = unrestricted
	initial, _ := service.serverConfig("docs")
	client := service.httpClientForServer("docs", &initial)
	service.ApplyRuntimeConfig(&RuntimeConfig{
		RequiresReadOnlyMCPTools: true,
		Servers: map[string]ServerRegistration{
			"docs": {Config: ServerConfig{URL: "https://example.test/mcp", Enabled: true}},
		},
	})
	updated, _ := service.serverConfig("docs")
	if got := service.httpClientForServer("docs", &updated); got == client {
		t.Fatal("enabling the read-only policy reused an unrestricted connection")
	}
}

// TestMCPReadOnlyToolsKeepsUnrestrictedRequestsUnmarked pins that the marker is
// absent (not false) for an unrestricted connection with no caller metadata.
func TestMCPReadOnlyToolsKeepsUnrestrictedRequestsUnmarked(t *testing.T) {
	config := &ServerConfig{URL: "https://example.test/mcp", Enabled: true}
	params := mcpListParamsForCursorWithConfig(config, nil)
	if _, ok := params["_meta"]; ok {
		t.Fatalf("unrestricted tools/list must not carry metadata: %#v", params)
	}
	callParams := map[string]any{"name": "read", "arguments": map[string]any{}}
	applyMCPReadOnlyToolsMeta(callParams, config)
	if _, ok := callParams["_meta"]; ok {
		t.Fatalf("unrestricted tools/call must not carry metadata: %#v", callParams)
	}
	// The shared tools/call builder covers both transports.
	if params := mcpToolCallParams(config, "read", map[string]any{}, nil); params["name"] != "read" {
		t.Fatalf("tools/call params = %#v", params)
	} else if _, ok := params["_meta"]; ok {
		t.Fatalf("unrestricted tools/call params must not carry metadata: %#v", params)
	}
	readOnlyParams := mcpToolCallParams(&ServerConfig{URL: "https://example.test/mcp", Enabled: true, RequiresReadOnlyTools: true}, "write", map[string]any{"q": 1}, nil)
	if got := metaValue(readOnlyParams); got != true {
		t.Fatalf("read-only tools/call params marker = %#v, want true", got)
	}
	// The caller's metadata object is never mutated in place.
	callerMeta := map[string]any{"callId": "preserved"}
	callParams["_meta"] = callerMeta
	applyMCPReadOnlyToolsMeta(callParams, &ServerConfig{URL: "https://example.test/mcp", Enabled: true, RequiresReadOnlyTools: true})
	if _, ok := callerMeta["openai/readOnly"]; ok {
		t.Fatalf("the caller's metadata object was mutated: %#v", callerMeta)
	}
	if got := metaValue(callParams); got != true {
		t.Fatalf("read-only marker = %#v, want true", got)
	}
}
