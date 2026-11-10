package turn

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/mcp"
	"codex_go/tool"
)

// TestMCPResourceListingReportsDeferredServersLikeRust pins Rust #60423: an
// aggregate listing returns resources from the servers that answered and names
// the servers whose startup was deferred, while an explicit query still reads
// that server.
func TestMCPResourceListingReportsDeferredServersLikeRust(t *testing.T) {
	notStarted := mcp.MCPConnectionNotStarted
	service := mcp.NewMCPService(nil)
	service.SetServer(mcp.MCPServerStatus{
		Server:            mcp.MCPServerInfo{Name: "ready"},
		State:             mcp.MCPServerReady,
		Tools:             []mcp.MCPToolInfo{{Name: "read"}},
		Resources:         []mcp.MCPResource{{URI: "file://ready", Name: "ready"}},
		ResourceTemplates: []mcp.MCPResourceTemplate{{URITemplate: "file://ready/{id}", Name: "ready"}},
	})
	service.SetServer(mcp.MCPServerStatus{
		Server:        mcp.MCPServerInfo{Name: "dormant"},
		RuntimeStatus: &notStarted,
	})

	h := &mcpResourceListExecutor{service: service}
	out, err := h.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var payload mcp.ListMCPResourcesPayload
	if err := json.Unmarshal([]byte(out.Body), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(payload.Resources) != 1 || payload.Resources[0].Server != "ready" {
		t.Fatalf("resources = %#v, want only the ready server", payload.Resources)
	}
	wantDeferred := []string{"dormant"}
	if len(payload.DeferredServers) != 1 || payload.DeferredServers[0] != wantDeferred[0] {
		t.Fatalf("deferredServers = %#v, want %#v", payload.DeferredServers, wantDeferred)
	}

	th := &mcpResourceListExecutor{service: service, templates: true}
	tout, err := th.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`}})
	if err != nil {
		t.Fatalf("templates Execute() error = %v", err)
	}
	var tpayload mcp.ListMCPResourceTemplatesPayload
	if err := json.Unmarshal([]byte(tout.Body), &tpayload); err != nil {
		t.Fatalf("templates Unmarshal() error = %v", err)
	}
	if len(tpayload.ResourceTemplates) != 1 || tpayload.ResourceTemplates[0].Server != "ready" {
		t.Fatalf("templates = %#v, want only the ready server", tpayload.ResourceTemplates)
	}
	if len(tpayload.DeferredServers) != 1 || tpayload.DeferredServers[0] != wantDeferred[0] {
		t.Fatalf("templates deferredServers = %#v, want %#v", tpayload.DeferredServers, wantDeferred)
	}

	// An explicit server query keeps the single-server payload shape: no
	// deferred list, and the named server's own resources.
	out, err = h.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"server":"dormant"}`}})
	if err != nil {
		t.Fatalf("Execute(dormant) error = %v", err)
	}
	var single mcp.ListMCPResourcesPayload
	if err := json.Unmarshal([]byte(out.Body), &single); err != nil {
		t.Fatalf("Unmarshal(dormant) error = %v", err)
	}
	if len(single.DeferredServers) != 0 {
		t.Fatalf("single-server deferredServers = %#v, want none", single.DeferredServers)
	}
	if out.Data["server"] != "dormant" {
		t.Fatalf("single-server output server = %#v, want dormant", out.Data["server"])
	}
}
