package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCPStatusExposesToolListMetaLikeRust covers Rust #54686: the
// response-level `_meta` of the first `tools/list` page is preserved on the
// server status and exposed as `toolListMeta` in both detail modes.
func TestMCPStatusExposesToolListMetaLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "docs", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "search",
					"description": "search the docs",
					"inputSchema": map[string]any{"type": "object"},
				}},
				"_meta": map[string]any{"traceId": "abc", "nested": map[string]any{"k": 1}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	defer service.Close()
	for _, mode := range []MCPServerStatusDetailMode{MCPServerStatusDetailFull, MCPServerStatusDetailToolsAndAuthOnly} {
		response, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: mode}})
		if err != nil {
			t.Fatalf("ListStatusChecked(%s) error = %v", mode, err)
		}
		if len(response.Data) != 1 {
			t.Fatalf("status data (%s) = %#v", mode, response.Data)
		}
		status := response.Data[0]
		var decoded map[string]any
		if err := json.Unmarshal(status.ToolListMeta, &decoded); err != nil {
			t.Fatalf("toolListMeta (%s) = %s, error %v", mode, status.ToolListMeta, err)
		}
		if decoded["traceId"] != "abc" {
			t.Fatalf("toolListMeta (%s) = %#v, want the first page's _meta", mode, decoded)
		}
		wire, err := json.Marshal(&status)
		if err != nil {
			t.Fatalf("Marshal(%s) error = %v", mode, err)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(wire, &payload); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", mode, err)
		}
		var wireMeta map[string]any
		if err := json.Unmarshal(payload["toolListMeta"], &wireMeta); err != nil {
			t.Fatalf("wire toolListMeta (%s) = %s, error %v", mode, payload["toolListMeta"], err)
		}
		if wireMeta["traceId"] != "abc" {
			t.Fatalf("wire toolListMeta (%s) = %#v", mode, wireMeta)
		}
		// Rust serializes the field directly after serverCapabilities.
		capabilitiesAt := strings.Index(string(wire), `"serverCapabilities"`)
		metaAt := strings.Index(string(wire), `"toolListMeta"`)
		if capabilitiesAt < 0 || metaAt < capabilitiesAt {
			t.Fatalf("wire order (%s) = %s", mode, wire)
		}
	}
}

// TestMCPStatusSerializesUnavailableToolListMetaAsNullLikeRust covers the Rust
// #54686 null contract: a catalog listed without `_meta` still reports the
// field, and it is null.
func TestMCPStatusSerializesUnavailableToolListMetaAsNullLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "docs", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	defer service.Close()
	response, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].ToolListMeta) != 0 {
		t.Fatalf("status = %#v, want unavailable tool list meta", response.Data)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("data = %#v", payload.Data)
	}
	value, present := payload.Data[0]["toolListMeta"]
	if !present || !strings.EqualFold(string(value), "null") {
		t.Fatalf("toolListMeta wire value = %s, present=%v, want null", value, present)
	}
}

// TestMCPToolListMetaComesFromTheFirstPageLikeRust covers the Rust #54686 rule
// that only the first `tools/list` page's `_meta` is retained: a later page
// must not replace it.
func TestMCPToolListMetaComesFromTheFirstPageLikeRust(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "docs", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			pages++
			if request.Params.Cursor == "" {
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{
					"tools":      []map[string]any{{"name": "first", "inputSchema": map[string]any{"type": "object"}}},
					"_meta":      map[string]any{"page": "first"},
					"nextCursor": "next-page",
				})
				return
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"tools": []map[string]any{{"name": "second", "inputSchema": map[string]any{"type": "object"}}},
				"_meta": map[string]any{"page": "second"},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	defer service.Close()
	response, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if pages != 2 {
		t.Fatalf("tools/list pages = %d, want 2", pages)
	}
	if len(response.Data) != 1 || len(response.Data[0].Tools) != 2 {
		t.Fatalf("status = %#v, want both pages' tools", response.Data)
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Data[0].ToolListMeta, &decoded); err != nil {
		t.Fatalf("toolListMeta = %s, error %v", response.Data[0].ToolListMeta, err)
	}
	if decoded["page"] != "first" {
		t.Fatalf("toolListMeta = %#v, want the first page's metadata", decoded)
	}
}

// TestMCPToolListMetaSurvivesARetainedCatalogLikeRust covers the Rust #54686
// "through catalog refreshes and caches" half: metadata published next to a
// retained catalog stays attached to it, and a caller cannot alias it away.
func TestMCPToolListMetaSurvivesARetainedCatalogLikeRust(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: "http://127.0.0.1:1/mcp", Enabled: true, Required: true}},
	}})
	defer service.Close()
	if got := len(service.ConfiguredStatuses()); got != 1 {
		t.Fatalf("configured statuses = %d, want 1", got)
	}

	service.recordInventoryStatus("docs", MCPServerStatus{
		Name:         "docs",
		State:        MCPServerReady,
		Tools:        []MCPToolInfo{{Name: "search"}},
		ToolListMeta: json.RawMessage(`{"traceId":"retained"}`),
	}, true, true)

	retained := service.ConfiguredStatuses()
	if len(retained) != 1 {
		t.Fatalf("retained statuses = %#v", retained)
	}
	var decoded map[string]any
	if err := json.Unmarshal(retained[0].ToolListMeta, &decoded); err != nil {
		t.Fatalf("retained toolListMeta = %s, error %v", retained[0].ToolListMeta, err)
	}
	if decoded["traceId"] != "retained" {
		t.Fatalf("retained toolListMeta = %#v", decoded)
	}
	if len(retained[0].Tools) != 1 {
		t.Fatalf("retained tools = %#v", retained[0].Tools)
	}

	// The retained metadata must not alias the service's copy.
	if len(retained[0].ToolListMeta) > 0 {
		retained[0].ToolListMeta[2] = 'X'
	}
	again := service.ConfiguredStatuses()
	if string(again[0].ToolListMeta) != `{"traceId":"retained"}` {
		t.Fatalf("toolListMeta after caller mutation = %s", again[0].ToolListMeta)
	}
}
