package app

import (
	"context"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/model"
	codextui "codex_go/tui"
)

// interactiveRemoteListModels mirrors Rust #63553's provider-scoped model
// catalog for a connected app server. A server-owned (Cloud) conversation reads
// the server's default catalog; a non-Cloud server that cannot scope model/list
// to the requested provider falls back to its default list.
func interactiveRemoteListModels(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func(cwd string, provider string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
	return func(_ string, provider string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		if threadParamsModeForEndpoint(endpoint).SettingsOwner() == ThreadSettingsOwnerServer {
			provider = ""
		}
		params := model.ModelListParams{ModelProvider: strings.TrimSpace(provider)}
		presets, err := remoteModelList(ctx, client, params)
		if err != nil && params.ModelProvider != "" && remoteModelListScopeUnsupported(err) {
			params.ModelProvider = ""
			presets, err = remoteModelList(ctx, client, params)
		}
		if err != nil {
			return nil, err
		}
		return modelPickerOptionsFromRemoteModels(presets, includeHidden), nil
	}
}

// modelPickerOptionsFromRemoteModels converts the app server's model/list
// summaries into picker options, keeping the catalog's reasoning, service-tier
// and access-program metadata.
func modelPickerOptionsFromRemoteModels(summaries []model.ModelSummary, includeHidden bool) []codextui.ModelPickerOption {
	options := make([]codextui.ModelPickerOption, 0, len(summaries))
	for _, summary := range summaries {
		id := strings.TrimSpace(summary.Model)
		if id == "" {
			id = strings.TrimSpace(summary.ID)
		}
		if id == "" || (!includeHidden && summary.Hidden) {
			continue
		}
		label := strings.TrimSpace(summary.DisplayName)
		if label == "" {
			label = id
		}
		option := codextui.ModelPickerOption{
			ID:                     id,
			Label:                  label,
			Description:            strings.TrimSpace(summary.Description),
			IsDefault:              summary.IsDefault,
			ShowInPicker:           !summary.Hidden,
			DefaultReasoningEffort: strings.TrimSpace(summary.DefaultReasoningEffort),
			ServiceTierDefinitions: append([]model.ModelServiceTier(nil), summary.ServiceTiers...),
		}
		for _, effort := range summary.SupportedReasoningEfforts {
			option.SupportedReasoningEfforts = append(option.SupportedReasoningEfforts, codextui.ReasoningEffortOption{
				Effort:      strings.TrimSpace(effort.ReasoningEffort),
				Description: strings.TrimSpace(effort.Description),
				IsDefault:   strings.TrimSpace(effort.ReasoningEffort) == option.DefaultReasoningEffort,
			})
		}
		seenTiers := map[string]bool{}
		for _, tier := range summary.ServiceTiers {
			if id := strings.TrimSpace(tier.ID); id != "" && !seenTiers[id] {
				seenTiers[id] = true
				option.ServiceTiers = append(option.ServiceTiers, id)
			}
		}
		for _, tier := range summary.AdditionalSpeedTiers {
			if id := strings.TrimSpace(tier); id != "" && !seenTiers[id] {
				seenTiers[id] = true
				option.ServiceTiers = append(option.ServiceTiers, id)
			}
		}
		if summary.DefaultServiceTier != nil {
			option.DefaultServiceTier = strings.TrimSpace(*summary.DefaultServiceTier)
		}
		if summary.AvailableAccessPrograms != nil {
			option.AvailableCyberPrograms = append([]string(nil), summary.AvailableAccessPrograms.Cyber...)
		}
		options = append(options, option)
	}
	return options
}

// remoteModelList reads one model/list page and accepts both the data and
// models response shapes an app server may send.
func remoteModelList(ctx context.Context, client *remoteAppServerTUIClient, params model.ModelListParams) ([]model.ModelSummary, error) {
	var listed model.ModelListResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodModelList, params, &listed); err != nil {
		return nil, err
	}
	presets := listed.Data
	if len(presets) == 0 {
		presets = listed.Models
	}
	return presets, nil
}

// remoteModelListScopeUnsupported reports the invalid-params or missing-method
// errors an older server returns when it cannot scope model/list by provider.
func remoteModelListScopeUnsupported(err error) bool {
	var rpcErr *remoteRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	return rpcErr.Code == appserver.JSONRPCInvalidParamsErrorCode || rpcErr.Code == appserver.JSONRPCMethodNotFoundErrorCode
}
