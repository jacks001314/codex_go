package appserver

import (
	"context"
	"strings"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/plugin"
	promptctx "codex_go/prompt"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

// maxAppUsedEmittedKeys bounds the app-use dedup keys, mirroring Rust's
// ANALYTICS_EVENT_DEDUPE_MAX_KEYS; the set is cleared when it is full.
const maxAppUsedEmittedKeys = 4096

// rememberExplicitAppMentions merges the connectors this turn's input named
// explicitly into the thread's connector selection and reports each mention
// (Rust's turn-input tracking: the selection decides whether a later app call is
// explicit or implicit, and `codex_app_mentioned` reports the mention itself).
func (r *RuntimeRouter) rememberExplicitAppMentions(ctx context.Context, threadID string, turnID string, connectionID string, params *turn.TurnStartParams, cfg *config.Config, modelSlug string) {
	if r == nil {
		return
	}
	mentioned := plugin.CollectExplicitAppIDs(pluginUserInputFromTurn(params))
	if len(mentioned) == 0 {
		return
	}
	connectorIDs := sortedBoolKeys(mentioned)
	if sessionState := r.sessionStateForThread(threadID); sessionState != nil {
		sessionState.MergeConnectorSelection(connectorIDs...)
	}
	if r.services.Analytics == nil || r.threadAnalyticsDisabled(threadID) {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.AppEventSink)
	if !ok {
		return
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	appNames := map[string]string{}
	for id, app := range r.appsForExplicitMentions(threadID, cfg) {
		appNames[id] = strings.TrimSpace(app.Name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, connectorID := range connectorIDs {
		event := telemetry.NewCodexAppMentionedEvent(telemetry.CodexAppMetadata{
			ConnectorID:     stringPtrIfNotEmpty(connectorID),
			ThreadID:        stringPtrIfNotEmpty(threadID),
			TurnID:          stringPtrIfNotEmpty(turnID),
			AppName:         stringPtrIfNotEmpty(appNames[connectorID]),
			ProductClientID: stringPtrIfNotEmpty(client.ProductClientID),
			InvokeType:      stringPtrIfNotEmpty(telemetry.InvocationTypeExplicit),
			ModelSlug:       stringPtrIfNotEmpty(strings.TrimSpace(modelSlug)),
		})
		sink.TrackCodexAppMentionedEvent(ctx, event)
	}
}

// emitCodexAppUsedEvent reports one host-owned apps call (Rust's
// maybe_track_codex_app_used): the connector and app name, whether the connector
// was explicitly selected for this thread, the invoking model, and the
// classification the call carried. Rust deduplicates at most one event per turn
// and connector - a call without a connector is always reported - and the first
// event for a key keeps its classification.
func (r *RuntimeRouter) emitCodexAppUsedEvent(ctx context.Context, threadID string, turnID string, connectionID string, connectorID string, connectorName string, modelSlug string, elicitationType *string) {
	if r == nil || r.services.Analytics == nil || r.threadAnalyticsDisabled(threadID) {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.AppEventSink)
	if !ok {
		return
	}
	connectorID = strings.TrimSpace(connectorID)
	if connectorID != "" && !r.claimAppUsedKey(turnID, connectorID) {
		return
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	invokeType := telemetry.InvocationTypeImplicit
	if connectorID != "" {
		if sessionState := r.sessionStateForThread(threadID); sessionState != nil {
			for _, selected := range sessionState.MergeConnectorSelection() {
				if selected == connectorID {
					invokeType = telemetry.InvocationTypeExplicit
					break
				}
			}
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sink.TrackCodexAppUsedEvent(ctx, telemetry.NewCodexAppUsedEvent(telemetry.CodexAppUsedEventParams{
		CodexAppMetadata: telemetry.CodexAppMetadata{
			ConnectorID:     stringPtrIfNotEmpty(connectorID),
			ThreadID:        stringPtrIfNotEmpty(threadID),
			TurnID:          stringPtrIfNotEmpty(turnID),
			AppName:         stringPtrIfNotEmpty(connectorName),
			ProductClientID: stringPtrIfNotEmpty(client.ProductClientID),
			InvokeType:      stringPtrIfNotEmpty(invokeType),
			ModelSlug:       stringPtrIfNotEmpty(strings.TrimSpace(modelSlug)),
		},
		ElicitationType: cloneStringPtrAppserver(elicitationType),
	}))
}

// claimAppUsedKey reports whether this call is the first for its turn and
// connector, mirroring Rust's `should_enqueue_app_used`.
func (r *RuntimeRouter) claimAppUsedKey(turnID string, connectorID string) bool {
	turnID = strings.TrimSpace(turnID)
	connectorID = strings.TrimSpace(connectorID)
	if turnID == "" || connectorID == "" {
		return true
	}
	r.appUsedMu.Lock()
	defer r.appUsedMu.Unlock()
	if r.appUsedEmittedKeys == nil {
		r.appUsedEmittedKeys = map[string]bool{}
	}
	if len(r.appUsedEmittedKeys) >= maxAppUsedEmittedKeys {
		r.appUsedEmittedKeys = map[string]bool{}
	}
	key := turnID + "\x00" + connectorID
	if r.appUsedEmittedKeys[key] {
		return false
	}
	r.appUsedEmittedKeys[key] = true
	return true
}

// sessionStateForThread returns the thread's session state, creating it on first
// use. The router owns one per thread so a connector selection survives across
// the thread's turns (Rust's SessionState).
func (r *RuntimeRouter) sessionStateForThread(threadID string) *state.SessionState {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.connectorSelectionMu.Lock()
	defer r.connectorSelectionMu.Unlock()
	if r.connectorSelections == nil {
		r.connectorSelections = map[string]*state.SessionState{}
	}
	sessionState := r.connectorSelections[threadID]
	if sessionState == nil {
		sessionState = state.NewSessionState(threadID)
		r.connectorSelections[threadID] = sessionState
	}
	return sessionState
}

func (r *RuntimeRouter) forgetThreadSessionState(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.connectorSelectionMu.Lock()
	delete(r.connectorSelections, threadID)
	r.connectorSelectionMu.Unlock()
}

// rememberSkillDerivedAppMentions mirrors Rust's
// `collect_explicit_app_ids_from_skill_items`: a connector named inside the
// selected skills' instructions joins the thread's connector selection and is
// reported as a mention, so a later call for it counts as explicit.
func (r *RuntimeRouter) rememberSkillDerivedAppMentions(ctx context.Context, threadID string, turnID string, modelID string, cfg *config.Config, params *turn.TurnStartParams, skillItems []any, skills []promptctx.InstructionsSkillMetadata) {
	if r == nil || len(skillItems) == 0 || len(skills) == 0 {
		return
	}
	messages := make([]string, 0, len(skillItems))
	for _, item := range skillItems {
		if text := renderedItemInputText(item); text != "" {
			messages = append(messages, text)
		}
	}
	if len(messages) == 0 {
		return
	}
	appsByID := map[string]apps.AppEntry{}
	connectors := func() []plugin.SkillAppConnector {
		if len(appsByID) > 0 {
			out := make([]plugin.SkillAppConnector, 0, len(appsByID))
			for id, app := range appsByID {
				out = append(out, plugin.SkillAppConnector{ID: id, Name: strings.TrimSpace(app.Name)})
			}
			return out
		}
		// The catalog is fetched only when the skill text actually names
		// something a connector could answer to.
		appsByID = r.appsForExplicitMentions(threadID, cfg)
		out := make([]plugin.SkillAppConnector, 0, len(appsByID))
		for id, app := range appsByID {
			out = append(out, plugin.SkillAppConnector{ID: id, Name: strings.TrimSpace(app.Name)})
		}
		return out
	}
	skillNames := make([]string, 0, len(skills))
	for _, skill := range skills {
		skillNames = append(skillNames, skill.Name)
	}
	connectorIDs := plugin.CollectExplicitAppIDsFromSkillItems(messages, connectors, skillNames)
	if len(connectorIDs) == 0 {
		return
	}
	if sessionState := r.sessionStateForThread(threadID); sessionState != nil {
		sessionState.MergeConnectorSelection(sortedBoolKeys(connectorIDs)...)
	}
	if turnID == "" || r.services.Analytics == nil || r.threadAnalyticsDisabled(threadID) {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.AppEventSink)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	productClientID := ""
	if params != nil {
		productClientID = strings.TrimSpace(params.Originator)
	}
	for _, connectorID := range sortedBoolKeys(connectorIDs) {
		sink.TrackCodexAppMentionedEvent(ctx, telemetry.NewCodexAppMentionedEvent(telemetry.CodexAppMetadata{
			ConnectorID:     stringPtrIfNotEmpty(connectorID),
			ThreadID:        stringPtrIfNotEmpty(threadID),
			TurnID:          stringPtrIfNotEmpty(turnID),
			AppName:         stringPtrIfNotEmpty(strings.TrimSpace(appsByID[connectorID].Name)),
			ProductClientID: stringPtrIfNotEmpty(productClientID),
			InvokeType:      stringPtrIfNotEmpty(telemetry.InvocationTypeExplicit),
			ModelSlug:       stringPtrIfNotEmpty(strings.TrimSpace(modelID)),
		}))
	}
}

// renderedItemInputText returns the input text of a rendered fragment item, which
// is the shape the skill instructions are injected as.
func renderedItemInputText(item any) string {
	value, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	content, ok := value["content"].([]map[string]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, part := range content {
		if text, _ := part["text"].(string); strings.TrimSpace(text) != "" {
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(text)
		}
	}
	return builder.String()
}
