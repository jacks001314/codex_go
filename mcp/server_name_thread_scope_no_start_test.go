package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestListStatusServerNameThreadScopeDoesNotStartServerLikeRust mirrors Rust
// #48783 (ea64727556): a (thread, serverName) read answers from that thread's
// current state through `CodexThread::mcp_server_status_snapshot` ->
// `McpRuntime::server_status_snapshot` ->
// `collect_mcp_server_status_snapshot_from_manager`, which only reads the
// manager's published server infos and tool catalog. A server this thread never
// started therefore stays dormant: the read neither connects (no `initialize` /
// `tools/list`) nor materializes the catalog, and reports the status it already
// has. The `...WithoutThreadStillDiscoversLikeRust` case pins the other arm:
// without a thread the same selected server is still discovered, so the rule is
// thread-scoped and Rust #38217's lazy startup is untouched.
func TestListStatusServerNameThreadScopeDoesNotStartServerLikeRust(t *testing.T) {
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
				"serverInfo":      map[string]string{"name": "dormant-server", "version": "test"},
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

	var batches int
	SetBindingCatalogTelemetryObserver(func([]BindingCatalogTelemetry) { batches++ })
	defer SetBindingCatalogTelemetryObserver(nil)

	threadID := "thread-never-started"
	name := "thread"
	scoped, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ThreadID:   &threadID,
		ServerName: &name,
		Detail:     &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || scoped == nil || len(scoped.Servers) != 1 {
		t.Fatalf("thread-scoped ListStatusChecked() response=%#v err=%v", scoped, err)
	}
	if got := scoped.Servers[0].Name; got != name {
		t.Fatalf("thread-scoped page = %#v, want only %q", scoped.Servers, name)
	}
	if got := initializeCount.Load(); got != 0 {
		t.Fatalf("initialize count for the thread-scoped read = %d, want 0 (a status read must not start the server)", got)
	}
	if got := listCount.Load(); got != 0 {
		t.Fatalf("tools/list count for the thread-scoped read = %d, want 0 (a status read must not materialize the catalog)", got)
	}
	if tools := scoped.Servers[0].Tools; len(tools) != 0 {
		t.Fatalf("thread-scoped tools = %#v, want the dormant catalog reported as-is", tools)
	}
	if batches != 0 {
		t.Fatalf("catalog telemetry batches = %d, want 0 (nothing was materialized)", batches)
	}

	// Without a thread the selected server is still discovered and connected
	// (Rust #38217 lazy startup), so the no-start rule above stays thread-scoped.
	unscoped, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ServerName: &name,
		Detail:     &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || unscoped == nil || len(unscoped.Servers) != 1 {
		t.Fatalf("unscoped ListStatusChecked() response=%#v err=%v", unscoped, err)
	}
	if got := initializeCount.Load(); got != 1 {
		t.Fatalf("initialize count after the unscoped read = %d, want 1", got)
	}
	if got := listCount.Load(); got != 1 {
		t.Fatalf("tools/list count after the unscoped read = %d, want 1", got)
	}
	if tools := unscoped.Servers[0].Tools; len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unscoped tools = %#v, want echo", tools)
	}
}
