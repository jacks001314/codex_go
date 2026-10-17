package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAppsStartupFailureRetainsCachedToolsLikeRust(t *testing.T) {
	const toolName = "calendar_list_events"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "startup failed", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {
			Name: CodexAppsServerName,
			Config: ServerConfig{
				URL:     server.URL,
				Enabled: true,
			},
		},
	}})
	t.Cleanup(func() { _ = service.Close() })
	service.SetServer(MCPServerStatus{
		Name:  CodexAppsServerName,
		State: MCPServerReady,
		Tools: []MCPToolInfo{{
			Name:        toolName,
			Description: "Cached Calendar tool",
			InputSchema: map[string]any{"type": "object"},
		}},
	})

	status, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
		NonBlockingOptional:  true,
		OptionalStartupGrace: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 {
		t.Fatalf("status data = %#v, want one Apps server", status.Data)
	}
	apps := status.Data[0]
	if apps.State != MCPServerFailed || apps.Error == nil {
		t.Fatalf("Apps status = %#v, want failed with startup error", apps)
	}
	if len(apps.Tools) != 1 || apps.Tools[0].Name != toolName {
		t.Fatalf("cached Apps tools = %#v, want %q retained", apps.Tools, toolName)
	}
	binding := service.CaptureBinding(status.Data)
	if !binding.tools[CodexAppsServerName][toolName] {
		t.Fatalf("binding lost cached Apps tool: %#v", binding.tools)
	}
}

func TestOrdinaryStartupFailureStillClearsToolsLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "startup failed", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {
			Name: "docs",
			Config: ServerConfig{
				URL:     server.URL,
				Enabled: true,
			},
		},
	}})
	t.Cleanup(func() { _ = service.Close() })
	service.SetServer(MCPServerStatus{
		Name:  "docs",
		State: MCPServerReady,
		Tools: []MCPToolInfo{{Name: "stale_tool"}},
	})

	status, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
		NonBlockingOptional:  true,
		OptionalStartupGrace: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].State != MCPServerFailed {
		t.Fatalf("ordinary status = %#v, want failed", status.Data)
	}
	if len(status.Data[0].Tools) != 0 {
		t.Fatalf("ordinary failed server retained tools: %#v", status.Data[0].Tools)
	}
}
