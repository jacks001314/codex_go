package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestListStatusServerNameLimitsDiscoveryLikeRust mirrors Rust #48783
// (ea64727556): a selected serverName limits discovery to that server, so
// unrelated servers are neither started nor connected; an unknown name yields
// an empty page; omitting the name preserves full-inventory discovery.
func TestListStatusServerNameLimitsDiscoveryLikeRust(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"alpha": {Config: ServerConfig{Command: "codex-go-missing-server-name-alpha", Enabled: true}},
		"beta":  {Config: ServerConfig{Command: "codex-go-missing-server-name-beta", Enabled: true}},
	}})
	defer service.Close()

	updates := map[string]int{}
	collect := func(name string, _ MCPServerStartupState, _ *string, _ error) { updates[name]++ }

	alpha := "alpha"
	response, err := service.ListStatusCheckedWithObserver(&MCPListServerStatusParams{
		ServerName: &alpha,
		Detail:     &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	}, collect)
	if err != nil || response == nil {
		t.Fatalf("ListStatusCheckedWithObserver() response=%#v err=%v", response, err)
	}
	if len(response.Servers) != 1 || response.Servers[0].Name != "alpha" {
		t.Fatalf("selected page = %#v, want only alpha", response.Servers)
	}
	if updates["beta"] != 0 {
		t.Fatalf("beta startup updates = %d, want 0 (unselected servers are not started)", updates["beta"])
	}
	if updates["alpha"] == 0 {
		t.Fatalf("alpha startup updates = 0, want the selected server to be discovered")
	}
	// The unselected server keeps its seeded ready state instead of a failed
	// discovery, proving it was never connected.
	if status := service.ConfiguredStatuses(); len(status) != 2 || status[1].State != MCPServerReady {
		t.Fatalf("configured statuses = %#v, want beta still ready", status)
	}

	unknown := "missing-server"
	empty, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ServerName: &unknown,
		Detail:     &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || empty == nil {
		t.Fatalf("unknown name response=%#v err=%v", empty, err)
	}
	if len(empty.Servers) != 0 || empty.NextCursor != nil {
		t.Fatalf("unknown name page = %#v cursor=%v, want empty", empty.Servers, empty.NextCursor)
	}

	full, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || full == nil {
		t.Fatalf("full response=%#v err=%v", full, err)
	}
	if len(full.Servers) != 2 {
		t.Fatalf("full page = %#v, want both servers", full.Servers)
	}
}

// TestListStatusServerNameReusesConnectionLikeRust mirrors Rust #48783's
// regression assertion that a selected-server status read reuses the thread's
// current connection instead of connecting again: `initialize` is issued once
// and not repeated by the selected-server read.
func TestListStatusServerNameReusesConnectionLikeRust(t *testing.T) {
	var initializeCount atomic.Int64
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
				"serverInfo":      map[string]string{"name": "status-server", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	defer service.Close()

	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	}); err != nil {
		t.Fatalf("first ListStatusChecked() error = %v", err)
	}
	if got := initializeCount.Load(); got != 1 {
		t.Fatalf("initialize count after the first read = %d, want 1", got)
	}

	httpName := "http"
	selected, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ServerName: &httpName,
		Detail:     &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || selected == nil {
		t.Fatalf("selected ListStatusChecked() response=%#v err=%v", selected, err)
	}
	if len(selected.Servers) != 1 || selected.Servers[0].Name != "http" {
		t.Fatalf("selected page = %#v, want only http", selected.Servers)
	}
	if len(selected.Servers[0].Tools) != 1 || selected.Servers[0].Tools[0].Name != "echo" {
		t.Fatalf("selected tools = %#v, want echo", selected.Servers[0].Tools)
	}
	if got := initializeCount.Load(); got != 1 {
		t.Fatalf("initialize count after the selected read = %d, want 1 (connection reused)", got)
	}
}
