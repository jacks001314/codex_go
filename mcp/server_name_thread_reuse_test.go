package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestListStatusServerNameReusesThreadCatalogWithoutRelistingLikeRust mirrors
// Rust #48783 (ea64727556): with a thread and a selected `serverName`, the read
// answers from that thread's current connection and already materialized tool
// catalog (`CodexThread::mcp_server_status_snapshot` ->
// `McpRuntime::server_status_snapshot` ->
// `collect_mcp_server_status_snapshot_from_manager`), so it neither connects
// again nor repeats `initialize`/`tools/list`, and the snapshot read
// materializes nothing for the catalog size telemetry.
func TestListStatusServerNameReusesThreadCatalogWithoutRelistingLikeRust(t *testing.T) {
	var initializeCount, listCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			initializeCount.Add(1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "thread-server", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			listCount.Add(1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"thread": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	defer service.Close()

	threadID := "thread-status"
	first, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ThreadID: &threadID,
		Detail:   &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || first == nil || len(first.Servers) != 1 {
		t.Fatalf("first ListStatusChecked() response=%#v err=%v", first, err)
	}
	if len(first.Servers[0].Tools) != 1 || first.Servers[0].Tools[0].Name != "echo" {
		t.Fatalf("first tools = %#v, want echo", first.Servers[0].Tools)
	}
	if got := listCount.Load(); got != 1 {
		t.Fatalf("tools/list count after the first read = %d, want 1", got)
	}

	var batches int
	SetBindingCatalogTelemetryObserver(func([]BindingCatalogTelemetry) { batches++ })
	defer SetBindingCatalogTelemetryObserver(nil)

	name := "thread"
	selected, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ThreadID:   &threadID,
		ServerName: &name,
		Detail:     &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || selected == nil {
		t.Fatalf("selected ListStatusChecked() response=%#v err=%v", selected, err)
	}
	if len(selected.Servers) != 1 || selected.Servers[0].Name != "thread" {
		t.Fatalf("selected page = %#v, want only thread", selected.Servers)
	}
	if len(selected.Servers[0].Tools) != 1 || selected.Servers[0].Tools[0].Name != "echo" {
		t.Fatalf("selected tools = %#v, want the thread's materialized echo", selected.Servers[0].Tools)
	}
	if got := listCount.Load(); got != 1 {
		t.Fatalf("tools/list count after the selected read = %d, want 1 (the thread catalog is reused)", got)
	}
	if got := initializeCount.Load(); got != 1 {
		t.Fatalf("initialize count after the selected read = %d, want 1 (connection reused)", got)
	}
	if batches != 0 {
		t.Fatalf("catalog telemetry batches = %d, want 0 (the snapshot read materializes no catalog)", batches)
	}
}

// TestListStatusServerNameWithoutThreadStillDiscoversLikeRust pins the arm Rust
// keeps outside the thread branch: with no thread the selected server is
// discovered (and connected) as usual, so the reuse above is thread-scoped.
func TestListStatusServerNameWithoutThreadStillDiscoversLikeRust(t *testing.T) {
	var listCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "discover-server", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			listCount.Add(1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"discover": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	defer service.Close()

	name := "discover"
	first, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ServerName: &name,
		Detail:     &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || first == nil || len(first.Servers) != 1 || len(first.Servers[0].Tools) != 1 {
		t.Fatalf("discovery read response=%#v err=%v", first, err)
	}
	second, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ServerName: &name,
		Detail:     &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || second == nil || len(second.Servers) != 1 || len(second.Servers[0].Tools) != 1 {
		t.Fatalf("second discovery read response=%#v err=%v", second, err)
	}
	if got := listCount.Load(); got != 2 {
		t.Fatalf("tools/list count = %d, want 2 (a threadless read keeps discovering)", got)
	}
}
