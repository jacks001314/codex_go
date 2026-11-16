package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type recordingMCPStatusServer struct {
	initialize int64
	tools      int64
	resources  int64
	templates  int64
}

func newRecordingMCPStatusServer(t *testing.T) (*recordingMCPStatusServer, *httptest.Server) {
	t.Helper()
	counts := &recordingMCPStatusServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			atomic.AddInt64(&counts.initialize, 1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
				"serverInfo":      map[string]string{"name": "widget", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			atomic.AddInt64(&counts.tools, 1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"tools": []map[string]any{{"name": "open_widget"}},
			})
		case "resources/list":
			atomic.AddInt64(&counts.resources, 1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"resources": []map[string]any{{"uri": "ui://widget.html", "name": "widget"}},
			})
		case "resources/templates/list":
			atomic.AddInt64(&counts.templates, 1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"resourceTemplates": []map[string]any{{"uriTemplate": "ui://widgets/{id}", "name": "widget by ID"}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	t.Cleanup(server.Close)
	return counts, server
}

func (c *recordingMCPStatusServer) snapshot() (initialize, tools, resources, templates int64) {
	return atomic.LoadInt64(&c.initialize), atomic.LoadInt64(&c.tools),
		atomic.LoadInt64(&c.resources), atomic.LoadInt64(&c.templates)
}

// TestPaginatedThreadStatusReusesFixedCredentialInventoryLikeRust mirrors Rust
// #61738: a paginated ToolsAndAuthOnly status read on a thread answers from a
// connection whose static Authorization header keeps its credentials fixed, so
// it does not repeat tools/list. Lightweight reads also skip the resource
// listings, while Full detail still discovers resources and templates.
func TestPaginatedThreadStatusReusesFixedCredentialInventoryLikeRust(t *testing.T) {
	counts, server := newRecordingMCPStatusServer(t)
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"widget": {Config: ServerConfig{
			URL:         server.URL,
			Enabled:     true,
			HTTPHeaders: map[string]string{"Authorization": "Bearer fixed-test-token"},
		}},
	}})
	defer service.Close()

	threadID := "thread-fixed"
	lightweight := &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}
	first, err := service.ListStatusChecked(&MCPListServerStatusParams{ThreadID: &threadID, Detail: lightweight})
	if err != nil || first == nil || len(first.Data) != 1 {
		t.Fatalf("first ListStatusChecked() response=%#v err=%v", first, err)
	}
	if _, tools, resources, templates := counts.snapshot(); tools != 1 || resources != 0 || templates != 0 {
		t.Fatalf("first read tools/resources/templates = %d/%d/%d, want 1/0/0", tools, resources, templates)
	}

	again, err := service.ListStatusChecked(&MCPListServerStatusParams{ThreadID: &threadID, Detail: lightweight})
	if err != nil || again == nil || len(again.Data) != 1 {
		t.Fatalf("second ListStatusChecked() response=%#v err=%v", again, err)
	}
	if len(again.Data[0].Tools) != 1 || again.Data[0].Tools[0].Name != "open_widget" {
		t.Fatalf("second tools = %#v, want the retained open_widget catalog", again.Data[0].Tools)
	}
	if initialize, tools, resources, templates := counts.snapshot(); initialize != 1 || tools != 1 || resources != 0 || templates != 0 {
		t.Fatalf("lightweight reuse initialize/tools/resources/templates = %d/%d/%d/%d, want 1/1/0/0",
			initialize, tools, resources, templates)
	}

	full, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ThreadID: &threadID,
		Detail:   &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull},
	})
	if err != nil || full == nil || len(full.Data) != 1 {
		t.Fatalf("full ListStatusChecked() response=%#v err=%v", full, err)
	}
	if initialize, tools, resources, templates := counts.snapshot(); initialize != 1 || tools != 2 || resources != 1 || templates != 1 {
		t.Fatalf("full discovery initialize/tools/resources/templates = %d/%d/%d/%d, want 1/2/1/1",
			initialize, tools, resources, templates)
	}
	if len(full.Data[0].Resources) != 1 || len(full.Data[0].ResourceTemplates) != 1 {
		t.Fatalf("full inventory = %#v / %#v, want one resource and one template",
			full.Data[0].Resources, full.Data[0].ResourceTemplates)
	}
}

// TestPaginatedThreadStatusRediscoversEnvironmentCredentialsLikeRust mirrors the
// other half of Rust #61738: a connection whose credential comes from the
// environment may change between reads, so a paginated ToolsAndAuthOnly read
// lists its tools again instead of reusing the published catalog.
func TestPaginatedThreadStatusRediscoversEnvironmentCredentialsLikeRust(t *testing.T) {
	t.Setenv("CODEX_TEST_MCP_STATUS_TOKEN", "env-token")
	counts, server := newRecordingMCPStatusServer(t)
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"widget": {Config: ServerConfig{
			URL:               server.URL,
			Enabled:           true,
			BearerTokenEnvVar: "CODEX_TEST_MCP_STATUS_TOKEN",
		}},
	}})
	defer service.Close()

	threadID := "thread-env"
	detail := &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}
	for read := 1; read <= 2; read++ {
		response, err := service.ListStatusChecked(&MCPListServerStatusParams{ThreadID: &threadID, Detail: detail})
		if err != nil || response == nil || len(response.Data) != 1 {
			t.Fatalf("read %d ListStatusChecked() response=%#v err=%v", read, response, err)
		}
		if len(response.Data[0].Tools) != 1 {
			t.Fatalf("read %d tools = %#v, want open_widget", read, response.Data[0].Tools)
		}
		if _, tools, resources, templates := counts.snapshot(); tools != int64(read) || resources != 0 || templates != 0 {
			t.Fatalf("read %d tools/resources/templates = %d/%d/%d, want %d/0/0",
				read, tools, resources, templates, read)
		}
	}
}
