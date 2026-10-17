package mcp

import (
	"errors"
	"testing"
)

func TestBindingRejectsCatalogRefreshAndInvisibleTools(t *testing.T) {
	service := NewMCPService(nil)
	service.SetServer(MCPServerStatus{Name: "docs", State: MCPServerReady, Tools: []MCPToolInfo{{Name: "read"}}})
	binding := service.CaptureBinding(service.ListStatus(nil).Data)
	if _, err := binding.CallTool(&MCPToolCallParams{ServerName: "docs", ToolName: "write"}); !errors.Is(err, ErrInvalidMCPRequest) {
		t.Fatalf("invisible tool err=%v", err)
	}
	service.Refresh()
	if _, err := binding.CallTool(&MCPToolCallParams{ServerName: "docs", ToolName: "read"}); !errors.Is(err, ErrInvalidMCPRequest) {
		t.Fatalf("stale binding err=%v", err)
	}
}

func TestBindingPreservesCachedToolsForNonReadyServersLikeRust(t *testing.T) {
	service := NewMCPService(nil)
	binding := service.CaptureBinding([]MCPServerStatus{
		{
			Name:  "pending",
			State: MCPServerStarting,
			Tools: []MCPToolInfo{{Name: "peer_tool"}},
		},
		{
			Name:  "failed-with-cache",
			State: MCPServerFailed,
			Tools: []MCPToolInfo{{Name: "cached_tool"}},
		},
		{Name: "pending-empty", State: MCPServerStarting},
		{Name: CodexAppsServerName, State: MCPServerStarting},
	})
	if !binding.tools["pending"]["peer_tool"] {
		t.Fatalf("pending cached catalog was dropped: %#v", binding.tools)
	}
	if !binding.tools["failed-with-cache"]["cached_tool"] {
		t.Fatalf("failed server's retained catalog was dropped: %#v", binding.tools)
	}
	if _, present := binding.tools["pending-empty"]; present {
		t.Fatalf("empty non-ready catalog was captured: %#v", binding.tools)
	}
	if _, present := binding.tools[CodexAppsServerName]; !present {
		t.Fatalf("Apps cached empty catalog was dropped: %#v", binding.tools)
	}
}
