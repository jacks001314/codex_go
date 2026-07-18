package turn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/mcp"
	"codex_go/tool"
)

func TestMCPResourceToolsMatchRustSurface(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerMCPResourceHandlers(registry, mcp.NewMCPService(nil)); err != nil {
		t.Fatalf("registerMCPResourceHandlers() error = %v", err)
	}
	for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
		spec, ok := registry.Spec(tool.PlainName(name))
		if !ok {
			t.Fatalf("missing MCP resource tool %q", name)
		}
		if spec.InputSchema["type"] != "object" || spec.InputSchema["additionalProperties"] != false {
			t.Fatalf("%s schema = %#v", name, spec.InputSchema)
		}
	}
	read, _ := registry.Spec(tool.PlainName("read_mcp_resource"))
	required, ok := read.InputSchema["required"].([]string)
	if !ok || len(required) != 2 || required[0] != "server" || required[1] != "uri" {
		t.Fatalf("read_mcp_resource required = %#v", read.InputSchema["required"])
	}
}

func TestMCPResourceListPreservesServerNamesAcrossInventory(t *testing.T) {
	service := mcp.NewMCPService(nil)
	service.SetServer(mcp.MCPServerStatus{Server: mcp.MCPServerInfo{Name: "zeta"}, Resources: []mcp.MCPResource{{URI: "file://z", Name: "z"}}})
	service.SetServer(mcp.MCPServerStatus{Server: mcp.MCPServerInfo{Name: "alpha"}, Resources: []mcp.MCPResource{{URI: "file://a", Name: "a"}}})
	h := &mcpResourceListExecutor{service: service}
	out, err := h.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var payload mcp.ListMCPResourcesPayload
	if err := json.Unmarshal([]byte(out.Body), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(payload.Resources) != 2 || payload.Resources[0].Server != "alpha" || payload.Resources[1].Server != "zeta" {
		t.Fatalf("resources = %#v", payload.Resources)
	}
}

func TestMCPResourceListRejectsCursorWithoutServerLikeRust(t *testing.T) {
	h := &mcpResourceListExecutor{service: mcp.NewMCPService(nil)}
	_, err := h.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cursor":"next"}`}})
	if err == nil || !strings.Contains(err.Error(), "cursor can only be used when a server is specified") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestMCPResourceReadUsesThreadScopedRootsAndPayloadErrorsLikeRust(t *testing.T) {
	service := mcp.NewMCPService(nil)
	seenThread := ""
	service.SetRootsProvider(mcp.MCPRootsProviderFunc(func(threadID string) []mcp.MCPRoot { seenThread = threadID; return nil }))
	h := &mcpResourceReadExecutor{service: service, threadID: "thread-live"}
	if _, err := h.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadCustom}}); err == nil || !strings.Contains(err.Error(), "unsupported payload") {
		t.Fatalf("unsupported payload error = %v", err)
	}
	if _, err := h.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"server":"docs","uri":"file://x"}`}}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if seenThread != "thread-live" {
		t.Fatalf("roots provider thread = %q", seenThread)
	}
}
