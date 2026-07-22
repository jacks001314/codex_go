package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"codex_go/tool"
)

const (
	LegacyMCPToolNamePrefix = "mcp__"
	MCPToolNameDelimiter    = "__"
)

type ToolExecutorOptions struct {
	Service     *MCPService
	ServerName  string
	ToolInfo    *MCPToolInfo
	ToolName    tool.ToolName
	Parallel    bool
	ThreadID    string
	TurnID      string
	RequestMeta map[string]any
	Binding     *Binding
}

type ToolExecutor struct {
	service     *MCPService
	serverName  string
	toolInfo    MCPToolInfo
	toolName    tool.ToolName
	parallel    bool
	threadID    string
	turnID      string
	requestMeta map[string]any
	binding     *Binding
}

func NewToolExecutor(options *ToolExecutorOptions) *ToolExecutor {
	executor := &ToolExecutor{service: NewMCPService(nil)}
	if options == nil {
		return executor
	}
	if options.Service != nil {
		executor.service = options.Service
	}
	executor.serverName = strings.TrimSpace(options.ServerName)
	if options.ToolInfo != nil {
		executor.toolInfo = *options.ToolInfo
	}
	if options.ToolName.Key() != "" {
		executor.toolName = options.ToolName
	} else if executor.serverName != "" && executor.toolInfo.Name != "" {
		executor.toolName = tool.NamespacedName(executor.serverName, executor.toolInfo.Name)
	}
	executor.parallel = options.Parallel || mcpToolReadOnlyHint(executor.toolInfo.Annotations)
	executor.threadID = strings.TrimSpace(options.ThreadID)
	executor.turnID = strings.TrimSpace(options.TurnID)
	executor.requestMeta = cloneAnyMap(options.RequestMeta)
	executor.binding = options.Binding
	return executor
}

func RegisterToolExecutor(registry *tool.Registry, options *ToolExecutorOptions) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", tool.ErrToolInvalidCall)
	}
	return registry.Register(NewToolExecutor(options))
}

func RegisterToolExecutors(registry *tool.Registry, service *MCPService, tools []RuntimeToolInfo) error {
	tools = NormalizeRuntimeToolsForModel(tools)
	for i := range tools {
		info := runtimeToolInfoToMCPToolInfo(&tools[i])
		if err := RegisterToolExecutor(registry, &ToolExecutorOptions{
			Service:    service,
			ServerName: tools[i].ServerName,
			ToolInfo:   info,
			ToolName:   tool.NamespacedName(tools[i].CallableNamespace, tools[i].CallableName),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *ToolExecutor) Spec() tool.Spec {
	name := e.resolvedToolName()
	return tool.Spec{
		Name:        name,
		Description: firstNonEmptyMCP(e.toolInfo.Description, e.toolInfo.Title),
		InputSchema: cloneAnyMap(e.toolInfo.InputSchema),
		Search:      e.searchInfo(),
		Parallel:    e.parallel,
	}
}

func (e *ToolExecutor) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	_ = ctx
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.RespondToModel("mcp handler received unsupported payload")
	}
	arguments := mcpHookToolInput(invocation.Payload.Arguments)
	meta := e.requestMetaForCall(invocation.CallID)
	callParams := &MCPToolCallParams{
		ServerName: e.resolvedServerName(),
		ToolName:   e.resolvedRemoteToolName(),
		Arguments:  arguments,
		ThreadID:   e.threadID,
		TurnID:     e.turnID,
		ItemID:     invocation.CallID,
		Meta:       meta,
	}
	var response *MCPToolCallResponse
	var err error
	if e.binding != nil {
		response, err = e.binding.CallTool(callParams)
	} else {
		response, err = e.mcpService().CallTool(callParams)
	}
	if err != nil {
		return nil, err
	}
	body := MCPToolResponseText(response)
	data := mcpToolResponseData(response)
	if contentItems := mcpToolModelContentItems(response); len(contentItems) > 0 {
		data["content_items"] = contentItems
	}
	data["server"] = e.resolvedServerName()
	data["tool"] = e.resolvedRemoteToolName()
	return &tool.Output{
		Success:    !mcpToolCallIsError(response),
		Body:       body,
		Data:       data,
		LogPreview: mcpLogPreview(body),
	}, nil
}

func (e *ToolExecutor) requestMetaForCall(callID ...string) any {
	if e == nil {
		return nil
	}
	requestMeta := cloneAnyMap(e.requestMeta)
	if requestMeta == nil {
		requestMeta = map[string]any{}
	}
	if e.threadID != "" {
		requestMeta["thread_id"] = e.threadID
	}
	if IsCodexAppsMCPServerName(e.resolvedServerName()) {
		appsMeta, _ := requestMeta["_codex_apps"].(map[string]any)
		appsMeta = cloneAnyMap(appsMeta)
		if appsMeta == nil {
			appsMeta = map[string]any{}
		}
		if len(callID) > 0 && strings.TrimSpace(callID[0]) != "" {
			appsMeta["call_id"] = strings.TrimSpace(callID[0])
		}
		requestMeta["_codex_apps"] = appsMeta
	}
	if len(requestMeta) == 0 {
		return nil
	}
	return requestMeta
}

func (e *ToolExecutor) PreToolUsePayload(invocation *tool.Invocation) (*tool.PreToolUsePayload, bool) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, false
	}
	return &tool.PreToolUsePayload{
		ToolName:  e.hookToolName(),
		ToolInput: mcpHookToolInput(invocation.Payload.Arguments),
	}, true
}

func (e *ToolExecutor) PostToolUsePayload(invocation *tool.Invocation, output *tool.Output) (*tool.PostToolUsePayload, bool) {
	if invocation == nil || output == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, false
	}
	response := any(output.Data)
	if value, ok := output.Data["hook_response"]; ok {
		response = value
	}
	return &tool.PostToolUsePayload{
		ToolName:     e.hookToolName(),
		ToolUseID:    firstNonEmptyMCP(output.CallID, invocation.CallID),
		ToolInput:    mcpHookToolInput(invocation.Payload.Arguments),
		ToolResponse: response,
	}, true
}

func (e *ToolExecutor) WithUpdatedHookInput(invocation *tool.Invocation, updatedInput any) (*tool.Invocation, error) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		name := e.resolvedToolName()
		return nil, tool.RespondToModel(fmt.Sprintf("tool %s does not support hook input rewriting for this payload", name.Key()))
	}
	data, err := json.Marshal(updatedInput)
	if err != nil {
		return nil, tool.RespondToModel(fmt.Sprintf("failed to serialize rewritten MCP arguments: %v", err))
	}
	updated := *invocation
	updated.Payload.Arguments = string(data)
	return &updated, nil
}

func (e *ToolExecutor) resolvedToolName() tool.ToolName {
	if e == nil {
		return tool.ToolName{}
	}
	if e.toolName.Key() != "" {
		return e.toolName
	}
	if e.serverName != "" && e.toolInfo.Name != "" {
		return tool.NamespacedName(e.serverName, e.toolInfo.Name)
	}
	return tool.ToolName{}
}

func (e *ToolExecutor) resolvedServerName() string {
	if e == nil {
		return ""
	}
	if e.serverName != "" {
		return e.serverName
	}
	return e.resolvedToolName().Namespace
}

func (e *ToolExecutor) resolvedRemoteToolName() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.toolInfo.Name) != "" {
		return strings.TrimSpace(e.toolInfo.Name)
	}
	return e.resolvedToolName().Name
}

func (e *ToolExecutor) mcpService() *MCPService {
	if e == nil || e.service == nil {
		return NewMCPService(nil)
	}
	return e.service
}

func (e *ToolExecutor) hookToolName() *tool.HookToolName {
	return &tool.HookToolName{Name: EnsureMCPHookToolName(JoinToolName(e.resolvedToolName()))}
}

func (e *ToolExecutor) searchInfo() *tool.SearchInfo {
	text := strings.Join(e.searchTextParts(), " ")
	sourceName := e.resolvedServerName()
	if sourceName == "" {
		sourceName = "MCP"
	}
	return &tool.SearchInfo{
		Text: text,
		Source: &tool.SearchSourceInfo{
			Name: sourceName,
		},
	}
}

func (e *ToolExecutor) searchTextParts() []string {
	name := e.resolvedToolName()
	parts := []string{
		name.Key(),
		JoinToolName(name),
		e.resolvedRemoteToolName(),
		e.resolvedServerName(),
		e.toolInfo.Title,
		e.toolInfo.Description,
	}
	if e.toolInfo.InputSchema != nil {
		if properties, ok := e.toolInfo.InputSchema["properties"].(map[string]any); ok {
			names := make([]string, 0, len(properties))
			for name := range properties {
				names = append(names, name)
			}
			sort.Strings(names)
			parts = append(parts, names...)
		} else if encoded, err := json.Marshal(e.toolInfo.InputSchema); err == nil {
			parts = append(parts, string(encoded))
		}
	}
	return nonEmptyStrings(parts)
}

func JoinToolName(name tool.ToolName) string {
	if name.Namespace == "" {
		return name.Name
	}
	namespace := strings.TrimRight(name.Namespace, "_")
	toolName := strings.TrimLeft(name.Name, "_")
	return namespace + MCPToolNameDelimiter + toolName
}

func EnsureMCPHookToolName(name string) string {
	if strings.HasPrefix(name, LegacyMCPToolNamePrefix) {
		return name
	}
	return LegacyMCPToolNamePrefix + name
}

func MCPToolResponseText(response *MCPToolCallResponse) string {
	if response == nil {
		return ""
	}
	var parts []string
	for _, content := range response.Content {
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, content.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	encoded, err := json.Marshal(mcpToolResponseData(response))
	if err != nil {
		return ""
	}
	return string(encoded)
}

func mcpToolModelContentItems(response *MCPToolCallResponse) []any {
	if response == nil {
		return nil
	}
	items := make([]any, 0, len(response.Content))
	for i := range response.Content {
		item := response.Content[i].Map()
		switch item["type"] {
		case "text":
			text, _ := item["text"].(string)
			items = append(items, map[string]any{"type": "input_text", "text": text})
		case "encrypted_content":
			encrypted, _ := item["encrypted_content"].(string)
			if encrypted != "" {
				items = append(items, map[string]any{
					"type":              "encrypted_content",
					"encrypted_content": encrypted,
				})
			}
		}
	}
	return items
}

func mcpHookToolInput(rawArguments string) any {
	if strings.TrimSpace(rawArguments) == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(rawArguments), &value); err != nil {
		return rawArguments
	}
	return value
}

func mcpToolResponseData(response *MCPToolCallResponse) map[string]any {
	data := map[string]any{"mcpToolCall": true}
	if response == nil {
		return data
	}
	content := make([]map[string]any, 0, len(response.Content))
	for _, item := range response.Content {
		content = append(content, item.Map())
	}
	data["content"] = content
	if response.StructuredContent != nil {
		data["structuredContent"] = response.StructuredContent
	}
	if response.Meta != nil {
		data["_meta"] = response.Meta
	}
	if response.IsError != nil {
		data["isError"] = *response.IsError
	}
	data["hook_response"] = cloneMCPHookResponse(data)
	return data
}

func mcpToolCallIsError(response *MCPToolCallResponse) bool {
	return response != nil && response.IsError != nil && *response.IsError
}

func cloneMCPHookResponse(data map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range data {
		if key == "hook_response" || key == "isError" {
			continue
		}
		out[key] = value
	}
	return out
}

func runtimeToolInfoToMCPToolInfo(info *RuntimeToolInfo) *MCPToolInfo {
	if info == nil {
		return &MCPToolInfo{}
	}
	return &MCPToolInfo{
		Name:        info.Tool.Name,
		Title:       info.Tool.Title,
		Description: info.Tool.Description,
		InputSchema: cloneAnyMap(info.Tool.InputSchema),
		Annotations: info.Tool.Annotations,
	}
}

func mcpToolReadOnlyHint(annotations any) bool {
	encoded, err := json.Marshal(annotations)
	if err != nil {
		return false
	}
	var raw struct {
		ReadOnlyHint *bool `json:"readOnlyHint"`
	}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return false
	}
	return raw.ReadOnlyHint != nil && *raw.ReadOnlyHint
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func mcpLogPreview(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= 200 {
		return body
	}
	return body[:200]
}

var _ tool.Executor = (*ToolExecutor)(nil)
var _ tool.PreToolUsePayloadProvider = (*ToolExecutor)(nil)
var _ tool.PostToolUsePayloadProvider = (*ToolExecutor)(nil)
var _ tool.HookInputUpdater = (*ToolExecutor)(nil)
