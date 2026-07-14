package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/internal/mcp"
	"codex_go/internal/tool"
)

func registerMCPResourceHandlers(registry *tool.Registry, service *mcp.MCPService, threadID ...string) error {
	if service == nil {
		return nil
	}
	id := ""
	if len(threadID) > 0 {
		id = strings.TrimSpace(threadID[0])
	}
	for _, executor := range []tool.Executor{
		&mcpResourceListExecutor{service: service, threadID: id}, &mcpResourceListExecutor{service: service, templates: true, threadID: id}, &mcpResourceReadExecutor{service: service, threadID: id},
	} {
		if err := registry.Register(executor); err != nil {
			return err
		}
	}
	return nil
}

type mcpResourceListExecutor struct {
	service   *mcp.MCPService
	templates bool
	threadID  string
}

func (h *mcpResourceListExecutor) Spec() tool.Spec {
	name := "list_mcp_resources"
	description := "Lists resources provided by MCP servers. Resources allow servers to share data that provides context to language models, such as files, database schemas, or application-specific information. Prefer resources over web search when possible."
	cursor := "Opaque cursor from a previous list_mcp_resources call; omit for the first page."
	if h.templates {
		name = "list_mcp_resource_templates"
		description = "Lists resource templates provided by MCP servers. Parameterized resource templates allow servers to share data that provides context to language models, such as files, database schemas, or application-specific information. Prefer resource templates over web search when possible."
		cursor = "Opaque cursor from a previous list_mcp_resource_templates call; omit for the first page."
	}
	return tool.Spec{Name: tool.PlainName(name), Description: description, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"server": map[string]any{"type": "string", "description": "MCP server name. Omit to list resources from every configured server."}, "cursor": map[string]any{"type": "string", "description": cursor}}, "additionalProperties": false}}
}
func (h *mcpResourceListExecutor) Execute(ctx context.Context, inv *tool.Invocation) (*tool.Output, error) {
	_ = ctx
	if inv == nil {
		return nil, fmt.Errorf("%w: invocation is nil", tool.ErrToolInvalidCall)
	}
	if inv.Payload.Kind != tool.PayloadFunction {
		return nil, tool.RespondToModel(h.Spec().Name.Name + " handler received unsupported payload")
	}
	var a struct {
		Server string `json:"server"`
		Cursor string `json:"cursor"`
	}
	if err := inv.DecodeArguments(&a); err != nil {
		return nil, err
	}
	server := strings.TrimSpace(a.Server)
	if server == "" && strings.TrimSpace(a.Cursor) != "" {
		return nil, tool.RespondToModel("cursor can only be used when a server is specified")
	}
	detail := &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull}
	params := &mcp.MCPListServerStatusParams{Detail: detail}
	if h.threadID != "" {
		params.ThreadID = &h.threadID
	}
	status, err := h.service.ListStatusChecked(params)
	if err != nil {
		return nil, err
	}
	if h.templates {
		m := map[string][]mcp.MCPResourceTemplate{}
		for _, s := range status.Data {
			if server == "" || s.Server.Name == server {
				m[s.Server.Name] = append(m[s.Server.Name], s.ResourceTemplates...)
			}
		}
		return marshalMCPResourceTool(mcp.MCPResourceTemplatesFromAllServers(m))
	}
	m := map[string][]mcp.MCPResource{}
	for _, s := range status.Data {
		if server == "" || s.Server.Name == server {
			m[s.Server.Name] = append(m[s.Server.Name], s.Resources...)
		}
	}
	return marshalMCPResourceTool(mcp.MCPResourcesFromAllServers(m))
}

type mcpResourceReadExecutor struct {
	service  *mcp.MCPService
	threadID string
}

func (h *mcpResourceReadExecutor) Spec() tool.Spec {
	return tool.Spec{Name: tool.PlainName("read_mcp_resource"), Description: "Read a specific resource from an MCP server given the server name and resource URI.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"server": map[string]any{"type": "string", "description": "MCP server name exactly as configured. Must match the 'server' field returned by list_mcp_resources."}, "uri": map[string]any{"type": "string", "description": "Resource URI to read. Must be one of the URIs returned by list_mcp_resources."}}, "required": []string{"server", "uri"}, "additionalProperties": false}}
}
func (h *mcpResourceReadExecutor) Execute(ctx context.Context, inv *tool.Invocation) (*tool.Output, error) {
	_ = ctx
	if inv == nil {
		return nil, fmt.Errorf("%w: invocation is nil", tool.ErrToolInvalidCall)
	}
	if inv.Payload.Kind != tool.PayloadFunction {
		return nil, tool.RespondToModel("read_mcp_resource handler received unsupported payload")
	}
	var a mcp.ReadMCPResourceArgs
	if err := inv.DecodeArguments(&a); err != nil {
		return nil, err
	}
	if err := a.Validate(); err != nil {
		return nil, tool.RespondToModel("server and uri are required")
	}
	params := &mcp.MCPResourceReadParams{Server: a.Server, URI: a.URI}
	if h.threadID != "" {
		params.ThreadID = &h.threadID
	}
	r, err := h.service.ReadResource(params)
	if err != nil {
		return nil, tool.RespondToModel(fmt.Sprintf("resources/read failed: %v", err))
	}
	return marshalMCPResourceTool(mcp.ReadResourcePayloadFromResponse(a.Server, r))
}
func marshalMCPResourceTool(v any) (*tool.Output, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return &tool.Output{Success: true, Body: string(b)}, nil
}
