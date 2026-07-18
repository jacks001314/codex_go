package appserver

import (
	"context"
	"strings"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/plugin"
	"codex_go/tool"
)

const codexAppsMCPServerName = "codex_apps"

type pluginInstallRuntime struct {
	broker   *ServerRequestBroker
	plugins  *plugin.PluginService
	apps     *apps.AppService
	config   *config.ConfigService
	threadID string
	turnID   string
}

func (r *pluginInstallRuntime) RequestPluginInstall(ctx context.Context, request *tool.PluginInstallRequest) (*tool.PluginInstallRuntimeResult, error) {
	result := &tool.PluginInstallRuntimeResult{}
	if r == nil || request == nil || r.broker == nil {
		result.ResponseAction = "unavailable"
		return result, nil
	}
	elicitation := request.Elicitation
	if elicitation == nil {
		elicitation = &tool.PluginInstallElicitation{
			Message:         request.SuggestReason,
			Meta:            map[string]any{},
			RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}
	}
	turnID := strings.TrimSpace(r.turnID)
	params := &MCPElicitationRequestParams{
		ThreadID:        strings.TrimSpace(r.threadID),
		TurnID:          stringPtrIfNotEmptyAppserver(turnID),
		ServerName:      codexAppsMCPServerName,
		Mode:            "form",
		Meta:            elicitation.Meta,
		Message:         elicitation.Message,
		RequestedSchema: elicitation.RequestedSchema,
		ElicitationID:   strings.TrimSpace(request.SuggestionID),
	}
	var response MCPElicitationRequestResponse
	if err := r.broker.Request(ctx, ServerRequestMCPElicitation, params, &response); err != nil {
		result.ResponseAction = "unavailable"
		return result, nil
	}
	result.Sent = true
	result.ResponseAction = string(response.Action)
	result.PersistDisable = pluginInstallResponseRequestsPersistentDisable(&response)
	toolType := pluginInstallRequestToolType(request)
	if result.PersistDisable && strings.TrimSpace(request.Tool.ID) != "" {
		_ = persistDisabledInstallSuggestion(r.config, toolType, request.Tool.ID)
	}
	if response.Action != MCPElicitationActionAccept {
		return result, nil
	}
	result.UserConfirmed = true
	if toolType != "plugin" {
		result.Completed = connectorInstallCompleted(r.apps, request.Tool.ID)
		return result, nil
	}
	if r.plugins == nil {
		return result, nil
	}
	installed, err := r.plugins.Install(&plugin.PluginInstallParams{PluginID: request.Tool.ID})
	if err != nil || installed == nil || strings.TrimSpace(installed.PluginID) == "" {
		return result, nil
	}
	result.Completed = true
	return result, nil
}

func connectorInstallCompleted(service *apps.AppService, connectorID string) bool {
	connectorID = strings.TrimSpace(connectorID)
	if service == nil || connectorID == "" {
		return false
	}
	list, err := service.List(&apps.AppListParams{ForceRefetch: true})
	if err != nil || list == nil {
		return false
	}
	for i := range list.Apps {
		app := list.Apps[i]
		if app.ID == connectorID && app.IsAccessible {
			return true
		}
	}
	return false
}

func pluginInstallResponseRequestsPersistentDisable(response *MCPElicitationRequestResponse) bool {
	if response == nil || response.Action != MCPElicitationActionDecline {
		return false
	}
	meta, ok := response.Meta.(map[string]any)
	if !ok {
		return false
	}
	value, _ := meta["persist"].(string)
	return strings.TrimSpace(value) == "always"
}

func persistDisabledInstallSuggestion(service *config.ConfigService, toolType string, toolID string) error {
	toolType = strings.TrimSpace(toolType)
	toolID = strings.TrimSpace(toolID)
	if service == nil || toolType == "" || toolID == "" {
		return nil
	}
	read, err := service.Read(&config.ConfigReadParams{})
	if err != nil {
		return err
	}
	disabled := disabledToolSuggestEntries(read.Config)
	disabled = appendDisabledToolSuggestEntry(disabled, toolType, toolID)
	_, err = service.BatchWrite(&config.ConfigBatchWriteParams{
		Edits: []config.ConfigEdit{{
			KeyPath: "tool_suggest.disabled_tools",
			Value:   disabled,
		}},
	})
	return err
}

func pluginInstallRequestToolType(request *tool.PluginInstallRequest) string {
	if request == nil {
		return "plugin"
	}
	switch strings.TrimSpace(request.ToolType) {
	case "connector":
		return "connector"
	default:
		return "plugin"
	}
}

func disabledToolSuggestEntries(values map[string]any) []any {
	if values == nil {
		return []any{}
	}
	toolSuggest, _ := values["tool_suggest"].(map[string]any)
	raw, _ := toolSuggest["disabled_tools"].([]any)
	out := make([]any, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := entry["type"].(string)
		id, _ := entry["id"].(string)
		kind = strings.TrimSpace(kind)
		id = strings.TrimSpace(id)
		if kind == "" || id == "" {
			continue
		}
		key := kind + "\x00" + id
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, map[string]any{"type": kind, "id": id})
	}
	return out
}

func appendDisabledToolSuggestEntry(entries []any, kind string, id string) []any {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return entries
	}
	key := kind + "\x00" + id
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		existingKind, _ := entry["type"].(string)
		existingID, _ := entry["id"].(string)
		if strings.TrimSpace(existingKind)+"\x00"+strings.TrimSpace(existingID) == key {
			return entries
		}
	}
	return append(entries, map[string]any{"type": kind, "id": id})
}

func stringPtrIfNotEmptyAppserver(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
