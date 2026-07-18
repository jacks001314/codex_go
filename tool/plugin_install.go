package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"codex_go/plugin"
)

const (
	ListAvailablePluginsToInstallToolName = "list_available_plugins_to_install"
	RequestPluginInstallToolName          = "request_plugin_install"

	maxPluginInstallCandidateDescriptionChars = 240

	requestPluginInstallApprovalKind      = "tool_suggestion"
	requestPluginInstallPersistKey        = "persist"
	requestPluginInstallPersistAlways     = "always"
	requestPluginInstallActionInstall     = "install"
	requestPluginInstallToolTypePlugin    = "plugin"
	requestPluginInstallToolTypeConnector = "connector"
	requestPluginInstallTUIClientName     = "codex-tui"
)

type PluginInstallSuggestionOptions struct {
	Candidates            []plugin.DiscoverableInfo
	RecommendationContext bool
	Runtime               PluginInstallRuntime
	AppServerClientName   string
}

type PluginInstallRuntime interface {
	RequestPluginInstall(ctx context.Context, request *PluginInstallRequest) (*PluginInstallRuntimeResult, error)
}

type PluginInstallRequest struct {
	CallID        string                    `json:"callId,omitempty"`
	SuggestionID  string                    `json:"suggestionId,omitempty"`
	ToolType      string                    `json:"toolType"`
	ActionType    string                    `json:"actionType"`
	Tool          plugin.DiscoverableInfo   `json:"tool"`
	SuggestReason string                    `json:"suggestReason"`
	Elicitation   *PluginInstallElicitation `json:"elicitation,omitempty"`
}

type PluginInstallElicitation struct {
	Message         string         `json:"message"`
	Meta            map[string]any `json:"_meta"`
	RequestedSchema map[string]any `json:"requestedSchema"`
}

type PluginInstallRuntimeResult struct {
	Sent           bool   `json:"sent"`
	UserConfirmed  bool   `json:"userConfirmed"`
	Completed      bool   `json:"completed"`
	ResponseAction string `json:"responseAction,omitempty"`
	PersistDisable bool   `json:"persistDisable,omitempty"`
}

type RequestPluginInstallEntry struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        *string  `json:"description,omitempty"`
	InstallURL         *string  `json:"install_url,omitempty"`
	ToolType           string   `json:"tool_type"`
	PluginDisplayNames []string `json:"plugin_display_names,omitempty"`
	HasSkills          bool     `json:"has_skills"`
	MCPServerNames     []string `json:"mcp_server_names"`
	AppConnectorIDs    []string `json:"app_connector_ids"`
}

type ListAvailablePluginsToInstallResult struct {
	Tools []RequestPluginInstallEntry `json:"tools"`
}

type RequestPluginInstallArgs struct {
	ToolType      string `json:"tool_type,omitempty"`
	ActionType    string `json:"action_type,omitempty"`
	ToolID        string `json:"tool_id,omitempty"`
	PluginID      string `json:"plugin_id,omitempty"`
	SuggestReason string `json:"suggest_reason"`
}

type RequestPluginInstallResult struct {
	Completed      bool   `json:"completed"`
	UserConfirmed  bool   `json:"user_confirmed"`
	ResponseAction string `json:"response_action,omitempty"`
	PersistDisable bool   `json:"persist_disable,omitempty"`
	ToolType       string `json:"tool_type"`
	ActionType     string `json:"action_type"`
	ToolID         string `json:"tool_id"`
	ToolName       string `json:"tool_name"`
	SuggestReason  string `json:"suggest_reason"`
	SuggestionID   string `json:"suggestion_id"`
	Elicitation    bool   `json:"elicitation"`
}

func RegisterPluginInstallSuggestionHandlers(registry *Registry, options *PluginInstallSuggestionOptions) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrToolInvalidCall)
	}
	if options == nil || len(options.Candidates) == 0 {
		return nil
	}
	candidates := cloneDiscoverablePluginCandidates(options.Candidates)
	if !options.RecommendationContext {
		if err := registry.Register(NewListAvailablePluginsToInstallHandler(candidates)); err != nil {
			return err
		}
	}
	return registry.Register(NewRequestPluginInstallHandler(&RequestPluginInstallHandlerOptions{
		Candidates:            candidates,
		RecommendationContext: options.RecommendationContext,
		Runtime:               options.Runtime,
		AppServerClientName:   options.AppServerClientName,
	}))
}

type ListAvailablePluginsToInstallHandler struct {
	candidates []plugin.DiscoverableInfo
}

func NewListAvailablePluginsToInstallHandler(candidates []plugin.DiscoverableInfo) *ListAvailablePluginsToInstallHandler {
	candidates = cloneDiscoverablePluginCandidates(candidates)
	sortDiscoverablePluginCandidates(candidates)
	return &ListAvailablePluginsToInstallHandler{candidates: candidates}
}

func (h *ListAvailablePluginsToInstallHandler) Spec() Spec {
	return Spec{
		Name:        PlainName(ListAvailablePluginsToInstallToolName),
		Description: "# List plugin/connector install candidates\n\nUse this tool only when both are true:\n- The user explicitly asks to use a specific plugin or connector that is not already available in the current context or active `tools` list.\n- `tool_search` is not available, or it has already been called and did not find or make the requested tool callable.\n\nReturns known plugins and connectors that can be passed to `request_plugin_install`. When both a plugin and a connector match, prefer the plugin; use the connector only when its corresponding plugin is already installed.\n",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
		Parallel: true,
	}
}

func (h *ListAvailablePluginsToInstallHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = ctx
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	if invocation.Payload.Kind != PayloadFunction {
		return nil, Fatal(ListAvailablePluginsToInstallToolName + " handler received unsupported payload")
	}
	result := ListAvailablePluginsToInstallResult{Tools: requestPluginInstallEntries(h.candidates)}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &Output{Success: true, Body: string(body), Data: map[string]any{"tools": requestPluginInstallEntriesAny(result.Tools)}}, nil
}

type RequestPluginInstallHandler struct {
	candidates            []plugin.DiscoverableInfo
	recommendationContext bool
	runtime               PluginInstallRuntime
	appServerClientName   string
}

type RequestPluginInstallHandlerOptions struct {
	Candidates            []plugin.DiscoverableInfo
	RecommendationContext bool
	Runtime               PluginInstallRuntime
	AppServerClientName   string
}

func NewRequestPluginInstallHandler(options *RequestPluginInstallHandlerOptions) *RequestPluginInstallHandler {
	if options == nil {
		options = &RequestPluginInstallHandlerOptions{}
	}
	candidates := cloneDiscoverablePluginCandidates(options.Candidates)
	sortDiscoverablePluginCandidates(candidates)
	return &RequestPluginInstallHandler{
		candidates:            candidates,
		recommendationContext: options.RecommendationContext,
		runtime:               options.Runtime,
		appServerClientName:   strings.TrimSpace(options.AppServerClientName),
	}
}

func (h *RequestPluginInstallHandler) Spec() Spec {
	properties := map[string]any{}
	required := []string{}
	description := "# Request plugin/connector install\n\nUse this tool only after `list_available_plugins_to_install` returns a plugin or connector that exactly matches the user's explicit request.\n\nDo not use it for adjacent capabilities, broad recommendations, or tools that merely seem useful. Pass the returned `tool_type` through directly, and pass the returned `id` as `tool_id`.\n\nIMPORTANT: DO NOT call this tool in parallel with other tools."
	if h != nil && h.recommendationContext {
		description = "# Suggest a recommended plugin installation\n\nSuggest installing a plugin from the `<recommended_plugins>` list when it would help with the user's current request. Briefly explain why in `suggest_reason`."
		properties["plugin_id"] = map[string]any{"type": "string", "description": "Plugin id from the `<recommended_plugins>` list."}
		properties["suggest_reason"] = map[string]any{"type": "string", "description": "Concise one-line user-facing reason why this plugin can help with the current request."}
		required = []string{"plugin_id", "suggest_reason"}
	} else {
		properties["tool_type"] = map[string]any{"type": "string", "description": "Type of discoverable tool to suggest. Use \"connector\" or \"plugin\"."}
		properties["action_type"] = map[string]any{"type": "string", "description": "Suggested action for the tool. Use \"install\"."}
		properties["tool_id"] = map[string]any{"type": "string", "description": "Connector or plugin id to suggest."}
		properties["suggest_reason"] = map[string]any{"type": "string", "description": "Concise one-line user-facing reason why this plugin or connector can help with the current request."}
		required = []string{"tool_type", "action_type", "tool_id", "suggest_reason"}
	}
	return Spec{
		Name:        PlainName(RequestPluginInstallToolName),
		Description: description,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		},
	}
}

func (h *RequestPluginInstallHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	if invocation.Payload.Kind != PayloadFunction {
		return nil, Fatal(RequestPluginInstallToolName + " handler received unsupported payload")
	}
	var args RequestPluginInstallArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	toolID := strings.TrimSpace(args.ToolID)
	requestedToolType := strings.TrimSpace(args.ToolType)
	if h != nil && h.recommendationContext {
		toolID = strings.TrimSpace(firstNonEmptyTool(args.PluginID, args.ToolID))
	} else if strings.TrimSpace(args.ActionType) != requestPluginInstallActionInstall {
		return nil, RespondToModel("plugin install requests currently support only action_type=\"install\"")
	}
	if toolID == "" {
		if h != nil && h.recommendationContext {
			return nil, RespondToModel("plugin_id must match one of the entries in the <recommended_plugins> list")
		}
		return nil, RespondToModel("tool_id must match one of the discoverable tools returned by list_available_plugins_to_install")
	}
	reason := strings.TrimSpace(args.SuggestReason)
	if reason == "" {
		return nil, RespondToModel("suggest_reason must not be empty")
	}
	if h != nil && (h.recommendationContext || requestedToolType == requestPluginInstallToolTypePlugin) && h.appServerClientName == requestPluginInstallTUIClientName {
		return nil, RespondToModel("plugin install requests are not available in codex-tui yet")
	}
	if h == nil {
		return nil, fmt.Errorf("%w: request plugin install handler is nil", ErrToolInvalidCall)
	}
	candidate, ok := findPluginInstallCandidate(h.candidates, toolID, requestedToolType, h.recommendationContext)
	if !ok {
		if h != nil && h.recommendationContext {
			return nil, RespondToModel("plugin_id must match one of the entries in the <recommended_plugins> list")
		}
		return nil, RespondToModel("tool_id must match one of the discoverable tools returned by list_available_plugins_to_install")
	}
	if h.appServerClientName == requestPluginInstallTUIClientName && discoverableCandidateToolType(&candidate) == requestPluginInstallToolTypePlugin {
		return nil, RespondToModel("plugin install requests are not available in codex-tui yet")
	}
	suggestionID := "request_plugin_install_" + strings.TrimSpace(invocation.CallID)
	if strings.TrimSpace(invocation.CallID) == "" {
		suggestionID = "request_plugin_install"
	}
	runtimeResult := &PluginInstallRuntimeResult{}
	if h.runtime != nil {
		request := &PluginInstallRequest{
			CallID:        invocation.CallID,
			SuggestionID:  suggestionID,
			ToolType:      discoverableCandidateToolType(&candidate),
			ActionType:    requestPluginInstallActionInstall,
			Tool:          cloneDiscoverablePluginCandidate(candidate),
			SuggestReason: reason,
			Elicitation:   buildPluginInstallElicitation(reason, &candidate),
		}
		if result, err := h.runtime.RequestPluginInstall(ctx, request); err == nil && result != nil {
			runtimeResult = result
		}
	}
	result := RequestPluginInstallResult{
		Completed:      runtimeResult.Completed,
		UserConfirmed:  runtimeResult.UserConfirmed,
		ResponseAction: strings.TrimSpace(runtimeResult.ResponseAction),
		PersistDisable: runtimeResult.PersistDisable,
		ToolType:       discoverableCandidateToolType(&candidate),
		ActionType:     requestPluginInstallActionInstall,
		ToolID:         candidate.ID,
		ToolName:       candidate.Name,
		SuggestReason:  reason,
		SuggestionID:   suggestionID,
		Elicitation:    runtimeResult.Sent,
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &Output{Success: true, Body: string(body), Data: map[string]any{
		"completed":       result.Completed,
		"user_confirmed":  result.UserConfirmed,
		"tool_type":       result.ToolType,
		"action_type":     result.ActionType,
		"tool_id":         result.ToolID,
		"tool_name":       result.ToolName,
		"suggest_reason":  result.SuggestReason,
		"suggestion_id":   suggestionID,
		"elicitation":     runtimeResult.Sent,
		"response_action": result.ResponseAction,
		"persist_disable": result.PersistDisable,
	}}, nil
}

func requestPluginInstallEntries(candidates []plugin.DiscoverableInfo) []RequestPluginInstallEntry {
	out := make([]RequestPluginInstallEntry, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, RequestPluginInstallEntry{
			ID:                 candidate.ID,
			Name:               candidate.Name,
			Description:        stringPtrTool(truncateRunes(candidate.Description, maxPluginInstallCandidateDescriptionChars)),
			InstallURL:         stringPtrTool(candidate.InstallURL),
			ToolType:           discoverableCandidateToolType(&candidate),
			PluginDisplayNames: append([]string(nil), candidate.PluginDisplayNames...),
			HasSkills:          candidate.HasSkills,
			MCPServerNames:     append([]string(nil), candidate.MCPServerNames...),
			AppConnectorIDs:    append([]string(nil), candidate.AppConnectorIDs...),
		})
	}
	return out
}

func requestPluginInstallEntriesAny(entries []RequestPluginInstallEntry) []any {
	out := make([]any, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	return out
}

func findPluginInstallCandidate(candidates []plugin.DiscoverableInfo, id string, requestedToolType string, recommendationContext bool) (plugin.DiscoverableInfo, bool) {
	for _, candidate := range candidates {
		if candidate.ID != id && candidate.RemotePluginID != id {
			continue
		}
		toolType := discoverableCandidateToolType(&candidate)
		if recommendationContext {
			if toolType == requestPluginInstallToolTypePlugin {
				return candidate, true
			}
			continue
		}
		if strings.TrimSpace(requestedToolType) == "" || strings.TrimSpace(requestedToolType) == toolType {
			return candidate, true
		}
	}
	return plugin.DiscoverableInfo{}, false
}

func buildPluginInstallElicitation(reason string, candidate *plugin.DiscoverableInfo) *PluginInstallElicitation {
	if candidate == nil {
		candidate = &plugin.DiscoverableInfo{}
	}
	meta := map[string]any{
		"codex_approval_kind":          requestPluginInstallApprovalKind,
		requestPluginInstallPersistKey: requestPluginInstallPersistAlways,
		"tool_type":                    discoverableCandidateToolType(candidate),
		"suggest_type":                 requestPluginInstallActionInstall,
		"suggest_reason":               reason,
		"tool_id":                      candidate.ID,
		"tool_name":                    candidate.Name,
		"app_connector_ids":            append([]string(nil), candidate.AppConnectorIDs...),
	}
	if len(candidate.PluginDisplayNames) > 0 {
		meta["plugin_display_names"] = append([]string(nil), candidate.PluginDisplayNames...)
	}
	if strings.TrimSpace(candidate.RemotePluginID) != "" {
		meta["remote_plugin_id"] = strings.TrimSpace(candidate.RemotePluginID)
	}
	if strings.TrimSpace(candidate.InstallURL) != "" {
		meta["install_url"] = strings.TrimSpace(candidate.InstallURL)
	}
	return &PluginInstallElicitation{
		Message: reason,
		Meta:    meta,
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func cloneDiscoverablePluginCandidates(candidates []plugin.DiscoverableInfo) []plugin.DiscoverableInfo {
	out := make([]plugin.DiscoverableInfo, len(candidates))
	for i := range candidates {
		out[i] = cloneDiscoverablePluginCandidate(candidates[i])
	}
	return out
}

func cloneDiscoverablePluginCandidate(candidate plugin.DiscoverableInfo) plugin.DiscoverableInfo {
	candidate.PluginDisplayNames = append([]string(nil), candidate.PluginDisplayNames...)
	candidate.MCPServerNames = append([]string(nil), candidate.MCPServerNames...)
	candidate.AppConnectorIDs = append([]string(nil), candidate.AppConnectorIDs...)
	return candidate
}

func discoverableCandidateToolType(candidate *plugin.DiscoverableInfo) string {
	if candidate == nil {
		return requestPluginInstallToolTypePlugin
	}
	switch strings.TrimSpace(candidate.ToolType) {
	case requestPluginInstallToolTypeConnector:
		return requestPluginInstallToolTypeConnector
	default:
		return requestPluginInstallToolTypePlugin
	}
}

func sortDiscoverablePluginCandidates(candidates []plugin.DiscoverableInfo) {
	sort.SliceStable(candidates, func(i int, j int) bool {
		if candidates[i].Name == candidates[j].Name {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Name < candidates[j].Name
	})
}

func truncateRunes(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func stringPtrTool(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func firstNonEmptyTool(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

var _ Executor = (*ListAvailablePluginsToInstallHandler)(nil)
var _ Executor = (*RequestPluginInstallHandler)(nil)
