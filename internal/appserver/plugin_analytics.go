package appserver

import (
	"context"
	"errors"
	"strings"

	"codex_go/internal/plugin"
	"codex_go/internal/telemetry"
)

func (r *RuntimeRouter) emitPluginStateAnalyticsEvent(ctx context.Context, connectionID string, eventType string, detail *plugin.PluginDetail) {
	if r == nil || r.services.Analytics == nil || detail == nil || strings.TrimSpace(eventType) == "" {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.PluginEventSink)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event := telemetry.NewCodexPluginEvent(
		eventType,
		pluginAnalyticsMetadataFromDetail(detail, r.pluginAnalyticsProductClientID(connectionID)),
	)
	switch event.EventType {
	case telemetry.CodexPluginInstalledEventType:
		sink.TrackCodexPluginInstalledEvent(ctx, event)
	case telemetry.CodexPluginUninstalledEventType:
		sink.TrackCodexPluginUninstalledEvent(ctx, event)
	case telemetry.CodexPluginEnabledEventType:
		sink.TrackCodexPluginEnabledEvent(ctx, event)
	case telemetry.CodexPluginDisabledEventType:
		sink.TrackCodexPluginDisabledEvent(ctx, event)
	}
}

func (r *RuntimeRouter) emitPluginInstallFailedAnalyticsEvent(ctx context.Context, connectionID string, params *plugin.PluginInstallParams, installErr error) {
	if r == nil || r.services.Analytics == nil || installErr == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.PluginEventSink)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event := telemetry.NewCodexPluginInstallFailedEvent(
		pluginAnalyticsMetadataFromInstallParams(params, r.pluginAnalyticsProductClientID(connectionID)),
		pluginAnalyticsInstallErrorType(installErr),
	)
	sink.TrackCodexPluginInstallFailedEvent(ctx, event)
}

func (r *RuntimeRouter) pluginAnalyticsProductClientID(connectionID string) string {
	if r == nil {
		return defaultInitializeOriginator
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok || strings.TrimSpace(client.ProductClientID) == "" {
		return defaultInitializeOriginator
	}
	return strings.TrimSpace(client.ProductClientID)
}

func pluginAnalyticsMetadataFromDetail(detail *plugin.PluginDetail, productClientID string) telemetry.CodexPluginMetadata {
	if detail == nil {
		return pluginAnalyticsMetadataWithProductClientID(productClientID)
	}
	summary := detail.Summary
	pluginID := strings.TrimSpace(summary.ID)
	pluginName := strings.TrimSpace(summary.Name)
	marketplaceName := strings.TrimSpace(firstNonEmpty(summary.MarketplaceName, detail.MarketplaceName))
	if parsedName, parsedMarketplace := pluginAnalyticsSplitID(pluginID); pluginName == "" || marketplaceName == "" {
		if pluginName == "" {
			pluginName = parsedName
		}
		if marketplaceName == "" {
			marketplaceName = parsedMarketplace
		}
	}
	if pluginID == "" {
		pluginID = pluginAnalyticsID(pluginName, marketplaceName)
	}
	hasSkills := summary.HasSkills || len(detail.Skills) > 0
	mcpServerCount := len(detail.MCPServers)
	if mcpServerCount == 0 && len(summary.MCPServers) > 0 {
		mcpServerCount = len(summary.MCPServers)
	}
	metadata := pluginAnalyticsMetadataWithProductClientID(productClientID)
	metadata.PluginID = stringPtrIfNotEmpty(strings.TrimSpace(pluginID))
	metadata.RemotePluginID = stringPtrIfNotEmpty(strings.TrimSpace(summary.RemotePluginID))
	metadata.PluginName = stringPtrIfNotEmpty(pluginName)
	metadata.MarketplaceName = stringPtrIfNotEmpty(marketplaceName)
	metadata.HasSkills = &hasSkills
	metadata.MCPServerCount = &mcpServerCount
	metadata.ConnectorIDs = pluginAnalyticsConnectorIDs(detail)
	return metadata
}

func pluginAnalyticsMetadataFromInstallParams(params *plugin.PluginInstallParams, productClientID string) telemetry.CodexPluginMetadata {
	metadata := pluginAnalyticsMetadataWithProductClientID(productClientID)
	if params == nil {
		return metadata
	}
	pluginID := strings.TrimSpace(params.PluginID)
	pluginName := strings.TrimSpace(params.PluginName)
	marketplaceName := strings.TrimSpace(firstNonEmpty(params.RemoteMarketplaceName, params.MarketplaceName))
	if pluginID == "" {
		pluginID = pluginAnalyticsID(pluginName, marketplaceName)
	}
	if parsedName, parsedMarketplace := pluginAnalyticsSplitID(pluginID); pluginName == "" || marketplaceName == "" {
		if pluginName == "" {
			pluginName = parsedName
		}
		if marketplaceName == "" {
			marketplaceName = parsedMarketplace
		}
	}
	metadata.PluginID = stringPtrIfNotEmpty(pluginID)
	metadata.PluginName = stringPtrIfNotEmpty(pluginName)
	metadata.MarketplaceName = stringPtrIfNotEmpty(marketplaceName)
	return metadata
}

func pluginAnalyticsMetadataWithProductClientID(productClientID string) telemetry.CodexPluginMetadata {
	return telemetry.CodexPluginMetadata{
		ProductClientID: stringPtrIfNotEmpty(strings.TrimSpace(productClientID)),
	}
}

func pluginAnalyticsInstallErrorType(err error) string {
	if errors.Is(err, plugin.ErrInvalidPluginRequest) {
		return "store_invalid"
	}
	return "store_io"
}

func pluginAnalyticsConnectorIDs(detail *plugin.PluginDetail) []string {
	if detail == nil {
		return nil
	}
	ids := make([]string, 0, len(detail.Summary.AppConnectors)+len(detail.Apps)+len(detail.AppTemplates))
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		ids = append(ids, value)
	}
	for _, connectorID := range detail.Summary.AppConnectors {
		add(connectorID)
	}
	for _, app := range detail.Apps {
		add(app.ID)
	}
	for _, template := range detail.AppTemplates {
		if template.CanonicalConnectorID != nil {
			add(*template.CanonicalConnectorID)
		}
		for _, appID := range template.MaterializedAppIDs {
			add(appID)
		}
	}
	return ids
}

func pluginAnalyticsDetailByID(service *plugin.PluginService, pluginID string) *plugin.PluginDetail {
	if service == nil {
		return nil
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return nil
	}
	pluginName, marketplaceName := pluginAnalyticsSplitID(pluginID)
	readParams := &plugin.PluginReadParams{
		PluginName:      pluginName,
		MarketplaceName: marketplaceName,
		RemotePluginID:  pluginID,
	}
	if pluginName == "" {
		readParams.PluginName = pluginID
	}
	response, err := service.Read(readParams)
	if err != nil || response == nil {
		return nil
	}
	detail := response.Plugin
	return &detail
}

func pluginAnalyticsID(name string, marketplace string) string {
	name = strings.TrimSpace(name)
	marketplace = strings.TrimSpace(marketplace)
	if name == "" {
		return ""
	}
	if marketplace == "" {
		return name
	}
	return name + "@" + marketplace
}

func pluginAnalyticsSplitID(id string) (string, string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ""
	}
	index := strings.LastIndex(id, "@")
	if index < 0 {
		return id, ""
	}
	return strings.TrimSpace(id[:index]), strings.TrimSpace(id[index+1:])
}
