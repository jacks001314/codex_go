package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/features"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/retainedctx"
	"codex_go/session"
	"codex_go/turn"
)

type modelSearchProvider string

const (
	modelSearchHosted    modelSearchProvider = "hosted"
	modelSearchClientMCP modelSearchProvider = "client_mcp"
	modelSearchNative    modelSearchProvider = "native"
	modelSearchDisabled  modelSearchProvider = "disabled"
	// unmanaged is the absence of ModelSearch extension state, which preserves
	// the legacy configured behavior and leaves both declarations untouched.
	modelSearchUnmanaged modelSearchProvider = "unmanaged"
)

type modelSearchSelection struct {
	provider modelSearchProvider
	mode     codexapi.WebSearchMode
	fallback bool
	binding  *mcp.Binding
	context  map[string]any
	reason   string
}

type modelSearchResolveInput struct {
	cfg               *config.Config
	modelInfo         *model.ModelInfo
	tools             []mcp.RuntimeToolInfo
	turnMetadata      map[string]any
	nativeAvailable   bool
	owner             modelSearchProvider
	usedMCP           bool
	attributionBlocks bool
	ephemeral         bool
}

type modelSearchDiscoveryFunc func(context.Context, map[string]any) mcp.SearchDiscovery

type modelSearchDiscoveryCacheEntry struct {
	generation uint64
	request    string
	discovery  mcp.SearchDiscovery
}

type modelSearchReservation struct {
	threadID string
}

const (
	modelSearchUsedMCP uint8 = 1 << iota
	modelSearchUsedNative
)

// Rust emits these counters from core's ModelSearch resolution
// (codex-rs/core/src/model_search.rs); Go records them from the router that
// owns the per-thread and per-turn search state.
const (
	modelSearchTurnMetric      = "codex.model_search.turn"
	modelSearchDiscoveryMetric = "codex.model_search.discovery"
)

// resolveModelSearchSelection chooses one provider for a sampling step. A
// critical discovery failure may offer native search, but only when policy,
// thread ownership and MCP attribution all leave that provider available.
func resolveModelSearchSelection(ctx context.Context, input modelSearchResolveInput, discover modelSearchDiscoveryFunc) modelSearchSelection {
	if ctx == nil {
		ctx = context.Background()
	}
	provider := modelSearchProvider("")
	if input.cfg != nil && input.cfg.RequiresNativeSearch() && features.Enabled(input.cfg.FeatureSettings(), "standalone_web_search") {
		provider = modelSearchNative
	} else if input.modelInfo != nil && input.modelInfo.WebSearchProvider != nil {
		provider = parseModelSearchProvider(*input.modelInfo.WebSearchProvider)
	} else if input.cfg != nil {
		if configured, ok := input.cfg.ClientSearchProvider(); ok {
			provider = parseModelSearchProvider(configured)
			if provider == modelSearchHosted {
				provider = modelSearchClientMCP
			}
		}
	}
	request, validRequest := modelSearchRequestMetadata(input.turnMetadata)
	nativeMode, nativeOK := nativeModelSearchMode(input.cfg, request, input.ephemeral)
	if provider == "" {
		mode := codexapi.WebSearchModeDisabled
		if nativeOK && input.nativeAvailable {
			mode = nativeMode
		}
		return modelSearchSelection{provider: modelSearchUnmanaged, mode: mode, reason: "legacy"}
	}
	switch provider {
	case modelSearchNative:
		if nativeOK && input.nativeAvailable {
			return modelSearchSelection{provider: modelSearchNative, mode: nativeMode, reason: "native_selected"}
		}
		return modelSearchSelection{provider: modelSearchDisabled, reason: "search_policy"}
	case modelSearchClientMCP:
		return modelSearchSelection{provider: modelSearchClientMCP, reason: "client_selection"}
	case modelSearchDisabled:
		return modelSearchSelection{provider: modelSearchDisabled, reason: "disabled"}
	case modelSearchHosted:
	default:
		return modelSearchSelection{provider: modelSearchUnmanaged, reason: "invalid_provider"}
	}

	if input.cfg == nil || !features.Enabled(input.cfg.FeatureSettings(), "apps") {
		return modelSearchSelection{provider: modelSearchDisabled, reason: "apps_disabled"}
	}
	connectorAvailable := false
	for i := range input.tools {
		if mcp.IsModelSearchTool(&input.tools[i]) {
			connectorAvailable = true
			break
		}
	}
	if !connectorAvailable {
		return modelSearchSelection{provider: modelSearchDisabled, reason: "connector_unavailable"}
	}
	if !validRequest {
		return modelSearchSelection{provider: modelSearchDisabled, reason: "invalid_request_metadata"}
	}
	searchContext, ok := hostedModelSearchContext(input.cfg, request, input.ephemeral)
	if !ok {
		return modelSearchSelection{provider: modelSearchDisabled, reason: "search_policy"}
	}
	if input.modelInfo != nil {
		searchContext["model_supported_tools"] = append([]string{}, input.modelInfo.ExperimentalSupportedTools...)
	} else {
		searchContext["model_supported_tools"] = []string{}
	}
	discoveryRequest := cloneSearchObject(request)
	if unrestrictedSearchConnectors(request) {
		discoveryRequest["selected_connector_ids"] = []string{mcp.SearchConnector}
	}
	discoveryRequest["openai/search_context"] = cloneSearchObject(searchContext)
	metadata := cloneSearchObject(input.turnMetadata)
	metadata["mcp_request_meta"] = cloneSearchObject(discoveryRequest)
	discovery := mcp.SearchDiscovery{Kind: mcp.SearchDiscoveryUnavailable}
	if discover != nil {
		discovery = discover(ctx, metadata)
	}
	fallbackAllowed := input.nativeAvailable && nativeOK &&
		input.owner != modelSearchClientMCP && input.owner != modelSearchHosted &&
		!input.usedMCP && !input.attributionBlocks
	if discovery.Kind == mcp.SearchDiscoveryReady && discovery.Binding != nil {
		if input.owner != modelSearchNative {
			return modelSearchSelection{
				provider: modelSearchHosted,
				binding:  discovery.Binding,
				context:  searchContext,
				reason:   "hosted",
			}
		}
		if fallbackAllowed {
			return modelSearchSelection{provider: modelSearchNative, mode: nativeMode, fallback: true, reason: "native_browsing_state"}
		}
		return modelSearchSelection{provider: modelSearchDisabled, reason: "fallback_restricted"}
	}
	if discovery.Kind == mcp.SearchDiscoveryCritical && fallbackAllowed {
		reason := strings.TrimSpace(discovery.Reason)
		if reason == "" {
			reason = "discovery_error"
		}
		return modelSearchSelection{provider: modelSearchNative, mode: nativeMode, fallback: true, reason: reason}
	}
	if discovery.Kind == mcp.SearchDiscoveryCritical {
		return modelSearchSelection{provider: modelSearchDisabled, reason: "fallback_restricted"}
	}
	return modelSearchSelection{provider: modelSearchDisabled, reason: "unavailable"}
}

func modelSearchMayUseApps(info *model.ModelInfo, cfg *config.Config) bool {
	if cfg == nil || !features.Enabled(cfg.FeatureSettings(), "apps") || cfg.RequiresNativeSearch() {
		return false
	}
	provider := modelSearchProvider("")
	if info != nil && info.WebSearchProvider != nil {
		provider = parseModelSearchProvider(*info.WebSearchProvider)
	} else if configured, ok := cfg.ClientSearchProvider(); ok {
		provider = parseModelSearchProvider(configured)
	}
	if provider != modelSearchHosted {
		return false
	}
	mode := webSearchModeFromConfig(cfg)
	return mode != codexapi.WebSearchModeDisabled || features.Enabled(cfg.FeatureSettings(), "hosted_search_policy")
}

func parseModelSearchProvider(value string) modelSearchProvider {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "search_mcp":
		return modelSearchHosted
	case "standalone":
		return modelSearchNative
	default:
		return ""
	}
}

func modelSearchRequestMetadata(document map[string]any) (map[string]any, bool) {
	if document == nil {
		return map[string]any{}, true
	}
	raw, present := document["mcp_request_meta"]
	if !present || raw == nil {
		return map[string]any{}, true
	}
	switch value := raw.(type) {
	case map[string]any:
		return cloneSearchObject(value), true
	case string:
		var decoded map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(value)), &decoded) != nil || decoded == nil {
			return nil, false
		}
		return decoded, true
	default:
		return nil, false
	}
}

func searchMetadataObject(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case nil:
		return map[string]any{}, true
	case map[string]any:
		return cloneSearchObject(typed), true
	case string:
		var decoded map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(typed)), &decoded) != nil || decoded == nil {
			return nil, false
		}
		return decoded, true
	default:
		return nil, false
	}
}

func unrestrictedSearchConnectors(request map[string]any) bool {
	for key := range request {
		if key != "openai/search_context" && key != "openai/codex_installation_id" {
			return false
		}
	}
	return true
}

func nativeModelSearchMode(cfg *config.Config, request map[string]any, ephemeral bool) (codexapi.WebSearchMode, bool) {
	if !unrestrictedSearchConnectors(request) {
		return codexapi.WebSearchModeDisabled, false
	}
	context, ok := searchMetadataObject(request["openai/search_context"])
	if !ok {
		return codexapi.WebSearchModeDisabled, false
	}
	configured := webSearchModeFromConfig(cfg)
	requested, ok := requestedModelSearchMode(context, configured)
	if !ok {
		return codexapi.WebSearchModeDisabled, false
	}
	mode := configured.RestrictTo(requested)
	for key, value := range context {
		switch key {
		case "mode", "telemetry_attributes", "model_supported_tools":
		case "is_web_access_restricted":
			restricted, ok := value.(bool)
			if !ok {
				return codexapi.WebSearchModeDisabled, false
			}
			if restricted {
				mode = mode.RestrictTo(codexapi.WebSearchModeCached)
			}
		case "session_persistent":
			persistent, ok := value.(bool)
			if !ok || (!persistent && !ephemeral) {
				return codexapi.WebSearchModeDisabled, false
			}
		default:
			return codexapi.WebSearchModeDisabled, false
		}
	}
	if mode == codexapi.WebSearchModeDisabled || !modelSearchModeAllowed(cfg, mode) {
		return codexapi.WebSearchModeDisabled, false
	}
	return mode, true
}

func hostedModelSearchContext(cfg *config.Config, request map[string]any, ephemeral bool) (map[string]any, bool) {
	if cfg == nil || !features.Enabled(cfg.FeatureSettings(), "apps") {
		return nil, false
	}
	toolConfig := webSearchToolConfig(cfg)
	if _, ok := toolConfig["location"]; ok {
		return nil, false
	}
	if _, ok := toolConfig["search_context_size"]; ok {
		return nil, false
	}
	context, ok := searchMetadataObject(request["openai/search_context"])
	if !ok {
		return nil, false
	}
	nativeMode := webSearchModeFromConfig(cfg)
	hostMode, ok := requestedModelSearchMode(context, nativeMode)
	if !ok {
		return nil, false
	}
	mode := nativeMode.RestrictTo(hostMode)
	if nativeMode == codexapi.WebSearchModeDisabled && features.Enabled(cfg.FeatureSettings(), "hosted_search_policy") {
		mode = hostMode
	}
	if mode == codexapi.WebSearchModeDisabled || !modelSearchModeAllowed(cfg, mode) {
		return nil, false
	}
	context["mode"] = string(mode)
	restricted := false
	if value, present := context["is_web_access_restricted"]; present {
		var valid bool
		restricted, valid = value.(bool)
		if !valid {
			return nil, false
		}
	}
	context["is_web_access_restricted"] = restricted || mode == codexapi.WebSearchModeCached
	persistent := true
	if value, present := context["session_persistent"]; present {
		var valid bool
		persistent, valid = value.(bool)
		if !valid {
			return nil, false
		}
	}
	context["session_persistent"] = persistent && !ephemeral
	domains := stringSliceFromMapAny(toolConfig, "allowed_domains", "allowedDomains")
	if len(domains) > 0 {
		if requested, present := context["allowed_domains"]; present && !searchDomainsEqual(requested, domains) {
			return nil, false
		}
		context["allowed_domains"] = append([]string{}, domains...)
	}
	return context, true
}

func requestedModelSearchMode(context map[string]any, fallback codexapi.WebSearchMode) (codexapi.WebSearchMode, bool) {
	raw, present := context["mode"]
	if !present {
		return fallback, true
	}
	value, ok := raw.(string)
	if !ok {
		return codexapi.WebSearchModeDisabled, false
	}
	switch codexapi.WebSearchMode(strings.ToLower(strings.TrimSpace(value))) {
	case codexapi.WebSearchModeDisabled:
		return codexapi.WebSearchModeDisabled, true
	case codexapi.WebSearchModeCached:
		return codexapi.WebSearchModeCached, true
	case codexapi.WebSearchModeIndexed:
		return codexapi.WebSearchModeIndexed, true
	case codexapi.WebSearchModeLive:
		return codexapi.WebSearchModeLive, true
	default:
		return codexapi.WebSearchModeDisabled, false
	}
}

func modelSearchModeAllowed(cfg *config.Config, mode codexapi.WebSearchMode) bool {
	if cfg == nil || cfg.Requirements == nil || cfg.Requirements.AllowedWebSearchModes == nil {
		return true
	}
	for _, allowed := range cfg.Requirements.AllowedWebSearchModes {
		if codexapi.WebSearchMode(allowed) == mode {
			return true
		}
	}
	return false
}

func searchDomainsEqual(value any, want []string) bool {
	got := make([]string, 0)
	switch typed := value.(type) {
	case []string:
		got = append(got, typed...)
	case []any:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return false
			}
			got = append(got, text)
		}
	default:
		return false
	}
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if strings.TrimSpace(got[i]) != strings.TrimSpace(want[i]) {
			return false
		}
	}
	return true
}

func cloneSearchObject(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var decoded map[string]any
	if json.Unmarshal(encoded, &decoded) != nil || decoded == nil {
		return map[string]any{}
	}
	return decoded
}

func applyModelSearchTools(base []mcp.RuntimeToolInfo, selection modelSearchSelection) []mcp.RuntimeToolInfo {
	out := make([]mcp.RuntimeToolInfo, 0, len(base)+1)
	for i := range base {
		if !mcp.IsModelSearchTool(&base[i]) {
			out = append(out, base[i])
		}
	}
	switch selection.provider {
	case modelSearchClientMCP, modelSearchUnmanaged:
		return append([]mcp.RuntimeToolInfo(nil), base...)
	case modelSearchHosted:
		if selection.binding != nil {
			if tool, ok := selection.binding.SearchTool(); ok {
				out = append(out, tool)
			}
		}
	}
	return out
}

func searchCallTurnMetadata(base func() map[string]any, contextValue map[string]any) func() map[string]any {
	return func() map[string]any {
		var document map[string]any
		if base != nil {
			document = cloneSearchObject(base())
		} else {
			document = map[string]any{}
		}
		request, ok := modelSearchRequestMetadata(document)
		if !ok {
			request = map[string]any{}
		}
		request["openai/search_context"] = cloneSearchObject(contextValue)
		document["mcp_request_meta"] = request
		return document
	}
}

func (r *RuntimeRouter) resolveModelSearchForTurn(ctx context.Context, threadID string, turnID string, cfg *config.Config, info *model.ModelInfo, tools []mcp.RuntimeToolInfo, binding *mcp.Binding, nativeOptions *turn.WebSearchOptions, turnMetadata map[string]any) modelSearchSelection {
	input := modelSearchResolveInput{
		cfg:             cfg,
		modelInfo:       info,
		tools:           tools,
		turnMetadata:    turnMetadata,
		nativeAvailable: nativeOptions != nil,
		owner:           r.threadModelSearchProvider(threadID),
		usedMCP:         r.modelSearchTurnUses(threadID, turnID, modelSearchUsedMCP),
	}
	if record, err := r.threadRecord(session.ThreadID(threadID), true, false); err == nil && record != nil {
		input.ephemeral = runtimeRecordEphemeral(record)
	}
	if recorder := r.executedToolCallRecorder(threadID); recorder != nil {
		attribution := recorder.McpAttributionSnapshot()
		input.attributionBlocks = attribution.Status == retainedctx.McpAttributionStatusAttributionError
		for _, source := range attribution.Sources {
			if source.ConnectorID != nil && strings.TrimSpace(*source.ConnectorID) == mcp.SearchConnector {
				input.attributionBlocks = true
				break
			}
		}
	}
	discover := func(ctx context.Context, metadata map[string]any) mcp.SearchDiscovery {
		if binding == nil {
			return mcp.SearchDiscovery{Kind: mcp.SearchDiscoveryUnavailable}
		}
		return r.discoverModelSearchForStep(ctx, threadID, turnID, binding, metadata)
	}
	selection := resolveModelSearchSelection(ctx, input, discover)
	r.recordModelSearchCounter(modelSearchDiscoveryMetric, map[string]string{"reason": selection.reason})
	return selection
}

func (r *RuntimeRouter) discoverModelSearchForStep(ctx context.Context, threadID string, turnID string, binding *mcp.Binding, metadata map[string]any) mcp.SearchDiscovery {
	key := strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
	requestBytes, _ := json.Marshal(metadata)
	request := string(requestBytes)
	r.modelSearchMu.Lock()
	if entry, ok := r.modelSearchDiscoveryCache[key]; ok && entry.generation == binding.Generation() && entry.request == request {
		discovery := entry.discovery
		r.modelSearchMu.Unlock()
		return discovery
	}
	r.modelSearchMu.Unlock()
	discovery := binding.DiscoverModelSearch(ctx, metadata)
	r.modelSearchMu.Lock()
	if r.modelSearchDiscoveryCache == nil {
		r.modelSearchDiscoveryCache = map[string]modelSearchDiscoveryCacheEntry{}
	}
	r.modelSearchDiscoveryCache[key] = modelSearchDiscoveryCacheEntry{generation: binding.Generation(), request: request, discovery: discovery}
	r.modelSearchMu.Unlock()
	return discovery
}

func (r *RuntimeRouter) threadModelSearchProvider(threadID string) modelSearchProvider {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return ""
	}
	r.modelSearchMu.Lock()
	provider := r.modelSearchOwners[threadID]
	if provider == "" && len(r.modelSearchReservations[threadID]) > 0 {
		provider = modelSearchNative
	}
	restored := r.modelSearchRestored[threadID]
	r.modelSearchMu.Unlock()
	if provider != "" || restored {
		return provider
	}
	native := false
	if record, err := r.threadRecord(session.ThreadID(threadID), false, true); err == nil && record != nil {
		native = historyUsesNativeSearch(record.Items)
	}
	r.modelSearchMu.Lock()
	if r.modelSearchRestored == nil {
		r.modelSearchRestored = map[string]bool{}
	}
	if !r.modelSearchRestored[threadID] {
		r.modelSearchRestored[threadID] = true
		if native {
			if r.modelSearchOwners == nil {
				r.modelSearchOwners = map[string]modelSearchProvider{}
			}
			r.modelSearchOwners[threadID] = modelSearchNative
		}
	}
	provider = r.modelSearchOwners[threadID]
	if provider == "" && len(r.modelSearchReservations[threadID]) > 0 {
		provider = modelSearchNative
	}
	r.modelSearchMu.Unlock()
	return provider
}

func historyUsesNativeSearch(items []session.Item) bool {
	for i := range items {
		raw := harnessMetadataRawFromItem(&items[i])
		if len(raw) == 0 {
			continue
		}
		var fields map[string]any
		if json.Unmarshal(raw, &fields) == nil {
			if used, _ := fields["native_search_fallback_used"].(bool); used {
				return true
			}
		}
	}
	return false
}

func (r *RuntimeRouter) claimModelSearchProvider(threadID string, provider modelSearchProvider) bool {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || provider == "" {
		return false
	}
	// Lazily restore a durable native owner before deciding whether this call
	// may run. A live fallback reservation also blocks MCP without becoming a
	// persisted owner unless native search is actually used.
	_ = r.threadModelSearchProvider(threadID)
	r.modelSearchMu.Lock()
	defer r.modelSearchMu.Unlock()
	if r.modelSearchOwners == nil {
		r.modelSearchOwners = map[string]modelSearchProvider{}
	}
	if current := r.modelSearchOwners[threadID]; current != "" && current != provider {
		return false
	}
	if provider == modelSearchClientMCP && len(r.modelSearchReservations[threadID]) > 0 {
		return false
	}
	r.modelSearchOwners[threadID] = provider
	if r.modelSearchRestored == nil {
		r.modelSearchRestored = map[string]bool{}
	}
	r.modelSearchRestored[threadID] = true
	return true
}

// reserveModelSearchNative keeps a fallback offer from racing a background MCP
// call while the model request can still issue built-in native search. The
// reservation becomes durable native ownership only when a native search runs.
func (r *RuntimeRouter) reserveModelSearchNative(threadID string) (uint64, func(), bool) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return 0, nil, false
	}
	_ = r.threadModelSearchProvider(threadID)
	r.modelSearchMu.Lock()
	if r.modelSearchOwners[threadID] == modelSearchClientMCP || r.modelSearchOwners[threadID] == modelSearchHosted {
		r.modelSearchMu.Unlock()
		return 0, nil, false
	}
	r.modelSearchReservationSeq++
	id := r.modelSearchReservationSeq
	if r.modelSearchReservations == nil {
		r.modelSearchReservations = map[string]map[uint64]*modelSearchReservation{}
	}
	if r.modelSearchReservations[threadID] == nil {
		r.modelSearchReservations[threadID] = map[uint64]*modelSearchReservation{}
	}
	state := &modelSearchReservation{threadID: threadID}
	r.modelSearchReservations[threadID][id] = state
	r.modelSearchMu.Unlock()
	release := func() {
		r.modelSearchMu.Lock()
		defer r.modelSearchMu.Unlock()
		if reservations := r.modelSearchReservations[threadID]; reservations != nil {
			delete(reservations, id)
			if len(reservations) == 0 {
				delete(r.modelSearchReservations, threadID)
			}
		}
	}
	return id, release, true
}

func (r *RuntimeRouter) reserveModelSearchSelection(threadID string, selection modelSearchSelection) (modelSearchSelection, uint64) {
	if selection.provider != modelSearchNative || !selection.fallback {
		return selection, 0
	}
	id, _, ok := r.reserveModelSearchNative(threadID)
	if !ok {
		return modelSearchSelection{provider: modelSearchDisabled, reason: "fallback_restricted"}, 0
	}
	return selection, id
}

func (r *RuntimeRouter) reserveModelSearchForSampling(threadID string, turnID string) (func(), error) {
	if r == nil {
		return nil, nil
	}
	active := r.activeRuntimeTurnStateSnapshot(threadID, turnID)
	if active == nil || active.ModelSearchProvider != modelSearchNative || !active.ModelSearchFallback {
		return nil, nil
	}
	r.modelSearchMu.Lock()
	if state := r.modelSearchReservations[strings.TrimSpace(threadID)][active.ModelSearchReservationID]; state != nil {
		id := active.ModelSearchReservationID
		r.modelSearchMu.Unlock()
		return func() {
			r.releaseModelSearchReservation(threadID, id)
		}, nil
	}
	r.modelSearchMu.Unlock()
	id, release, ok := r.reserveModelSearchNative(threadID)
	if !ok {
		return nil, turn.ErrSearchReservationUnavailable
	}
	r.threads.UpdateTurn(strings.TrimSpace(threadID), strings.TrimSpace(turnID), func(active *activeRuntimeTurn) {
		active.ModelSearchReservationID = id
	})
	return release, nil
}

func (r *RuntimeRouter) releaseModelSearchReservation(threadID string, id uint64) {
	if r == nil || id == 0 {
		return
	}
	threadID = strings.TrimSpace(threadID)
	r.modelSearchMu.Lock()
	if reservations := r.modelSearchReservations[threadID]; reservations != nil {
		delete(reservations, id)
		if len(reservations) == 0 {
			delete(r.modelSearchReservations, threadID)
		}
	}
	r.modelSearchMu.Unlock()
}

func (r *RuntimeRouter) recordModelSearchUse(threadID string, turnID string, provider modelSearchProvider) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if r == nil || threadID == "" || turnID == "" {
		return
	}
	flag := modelSearchUsedNative
	if provider == modelSearchHosted || provider == modelSearchClientMCP {
		flag = modelSearchUsedMCP
	}
	r.modelSearchMu.Lock()
	if r.modelSearchTurnUsage == nil {
		r.modelSearchTurnUsage = map[string]uint8{}
	}
	key := threadID + "\x00" + turnID
	previous := r.modelSearchTurnUsage[key]
	r.modelSearchTurnUsage[key] = previous | flag
	r.modelSearchMu.Unlock()
	// Rust's SearchTurnUsage records the first actual use of each provider in
	// a turn and an extra mixed count when both providers served it.
	if previous&flag == 0 {
		r.recordModelSearchCounter(modelSearchTurnMetric, map[string]string{"provider": modelSearchProviderMetricLabel(provider)})
		if previous != 0 {
			r.recordModelSearchCounter(modelSearchTurnMetric, map[string]string{"provider": "mixed"})
		}
	}
}

func modelSearchProviderMetricLabel(provider modelSearchProvider) string {
	if provider == modelSearchNative {
		return "native"
	}
	return "search_mcp"
}

func (r *RuntimeRouter) recordModelSearchCounter(name string, tags map[string]string) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	r.services.TurnMetrics.Counter(name, 1, tags)
}

func (r *RuntimeRouter) modelSearchTurnUses(threadID string, turnID string, flag uint8) bool {
	if r == nil {
		return false
	}
	r.modelSearchMu.Lock()
	defer r.modelSearchMu.Unlock()
	return r.modelSearchTurnUsage[strings.TrimSpace(threadID)+"\x00"+strings.TrimSpace(turnID)]&flag != 0
}

func (r *RuntimeRouter) modelSearchMCPAdmission(threadID string, turnID string, selection modelSearchSelection) func(*mcp.RuntimeToolInfo, string) error {
	return func(info *mcp.RuntimeToolInfo, _ string) error {
		if !mcp.IsModelSearchTool(info) {
			return nil
		}
		provider := selection.provider
		if provider != modelSearchHosted && provider != modelSearchClientMCP {
			return errors.New("Search MCP is not available for this step")
		}
		if !r.claimModelSearchProvider(threadID, modelSearchClientMCP) {
			return errors.New("This thread is using native search")
		}
		r.recordModelSearchUse(threadID, turnID, modelSearchClientMCP)
		return nil
	}
}

func (r *RuntimeRouter) nativeModelSearchOptions(options *turn.WebSearchOptions, threadID string, turnID string) *turn.WebSearchOptions {
	if options == nil {
		return nil
	}
	clone := *options
	clone.Admit = func() error {
		if !r.claimModelSearchProvider(threadID, modelSearchNative) {
			return errors.New("Search MCP is already in use in this thread")
		}
		r.recordModelSearchUse(threadID, turnID, modelSearchNative)
		return nil
	}
	return &clone
}

func (r *RuntimeRouter) annotateModelSearchMetadata(threadID string, items []session.Item) {
	if r == nil || len(items) == 0 || r.threadModelSearchProvider(threadID) != modelSearchNative {
		return
	}
	for i := range items {
		if strings.TrimSpace(items[i].Type) == "webSearch" || strings.TrimSpace(items[i].Type) == "web_search_call" {
			mergeSessionItemHarnessMetadata(&items[i], map[string]any{"native_search_fallback_used": true})
		}
	}
}

func (r *RuntimeRouter) clearModelSearchReservations(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	r.modelSearchMu.Lock()
	delete(r.modelSearchReservations, threadID)
	r.modelSearchMu.Unlock()
}

func (r *RuntimeRouter) deleteModelSearchThreadState(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.modelSearchMu.Lock()
	delete(r.modelSearchOwners, threadID)
	delete(r.modelSearchRestored, threadID)
	delete(r.modelSearchReservations, threadID)
	for key := range r.modelSearchTurnUsage {
		if strings.HasPrefix(key, threadID+"\x00") {
			delete(r.modelSearchTurnUsage, key)
		}
	}
	for key := range r.modelSearchDiscoveryCache {
		if strings.HasPrefix(key, threadID+"\x00") {
			delete(r.modelSearchDiscoveryCache, key)
		}
	}
	r.modelSearchMu.Unlock()
}

func modelSearchRequestDocument(provider func() map[string]any) map[string]any {
	if provider == nil {
		return map[string]any{}
	}
	if document := provider(); document != nil {
		return cloneSearchObject(document)
	}
	return map[string]any{}
}
