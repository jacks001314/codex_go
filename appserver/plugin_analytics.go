package appserver

import (
	"context"
	"errors"
	"strings"

	"codex_go/config"
	"codex_go/plugin"
	"codex_go/telemetry"
	"codex_go/turn"
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
		pluginAnalyticsMetadataFromDetail(detail, r.analyticsProductClientID(connectionID)),
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
		pluginAnalyticsMetadataFromInstallParams(params, r.analyticsProductClientID(connectionID)),
		pluginAnalyticsInstallErrorType(installErr),
	)
	sink.TrackCodexPluginInstallFailedEvent(ctx, event)
}

// rememberExplicitPluginMentions reports each plugin the turn's input named
// explicitly. Rust core/src/session/turn.rs walks the mentioned plugin
// summaries, resolves telemetry metadata for each and calls track_plugin_used;
// the analytics client drops repeats of the same plugin within a turn.
func (r *RuntimeRouter) rememberExplicitPluginMentions(ctx context.Context, threadID string, turnID string, connectionID string, params *turn.TurnStartParams, cfg *config.Config, modelSlug string) {
	if r == nil || r.services.Plugins == nil || r.services.Analytics == nil || r.threadAnalyticsDisabled(threadID) {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.PluginUsedEventSink)
	if !ok {
		return
	}
	mentioned := plugin.CollectExplicitPluginMentions(pluginUserInputFromTurn(params), r.pluginCapabilitiesForThread(threadID))
	if len(mentioned) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	productClientID := r.analyticsProductClientID(connectionID)
	turnTrigger := r.analyticsTurnTriggerForTurn(threadID, turnID)
	for _, capability := range mentioned {
		metadata, pluginKey, reportable := pluginUsedAnalyticsMetadata(capability, productClientID, turnTrigger, threadID, turnID, modelSlug)
		if !reportable || !r.claimPluginUsedKey(turnID, pluginKey) {
			continue
		}
		sink.TrackCodexPluginUsedEvent(ctx, telemetry.NewCodexPluginUsedEvent(metadata))
	}
}

// pluginUsedAnalyticsMetadata builds Rust's codex_plugin_used_metadata from a
// capability summary. The plugin identity is parsed from the capability's
// config name, exactly like telemetry_metadata_for_capability_summary; a
// config name that is not a `<plugin>@<marketplace>` key makes the plugin
// unreportable.
func pluginUsedAnalyticsMetadata(capability plugin.CapabilitySummary, productClientID string, turnTrigger *string, threadID string, turnID string, modelSlug string) (telemetry.CodexPluginUsedMetadata, string, bool) {
	pluginID, err := plugin.ParsePluginId(capability.ConfigName)
	if err != nil {
		return telemetry.CodexPluginUsedMetadata{}, "", false
	}
	pluginKey := pluginID.Key()
	hasSkills := capability.HasSkills
	mcpServerCount := len(capability.MCPServers)
	metadata := telemetry.CodexPluginUsedMetadata{
		TurnTrigger: turnTrigger,
		CodexPluginMetadata: telemetry.CodexPluginMetadata{
			PluginID:        stringPtrIfNotEmpty(pluginKey),
			RemotePluginID:  stringPtrIfNotEmpty(strings.TrimSpace(capability.RemotePluginID)),
			PluginName:      stringPtrIfNotEmpty(pluginID.PluginName),
			MarketplaceName: stringPtrIfNotEmpty(pluginID.MarketplaceName),
			HasSkills:       &hasSkills,
			MCPServerCount:  &mcpServerCount,
			ConnectorIDs:    append([]string{}, capability.AppConnectors...),
			ProductClientID: stringPtrIfNotEmpty(strings.TrimSpace(productClientID)),
		},
		MCPServerNames: append([]string{}, capability.MCPServers...),
		ThreadID:       stringPtrIfNotEmpty(threadID),
		TurnID:         stringPtrIfNotEmpty(turnID),
		ModelSlug:      stringPtrIfNotEmpty(strings.TrimSpace(modelSlug)),
	}
	return metadata, pluginKey, true
}

// claimPluginUsedKey reports whether this is the first use of the plugin in the
// turn, mirroring Rust's should_enqueue_plugin_used. The dedupe set shares the
// analytics key cap and is cleared when it is full.
func (r *RuntimeRouter) claimPluginUsedKey(turnID string, pluginKey string) bool {
	turnID = strings.TrimSpace(turnID)
	pluginKey = strings.TrimSpace(pluginKey)
	if turnID == "" || pluginKey == "" {
		return true
	}
	r.pluginUsedMu.Lock()
	defer r.pluginUsedMu.Unlock()
	if r.pluginUsedEmittedKeys == nil {
		r.pluginUsedEmittedKeys = map[string]bool{}
	}
	if len(r.pluginUsedEmittedKeys) >= maxAppUsedEmittedKeys {
		r.pluginUsedEmittedKeys = map[string]bool{}
	}
	key := turnID + "\x00" + pluginKey
	if r.pluginUsedEmittedKeys[key] {
		return false
	}
	r.pluginUsedEmittedKeys[key] = true
	return true
}

func (r *RuntimeRouter) analyticsProductClientID(connectionID string) string {
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
