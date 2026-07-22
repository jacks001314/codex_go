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
