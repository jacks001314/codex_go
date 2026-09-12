package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"codex_go/jsonschema"
	"codex_go/tool"
	"codex_go/utils"
)

const (
	LegacyMCPToolNamePrefix = "mcp__"
	MCPToolNameDelimiter    = "__"
	openAIFileHookInputKey  = "_codex_openai_file_arguments"
	// ConfirmationPoliciesMetaKey mirrors Rust protocol::mcp::
	// CONFIRMATION_POLICIES_META_KEY; host-supplied confirmation-policy
	// documents forwarded to Node REPL-backed actor calls (#41072).
	ConfirmationPoliciesMetaKey = "openai/confirmation_policies"
	// maxSerializedMCPToolBytes mirrors Rust MAX_SERIALIZED_MCP_TOOL_BYTES:
	// the cap above which Agent Plugin v1 tools degrade to an accept-anything
	// object schema (responses_api.rs).
	maxSerializedMCPToolBytes = 8_000
)

// ActorConfirmationPolicies carries the issuing model's Browser Use and native
// Computer Use confirmation-policy Markdown (Rust
// protocol::openai_models::ConfirmationPolicies, #41072), forwarded verbatim in
// the openai/confirmation_policies request metadata for node_repl/cua_repl
// actor calls.
type ActorConfirmationPolicies struct {
	BrowserUse  string
	ComputerUse string
}

// IsNodeReplBackedServer mirrors Rust protocol::mcp::is_node_repl_backed_server:
// whether a raw MCP server name identifies a Node REPL-backed actor server.
func IsNodeReplBackedServer(server string) bool {
	return server == "node_repl" || server == "cua_repl"
}

func mustMarshalJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}

type ToolExecutorOptions struct {
	Service                       *MCPService
	ServerName                    string
	ServerOrigin                  string
	ToolInfo                      *MCPToolInfo
	ToolName                      tool.ToolName
	ConnectorID                   string
	ConnectorName                 string
	Model                         string
	Parallel                      bool
	ThreadID                      string
	TurnID                        string
	RequestMeta                   map[string]any
	Binding                       *Binding
	OpenAIFileRewriter            *OpenAIFileRewriter
	OpenAIFileInputOptionalFields map[string][]string
	// AgentPlugin marks tools contributed by Agent Plugins v1 servers, which
	// get Rust's oversized-schema fallback in Spec (mirrors
	// agent_plugin_mcp_tool_to_responses_api_tool).
	AgentPlugin bool
	// ConfirmationPolicies carries the issuing model's confirmation-policy
	// Markdown (#41072), forwarded verbatim in the openai/confirmation_policies
	// request metadata for node_repl/cua_repl actor calls. Nil still attaches an
	// empty object (clearing startup defaults).
	ConfirmationPolicies *ActorConfirmationPolicies
	// SuppressActorConfirmationPolicies omits the confirmation-policies metadata
	// entirely (Guardian review sessions), mirroring Rust's basic-session gate.
	SuppressActorConfirmationPolicies bool
	// AuthElicitation enables the Codex Apps connector URL elicitation flow for
	// this turn (Rust maybe_request_codex_apps_auth_elicitation). The caller
	// gates it on the auth_elicitation feature and the approval policy.
	AuthElicitation *AuthElicitationOptions
}

// AuthElicitationOptions carries the turn-scoped hooks the Codex Apps auth
// elicitation flow needs. Request asks the client to complete a URL
// elicitation, RefreshCodexApps re-lists the Codex Apps catalog after the user
// accepts, and InstallURL resolves the connector install URL.
type AuthElicitationOptions struct {
	Request          func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error)
	RefreshCodexApps func(ctx context.Context) error
	InstallURL       func(connectorName string, connectorID string) string
}

type ToolExecutor struct {
	service                       *MCPService
	serverName                    string
	serverOrigin                  string
	toolInfo                      MCPToolInfo
	toolName                      tool.ToolName
	connectorID                   string
	connectorName                 string
	model                         string
	parallel                      bool
	readOnlyHint                  *bool
	threadID                      string
	turnID                        string
	requestMeta                   map[string]any
	binding                       *Binding
	openAIFileRewriter            *OpenAIFileRewriter
	openAIFileInputOptionalFields map[string][]string
	agentPlugin                   bool
	confirmationPolicies          *ActorConfirmationPolicies
	suppressActorPolicies         bool
	authElicitation               *AuthElicitationOptions
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
	executor.serverOrigin = strings.TrimSpace(options.ServerOrigin)
	if options.ToolInfo != nil {
		executor.toolInfo = *options.ToolInfo
	}
	if options.ToolName.Key() != "" {
		executor.toolName = options.ToolName
	} else if executor.serverName != "" && executor.toolInfo.Name != "" {
		executor.toolName = tool.NamespacedName(executor.serverName, executor.toolInfo.Name)
	}
	executor.readOnlyHint = mcpToolReadOnlyHint(executor.toolInfo.Annotations)
	executor.parallel = options.Parallel || (executor.readOnlyHint != nil && *executor.readOnlyHint)
	executor.threadID = strings.TrimSpace(options.ThreadID)
	executor.turnID = strings.TrimSpace(options.TurnID)
	executor.requestMeta = cloneAnyMap(options.RequestMeta)
	executor.binding = options.Binding
	executor.connectorID = strings.TrimSpace(options.ConnectorID)
	executor.connectorName = strings.TrimSpace(options.ConnectorName)
	executor.model = strings.TrimSpace(options.Model)
	executor.openAIFileRewriter = options.OpenAIFileRewriter
	executor.openAIFileInputOptionalFields = cloneOpenAIFileOptionalFields(options.OpenAIFileInputOptionalFields)
	executor.agentPlugin = options.AgentPlugin
	executor.confirmationPolicies = options.ConfirmationPolicies
	executor.suppressActorPolicies = options.SuppressActorConfirmationPolicies
	executor.authElicitation = options.AuthElicitation
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
			Service:                       service,
			ServerName:                    tools[i].ServerName,
			ServerOrigin:                  tools[i].ServerOrigin,
			ToolInfo:                      info,
			ToolName:                      tool.NamespacedName(tools[i].CallableNamespace, tools[i].CallableName),
			ConnectorID:                   tools[i].ConnectorID,
			OpenAIFileInputOptionalFields: tools[i].OpenAIFileInputOptionalFields,
			AgentPlugin:                   tools[i].AgentPlugin,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *ToolExecutor) TelemetryTags(_ *tool.Invocation) map[string]string {
	if e == nil {
		return nil
	}
	tags := map[string]string{"mcp_server": e.resolvedServerName()}
	if e.serverOrigin != "" {
		tags["mcp_server_origin"] = e.serverOrigin
	}
	return tags
}

func (e *ToolExecutor) Spec() tool.Spec {
	name := e.resolvedToolName()
	parameters := jsonschema.Normalize(cloneAnyMap(e.toolInfo.InputSchema))
	// Mirrors Rust agent_plugin_mcp_tool_to_responses_api_tool: Agent Plugins
	// v1 tools that still exceed the serialized-size cap after normalization
	// degrade to an accept-anything object schema instead of reaching the
	// model oversized.
	if e.agentPlugin && len(mustMarshalJSON(parameters)) > maxSerializedMCPToolBytes {
		parameters = map[string]any{"type": "object", "additionalProperties": true}
	}
	return tool.Spec{
		Name:        name,
		Description: firstNonEmptyMCP(e.toolInfo.Description, e.toolInfo.Title),
		// Mirrors Rust mcp_tool_to_responses_api_tool: the model-visible
		// parameters go through the JsonSchema subset policy (sanitize, prune
		// unreachable $defs, drop non-subset fields, compact oversized
		// schemas); the raw schema stays on the executor for calls.
		InputSchema:  parameters,
		Search:       e.searchInfo(),
		Parallel:     e.parallel,
		ReadOnlyHint: cloneBoolPtrMCP(e.readOnlyHint),
	}
}

func (e *ToolExecutor) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.RespondToModel("mcp handler received unsupported payload")
	}
	arguments := mcpHookToolInput(invocation.Payload.Arguments)
	rewrittenArguments := any(nil)
	if e.openAIFileRewriter != nil && len(e.openAIFileInputOptionalFields) > 0 {
		rewritten, err := e.openAIFileRewriter.RewriteArgumentsWithOptionalFields(ctx, arguments, e.openAIFileInputOptionalFields, e.hostedFileUploadContext())
		if err != nil {
			return nil, tool.RespondToModel(err.Error())
		}
		arguments = rewritten
		rewrittenArguments = rewritten
	}
	meta := e.requestMetaForCall(invocation.CallID)
	callParams := &MCPToolCallParams{
		ServerName: e.resolvedServerName(),
		ToolName:   e.resolvedRemoteToolName(),
		Arguments:  arguments,
		ThreadID:   e.threadID,
		TurnID:     e.turnID,
		ItemID:     invocation.CallID,
		CallID:     invocation.CallID,
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
		if output, ok := mcpAuthenticationChallengeToolOutput(err); ok {
			return output, nil
		}
		return nil, err
	}
	// Rust maybe_request_codex_apps_auth_elicitation: a Codex Apps tool call that
	// failed because the connector needs authentication can prompt the client to
	// authorize it; on acceptance the model receives a completed result to retry.
	response = e.maybeRequestCodexAppsAuthElicitation(ctx, invocation.CallID, response)
	// Rust #41421: carry the effective per-tool output budget so tool output,
	// post-tool hook responses, and resumed sessions share the same truncation
	// limit. Resolve the configured per-tool limit and truncate the response
	// text before it reaches the model.
	if limit := e.resolvedOutputTokenLimit(); limit > 0 {
		truncateMCPResponseToOutputTokenLimit(response, limit)
	}
	// Rust #40737: MCP results become typed function-call output content items.
	// Encrypted content and unstructured content stay as content items, while
	// structured content is serialized to text.
	body, contentItems, useContentItems := mcpFunctionCallOutput(response)
	data := mcpToolResponseData(response)
	if useContentItems {
		data["content_items"] = contentItems
	}
	data["server"] = e.resolvedServerName()
	data["tool"] = e.resolvedRemoteToolName()
	if e.readOnlyHint != nil {
		data["read_only_hint"] = *e.readOnlyHint
	}
	if rewrittenArguments != nil {
		data[openAIFileHookInputKey] = rewrittenArguments
	}
	return &tool.Output{
		Success:    !mcpToolCallIsError(response),
		Body:       body,
		Data:       data,
		LogPreview: mcpLogPreview(body),
	}, nil
}

// maybeRequestCodexAppsAuthElicitation mirrors Rust's
// maybe_request_codex_apps_auth_elicitation: for a Codex Apps tool call whose
// result carries a connector auth failure, ask the client to complete the
// connector's URL elicitation and, when the user accepts, refresh the Codex Apps
// catalog and return a completed result telling the model to retry.
func (e *ToolExecutor) maybeRequestCodexAppsAuthElicitation(ctx context.Context, callID string, result *MCPToolCallResponse) *MCPToolCallResponse {
	if e == nil || e.authElicitation == nil || result == nil {
		return result
	}
	if e.resolvedServerName() != RuntimeCodexAppsMCPServerName {
		return result
	}
	connectorID := strings.TrimSpace(e.connectorID)
	if connectorID == "" {
		return result
	}
	connectorName := strings.TrimSpace(e.connectorName)
	if connectorName == "" {
		connectorName = connectorID
	}
	installURL := ""
	if e.authElicitation.InstallURL != nil {
		installURL = e.authElicitation.InstallURL(connectorName, connectorID)
	}
	plan := BuildAuthElicitationPlan(callID, result, connectorID, connectorName, installURL)
	if plan == nil || plan.Elicitation == nil {
		return result
	}
	if e.authElicitation.Request == nil {
		return result
	}
	elicitationRequest := &MCPElicitationRequest{
		ServerName:    RuntimeCodexAppsMCPServerName,
		ThreadID:      e.threadID,
		TurnID:        e.turnID,
		Method:        "elicitation/create",
		Message:       plan.Elicitation.Message,
		URL:           plan.Elicitation.URL,
		ElicitationID: plan.Elicitation.ElicitationID,
		Meta:          plan.Elicitation.Meta,
	}
	response, err := e.authElicitation.Request(ctx, elicitationRequest)
	if err != nil || response == nil || response.Action != MCPElicitationActionAccept {
		return result
	}
	if e.authElicitation.RefreshCodexApps != nil {
		_ = e.authElicitation.RefreshCodexApps(ctx)
	}
	return AuthElicitationCompletedResult(plan.AuthFailure, result.Meta)
}

// mcpAuthenticationChallengeToolOutput converts a 401 Unauthorized MCP
// response carrying WWW-Authenticate headers into a model-visible tool error
// with the combined challenge preserved under `mcp/www_authenticate` (Rust
// #42552). It returns false for other errors so normal handling is unchanged.
func mcpAuthenticationChallengeToolOutput(err error) (*tool.Output, bool) {
	// Rust #43947: a local expired credential that could not be refreshed uses
	// the same reconnect signal as a server-rejected call, with a fixed
	// challenge so token-endpoint and transport details stay hidden.
	var authRequired *mcpAuthenticationRequiredError
	if errors.As(err, &authRequired) {
		message := authRequired.Error()
		return &tool.Output{
			Success:    false,
			Body:       message,
			Error:      message,
			LogPreview: mcpLogPreview(message),
			Data: map[string]any{
				"mcp/www_authenticate": `Bearer error="invalid_token"`,
			},
		}, true
	}
	var statusErr *mcpHTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized || len(statusErr.WWWAuthenticate) == 0 {
		return nil, false
	}
	message := statusErr.Error()
	return &tool.Output{
		Success:    false,
		Body:       message,
		Error:      message,
		LogPreview: mcpLogPreview(message),
		Data: map[string]any{
			"mcp/www_authenticate": statusErr.CombinedWWWAuthenticate(),
		},
	}, true
}

// resolvedOutputTokenLimit returns the configured per-tool output token limit
// for this server/tool, resolved from the MCP service server config (Rust
// #41421 PreparedMcpCall::output_token_limit). 0 means no per-tool limit.
func (e *ToolExecutor) resolvedOutputTokenLimit() int {
	if e == nil || e.service == nil {
		return 0
	}
	limit := e.service.ConfiguredToolOutputLimit(e.resolvedServerName(), e.resolvedRemoteToolName())
	if limit == nil || *limit <= 0 {
		return 0
	}
	return *limit
}

// truncateMCPResponseToOutputTokenLimit truncates the text content of an MCP
// tool response to the given output token budget, preserving non-text content
// items (Rust #41421). It strips the text from over-budget content items so the
// model does not observe an unbounded tool result.
func truncateMCPResponseToOutputTokenLimit(response *MCPToolCallResponse, limit int) {
	if response == nil || limit <= 0 {
		return
	}
	totalTokens := 0
	for i := range response.Content {
		totalTokens += utils.ApproxTokenCount(response.Content[i].Text)
	}
	if totalTokens <= limit {
		return
	}
	remaining := limit
	for i := range response.Content {
		tokens := utils.ApproxTokenCount(response.Content[i].Text)
		if tokens <= remaining {
			remaining -= tokens
			continue
		}
		response.Content[i].Text = utils.TruncateText(response.Content[i].Text, utils.TokensPolicy(remaining))
		remaining = 0
	}
}

func (e *ToolExecutor) WaitUntilReady(ctx context.Context, invocation *tool.Invocation) error {
	threadID := e.threadID
	if invocation != nil && invocation.Context != nil {
		if value, ok := invocation.Context["thread_id"].(string); ok && strings.TrimSpace(value) != "" {
			threadID = strings.TrimSpace(value)
		}
	}
	return e.mcpService().WaitForServerStartup(ctx, e.resolvedServerName(), threadID)
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
	if IsNodeReplBackedServer(e.resolvedServerName()) && !e.suppressActorPolicies {
		requestMeta[ConfirmationPoliciesMetaKey] = e.actorConfirmationPoliciesMeta()
	}
	if len(requestMeta) == 0 {
		return nil
	}
	return requestMeta
}

// actorConfirmationPoliciesMeta builds the openai/confirmation_policies value for
// an eligible actor call. It is always a (possibly empty) object so runtimes
// can clear startup defaults; text is forwarded verbatim.
func (e *ToolExecutor) actorConfirmationPoliciesMeta() map[string]any {
	policies := map[string]any{}
	if e == nil || e.confirmationPolicies == nil {
		return policies
	}
	if e.confirmationPolicies.BrowserUse != "" {
		policies["browser_use"] = e.confirmationPolicies.BrowserUse
	}
	if e.confirmationPolicies.ComputerUse != "" {
		policies["computer_use"] = e.confirmationPolicies.ComputerUse
	}
	return policies
}

func (e *ToolExecutor) PreToolUsePayload(invocation *tool.Invocation) (*tool.PreToolUsePayload, bool) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, false
	}
	return &tool.PreToolUsePayload{
		ToolName:  e.hookToolName(),
		ToolInput: mcpHookToolInput(invocation.Payload.Arguments),
		McpTool:   e.mcpToolContext(),
	}, true
}

// mcpToolContext captures the model-visible MCP tool details and its source
// classification for tool lifecycle extensions (Rust McpToolContext, #40976).
// The executable client is never exposed.
func (e *ToolExecutor) mcpToolContext() *tool.McpToolContext {
	if e == nil {
		return nil
	}
	ctx := &tool.McpToolContext{
		ServerName: strings.TrimSpace(e.serverName),
		ToolName:   strings.TrimSpace(e.toolInfo.Name),
		Connector:  strings.TrimSpace(e.connectorID),
		Source:     tool.McpToolSourceOther,
	}
	if ctx.Connector != "" {
		ctx.Source = tool.McpToolSourceConnector
	} else if e.agentPlugin {
		ctx.Source = tool.McpToolSourcePlugin
	} else if e.serverName != "" {
		ctx.Source = tool.McpToolSourceConfig
	}
	return ctx
}

func (e *ToolExecutor) PostToolUsePayload(invocation *tool.Invocation, output *tool.Output) (*tool.PostToolUsePayload, bool) {
	if invocation == nil || output == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, false
	}
	response := any(output.Data)
	if value, ok := output.Data["hook_response"]; ok {
		response = value
	}
	toolInput := mcpHookToolInput(invocation.Payload.Arguments)
	if value, ok := output.Data[openAIFileHookInputKey]; ok {
		toolInput = value
	}
	return &tool.PostToolUsePayload{
		ToolName:     e.hookToolName(),
		ToolUseID:    firstNonEmptyMCP(output.CallID, invocation.CallID),
		ToolInput:    toolInput,
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

// mcpFunctionCallOutput mirrors Rust CallToolResult::as_function_call_output_payload
// (#40737): a single encrypted content item forces content items, otherwise a
// non-null structured result is serialized to text, and everything else becomes
// typed content items.
func mcpFunctionCallOutput(response *MCPToolCallResponse) (body string, contentItems []any, useContentItems bool) {
	contentItems = mcpToolModelContentItems(response)
	for _, item := range contentItems {
		if entry, ok := item.(map[string]any); ok && entry["type"] == "encrypted_content" {
			return MCPToolResponseText(response), contentItems, true
		}
	}
	if response != nil && response.StructuredContent != nil {
		encoded, err := json.Marshal(response.StructuredContent)
		if err != nil {
			return err.Error(), nil, false
		}
		return string(encoded), nil, false
	}
	return MCPToolResponseText(response), contentItems, true
}

// MCP content metadata keys mirror Rust CODEX_ENCRYPTED_CONTENT_META_KEY /
// CODEX_IMAGE_DETAIL_META_KEY.
const (
	mcpEncryptedContentMetaKey = "codex/encryptedContent"
	mcpImageDetailMetaKey      = "codex/imageDetail"
)

// mcpToolModelContentItems mirrors Rust convert_mcp_content_to_items: text,
// image, and audio become typed items; unknown or malformed content is
// preserved as serialized text so no result is silently dropped.
func mcpToolModelContentItems(response *MCPToolCallResponse) []any {
	if response == nil {
		return []any{}
	}
	items := make([]any, 0, len(response.Content))
	for i := range response.Content {
		items = append(items, mcpContentToModelItem(response.Content[i].Map()))
	}
	return items
}

func mcpContentToModelItem(content map[string]any) any {
	contentType, _ := content["type"].(string)
	switch contentType {
	case "text":
		text, ok := content["text"].(string)
		if !ok {
			return mcpUnknownContentItem(content)
		}
		if meta, ok := content["_meta"].(map[string]any); ok {
			if encrypted, _ := meta[mcpEncryptedContentMetaKey].(bool); encrypted {
				return map[string]any{"type": "encrypted_content", "encrypted_content": text}
			}
		}
		return map[string]any{"type": "input_text", "text": text}
	case "image":
		data, ok := content["data"].(string)
		if !ok {
			return mcpUnknownContentItem(content)
		}
		return map[string]any{
			"type":      "input_image",
			"image_url": mcpContentDataURL(data, mcpContentMimeType(content)),
			"detail":    mcpContentImageDetail(content),
		}
	case "audio":
		data, ok := content["data"].(string)
		if !ok {
			return mcpUnknownContentItem(content)
		}
		return map[string]any{
			"type":      "input_audio",
			"audio_url": mcpContentDataURL(data, mcpContentMimeType(content)),
		}
	default:
		return mcpUnknownContentItem(content)
	}
}

// mcpContentDataURL builds a data URL when the payload is not already one.
func mcpContentDataURL(data string, mimeType string) string {
	if strings.HasPrefix(data, "data:") {
		return data
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + data
}

func mcpContentMimeType(content map[string]any) string {
	if value, ok := content["mimeType"].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	if value, ok := content["mime_type"].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return ""
}

// mcpContentImageDetail mirrors Rust's default image detail (`high`) and its
// `codex/imageDetail` override.
func mcpContentImageDetail(content map[string]any) string {
	meta, _ := content["_meta"].(map[string]any)
	detail, _ := meta[mcpImageDetailMetaKey].(string)
	switch detail {
	case "auto", "low", "high", "original":
		return detail
	default:
		return "high"
	}
}

func mcpUnknownContentItem(content map[string]any) any {
	encoded, err := json.Marshal(content)
	if err != nil {
		return map[string]any{"type": "input_text", "text": "<content>"}
	}
	return map[string]any{"type": "input_text", "text": string(encoded)}
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
		Meta:        cloneJSONValue(info.Meta),
	}
}

// hostedFileUploadContext mirrors Rust's hosted-upload derivation in
// mcp_tool_call.rs (#38101): for Codex Apps MCP tools with both a connector ID
// and an action name (from the _codex_apps resource_uri), the OpenAI file
// upload is annotated with connector/action/model.
func (e *ToolExecutor) hostedFileUploadContext() *HostedFileUploadContext {
	if e == nil || !IsCodexAppsMCPServerName(e.resolvedServerName()) {
		return nil
	}
	connectorID := strings.TrimSpace(e.connectorID)
	actionName := mcpToolCallActionName(e.toolInfo.Meta)
	model := strings.TrimSpace(e.model)
	if connectorID == "" || actionName == "" || model == "" {
		return nil
	}
	return &HostedFileUploadContext{
		ConnectorID: connectorID,
		ActionName:  actionName,
		Model:       model,
	}
}

// mcpToolCallActionName derives the hosted-app action name from the tool's
// _codex_apps metadata: the last path segment of the resource_uri (Rust
// MCP_TOOL_RESOURCE_URI_META_KEY = "resource_uri").
func mcpToolCallActionName(meta any) string {
	base := metadataMap(meta)
	if base == nil {
		return ""
	}
	for _, key := range []string{"_codex_apps", "codex_apps", "codexApps"} {
		nested := metadataMap(base[key])
		if nested == nil {
			continue
		}
		raw, ok := nested["resource_uri"]
		if !ok {
			continue
		}
		uri, ok := raw.(string)
		if !ok {
			continue
		}
		uri = strings.Trim(uri, "/")
		if index := strings.LastIndex(uri, "/"); index >= 0 {
			uri = uri[index+1:]
		}
		uri = strings.TrimSpace(uri)
		if uri != "" {
			return uri
		}
	}
	return ""
}

func mcpToolReadOnlyHint(annotations any) *bool {
	encoded, err := json.Marshal(annotations)
	if err != nil {
		return nil
	}
	var raw struct {
		ReadOnlyHint *bool `json:"readOnlyHint"`
	}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return nil
	}
	return cloneBoolPtrMCP(raw.ReadOnlyHint)
}

func cloneBoolPtrMCP(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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
