package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"codex_go/mcp"
	"codex_go/model"
	"codex_go/tool"
)

// registerMCPResourceHandlers mirrors Rust #47677 add_mcp_resource_tools: the
// three built-in resource helpers take their catalog entries from
// model_messages.tools.mcp_resources for the current model.
func registerMCPResourceHandlers(registry *tool.Registry, service *mcp.MCPService, messages *model.McpResourceToolMessages, threadID ...string) error {
	if service == nil {
		return nil
	}
	id := ""
	if len(threadID) > 0 {
		id = strings.TrimSpace(threadID[0])
	}
	listMessages := (*model.ToolMessage)(nil)
	templateMessages := (*model.ToolMessage)(nil)
	readMessages := (*model.ToolMessage)(nil)
	if messages != nil {
		listMessages = messages.ListMCPResources
		templateMessages = messages.ListMCPResourceTemplates
		readMessages = messages.ReadMCPResource
	}
	for _, executor := range []tool.Executor{
		&mcpResourceListExecutor{service: service, threadID: id, message: listMessages}, &mcpResourceListExecutor{service: service, templates: true, threadID: id, message: templateMessages}, &mcpResourceReadExecutor{service: service, threadID: id, message: readMessages},
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
	message   *model.ToolMessage
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
	// Rust's MCP resource list handlers override
	// `supports_parallel_tool_calls` with true.
	return applyMCPResourceCatalogMessage(tool.Spec{Name: tool.PlainName(name), Description: description, Parallel: true, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"server": map[string]any{"type": "string", "description": "MCP server name. Omit to list resources from every configured server."}, "cursor": map[string]any{"type": "string", "description": cursor}}, "additionalProperties": false}}, h.message)
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
		return marshalMCPResourceTool(mcp.MCPResourceTemplatesFromAllServers(m), effectiveResourceServer(server), h.Spec().Name.Name)
	}
	m := map[string][]mcp.MCPResource{}
	for _, s := range status.Data {
		if server == "" || s.Server.Name == server {
			m[s.Server.Name] = append(m[s.Server.Name], s.Resources...)
		}
	}
	return marshalMCPResourceTool(mcp.MCPResourcesFromAllServers(m), effectiveResourceServer(server), h.Spec().Name.Name)
}

type mcpResourceReadExecutor struct {
	service  *mcp.MCPService
	threadID string
	message  *model.ToolMessage
}

func (h *mcpResourceReadExecutor) Spec() tool.Spec {
	return applyMCPResourceCatalogMessage(tool.Spec{Name: tool.PlainName("read_mcp_resource"), Description: "Read a specific resource from an MCP server given the server name and resource URI.", Parallel: true, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"server": map[string]any{"type": "string", "description": "MCP server name exactly as configured. Must match the 'server' field returned by list_mcp_resources."}, "uri": map[string]any{"type": "string", "description": "Resource URI to read. Must be one of the URIs returned by list_mcp_resources."}}, "required": []string{"server", "uri"}, "additionalProperties": false}}, h.message)
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
	return marshalMCPResourceTool(mcp.ReadResourcePayloadFromResponse(a.Server, r), a.Server, h.Spec().Name.Name)
}

// effectiveResourceServer mirrors Rust's default "codex" pseudo-server for the
// applyMCPResourceCatalogMessage mirrors Rust mcp_resource_spec::with_model_messages
// (#47677): description and parameter overrides apply independently, and an
// invalid parameter override keeps the bundled schema with a warning.
func applyMCPResourceCatalogMessage(spec tool.Spec, message *model.ToolMessage) tool.Spec {
	if message == nil {
		return spec
	}
	if message.Description != nil {
		spec.Description = *message.Description
	}
	if message.Parameters != nil {
		schema, err := tool.ParseCatalogParameters(*message.Parameters)
		if err != nil {
			slog.Warn(
				"Invalid catalog tool parameters; using bundled parameters",
				"tool", spec.Name.Key(),
				"reason", err,
			)
		} else {
			spec.InputSchema = schema
		}
	}
	return spec
}

// built-in list_mcp_resources/list_mcp_resource_templates tools.
func effectiveResourceServer(requested string) string {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested)
	}
	return "codex"
}

func marshalMCPResourceTool(v any, server string, toolName string) (*tool.Output, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return &tool.Output{
		Success: true,
		Body:    string(b),
		Data: map[string]any{
			"mcpToolCall": true,
			"server":      server,
			"tool":        toolName,
			"content":     []any{map[string]any{"type": "text", "text": string(b)}},
		},
	}, nil
}
