package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Session-startup prewarm mirrors Rust core/src/session_startup_prewarm.rs:
// when a thread is created, an asynchronous websocket prewarm is scheduled so
// the first regular turn reuses the established connection (its response id
// becomes the first request's PreviousResponseID) instead of paying the
// connection latency. The prewarm is token-free (generate=false) and
// best-effort: a turn that starts before the prewarm finishes proceeds without
// it, matching Rust's Unavailable resolution.

const startupPrewarmTimeout = 15 * time.Second

// startupPrewarmState is the per-thread prewarm result.
type startupPrewarmState struct {
	responseID string
	startedAt  time.Time
	finished   bool
	finishedAt time.Time
	// status mirrors Rust's prewarm task status ("ready" when the websocket
	// prewarm returned without error, otherwise "failed").
	status   string
	consumed bool
	// inputMode is "base" for the empty startup prewarm and "history" for the
	// idle-thread prewarm (Rust #48812).
	inputMode string
	// historyKeys and settingsKey gate reuse: only a prompt that extends the
	// prewarmed history under the same model/settings may chain its response id.
	historyKeys []string
	settingsKey string
}

// scheduleStartupPrewarm kicks off an asynchronous websocket prewarm for a
// newly created thread (Rust Session::schedule_startup_prewarm, session.rs),
// called from the ThreadStart lifecycle. Agents that do not implement the
// prewarm interface are skipped (Rust's websocket-disabled path falls back to
// auth prewarm, which Go's auth store resolves lazily on demand).
func (r *RuntimeRouter) scheduleStartupPrewarm(response *ThreadStartResponse) {
	if r == nil || response == nil || response.Thread == nil {
		return
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	if threadID == "" {
		return
	}
	r.scheduleCodeModeHostPrewarm(threadID, response.Thread.CWD)
	if r.services.Agent == nil {
		return
	}
	prewarmer, ok := r.services.Agent.(guardianPrewarmer)
	if !ok {
		return
	}
	r.startupPrewarmMu.Lock()
	if r.startupPrewarms == nil {
		r.startupPrewarms = map[string]*startupPrewarmState{}
	}
	if existing := r.startupPrewarms[threadID]; existing != nil && !existing.finished {
		// Rust shares an in-flight warmup with every later resume instead of
		// starting a second one.
		r.startupPrewarmMu.Unlock()
		return
	}
	// Rust #47709: a completed warmup returns its client to the cache and the
	// next resume re-checks the socket; when that check skips preparation the
	// completed baseline below stays valid.
	retained := r.startupPrewarms[threadID]
	if retained != nil && retained.consumed {
		retained = nil
	}
	startedAt := time.Now().UTC()
	r.startupPrewarms[threadID] = &startupPrewarmState{startedAt: startedAt, inputMode: "base"}
	r.startupPrewarmMu.Unlock()

	modelID := strings.TrimSpace(response.Model)
	cwd := strings.TrimSpace(response.CWD)
	if cwd == "" && response.Thread != nil {
		cwd = strings.TrimSpace(response.Thread.CWD)
	}
	providerID := strings.TrimSpace(response.ModelProvider)
	serviceTier := ""
	if response.ServiceTier != nil {
		serviceTier = strings.TrimSpace(*response.ServiceTier)
	}
	// Rust #43795: the warmup request resolves the request effort exactly like a
	// sampling request, so the pin is established before the first turn can
	// change the selection.
	params := &turn.TurnStartParams{
		ThreadID: threadID,
		CWD:      cwd,
		Model:    modelID,
		Effort:   response.ReasoningEffort,
	}
	effort := ""
	if cfg, cfgErr := r.effectiveConfigForTurn(params); cfgErr == nil && cfg != nil {
		effort = r.reasoningEffortForPrewarm(cfg, params, modelID, providerID)
	}
	// Rust #47709: the warmup handshake carries the startup turn's executed
	// settings and window/fork metadata (the request kind is `prewarm`), so the
	// prepared connection matches the window the first turn will use.
	lineage := r.responsesMetadataLineage(threadID)
	prewarmMetadata := turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
		InstallationID:             r.guardianInstallationID(),
		SessionID:                  firstNonEmpty(lineage.SessionID, threadID),
		ThreadID:                   threadID,
		WindowID:                   r.windowIDForThread(threadID),
		ContextWindowID:            r.contextWindowIDForThread(threadID),
		WindowNumber:               uint64PtrAppserver(r.windowNumberForThread(threadID)),
		RequestKind:                codexapi.ClientRequestPrewarm,
		ForkedFromThreadID:         lineage.ForkedFromThreadID,
		ForkedFromOrdinalExclusive: lineage.ForkedFromOrdinalExclusive,
		ParentThreadID:             lineage.ParentThreadID,
		RootTurnID:                 effectiveRootTurnID("", "", "", lineage.SubagentHeader),
		SubagentHeader:             lineage.SubagentHeader,
		SubagentKind:               lineage.SubagentKind,
		ThreadSource:               lineage.ThreadSource,
		CodexVersion:               appServerVersion(),
		Model:                      modelID,
		ReasoningEffort:            effort,
		UseResponsesLite:           r.modelUsesResponsesLite(modelID),
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), startupPrewarmTimeout)
		defer cancel()
		var responseID string
		status := "failed"
		resp, err := prewarmer.Prewarm(ctx, &model.AgentRequest{
			Model:           modelID,
			ServiceTier:     serviceTier,
			ReasoningEffort: effort,
			// Rust #47912: thread/start registers the creating connection before
			// the startup prewarm dials, so the handshake can request attestation
			// for the thread. The request therefore carries the thread id and the
			// app-server attestation provider.
			ThreadID:            threadID,
			AttestationProvider: r.appServerAttestationProvider(),
			PrewarmTools:        r.startupPrewarmToolProvider(params),
			Originator:          "session_startup",
			ClientMetadata:      prewarmMetadata,
		})
		if err == nil {
			status = "ready"
		}
		if err == nil && resp != nil {
			responseID = strings.TrimSpace(resp.ResponseID)
		}
		finishedAt := time.Now().UTC()
		r.startupPrewarmMu.Lock()
		if state := r.startupPrewarms[threadID]; state != nil {
			state.responseID = responseID
			state.finished = true
			state.finishedAt = finishedAt
			state.status = status
			if responseID == "" && err == nil && retained != nil {
				// The warmup skipped preparation on a socket that is still warm,
				// so the previous baseline remains the connection's last request.
				state.responseID = retained.responseID
			}
		}
		r.startupPrewarmMu.Unlock()
		// Rust's prewarm task records these when it completes.
		r.recordStartupPhase("startup_prewarm_total", finishedAt.Sub(startedAt), status)
		r.recordStartupPrewarmDuration(status, "base", finishedAt.Sub(startedAt))
	}()
}

// scheduleHistoryPrewarm prepares an idle thread's cached WebSocket with its
// existing conversation history and executed-tool metadata using generate:false
// (Rust #48812). The next turn may consume the response id only when its input
// extends this history and its model settings match.
func (r *RuntimeRouter) scheduleHistoryPrewarm(threadID string) {
	if r == nil || r.services.Agent == nil || r.services.ThreadRouter == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || r.threads == nil || r.threads.LiveThread(session.ThreadID(threadID)) == nil {
		return
	}
	prewarmer, ok := r.services.Agent.(guardianPrewarmer)
	if !ok {
		return
	}
	historyItems, _ := r.historyInputItemsForTurn(threadID)
	if len(historyItems) == 0 {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil || record == nil {
		return
	}
	params := &turn.TurnStartParams{
		ThreadID: threadID,
		CWD:      strings.TrimSpace(record.Metadata.CWD),
		Model:    strings.TrimSpace(record.Metadata.Model),
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil || cfg == nil {
		return
	}
	modelID := strings.TrimSpace(record.Metadata.Model)
	if modelID == "" {
		modelID = strings.TrimSpace(stringFromAny(cfg.Values["model"]))
	}
	if modelID == "" {
		return
	}
	params.Model = modelID
	providerID := strings.TrimSpace(record.Metadata.ModelProvider)
	if providerID == "" && record.Metadata.Extra != nil {
		providerID = firstNonEmpty(
			stringFromMap(record.Metadata.Extra, "model_provider"),
			stringFromMap(record.Metadata.Extra, "modelProvider"),
			stringFromMap(record.Metadata.Extra, "provider"),
		)
	}
	effort := r.reasoningEffortForPrewarm(cfg, params, modelID, providerID)
	instructions, _ := appBaseInstructionsForConfig(cfg)
	settingsKey := prewarmSettingsKey(modelID, instructions, record.Metadata.ServiceTier)
	historyKeys := prewarmHistoryKeys(historyItems)

	r.startupPrewarmMu.Lock()
	if r.startupPrewarms == nil {
		r.startupPrewarms = map[string]*startupPrewarmState{}
	}
	if existing := r.startupPrewarms[threadID]; existing != nil && !existing.finished {
		// Rust keeps an in-flight warmup shared with every later resume.
		r.startupPrewarmMu.Unlock()
		return
	}
	// Rust #47709: a completed warmup returns its client to the cache and the
	// next resume re-checks the socket, which may have closed since then. The
	// warmup below performs that check; when it skips preparation the retained
	// baseline is still the connection's last request.
	retained := r.startupPrewarms[threadID]
	if retained != nil && (retained.consumed ||
		retained.settingsKey != settingsKey ||
		!equalStringSlices(retained.historyKeys, historyKeys)) {
		retained = nil
	}
	startedAt := time.Now().UTC()
	r.startupPrewarms[threadID] = &startupPrewarmState{
		startedAt:   startedAt,
		inputMode:   "history",
		historyKeys: historyKeys,
		settingsKey: settingsKey,
	}
	r.startupPrewarmMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), startupPrewarmTimeout)
		defer cancel()
		var responseID string
		status := "failed"
		resp, err := prewarmer.Prewarm(ctx, &model.AgentRequest{
			Model:           modelID,
			ServiceTier:     strings.TrimSpace(record.Metadata.ServiceTier),
			ReasoningEffort: effort,
			Instructions:    instructions,
			InputItems:      historyItems,
			ThreadID:        threadID,
			// Rust #47912 applies to the idle-thread prewarm as well: the
			// handshake resolves attestation through the creating connection.
			AttestationProvider: r.appServerAttestationProvider(),
			Originator:          "session_startup",
			PrewarmInputMode:    "history",
		})
		if err == nil {
			status = "ready"
		}
		if err == nil && resp != nil {
			responseID = strings.TrimSpace(resp.ResponseID)
		}
		finishedAt := time.Now().UTC()
		r.startupPrewarmMu.Lock()
		if state := r.startupPrewarms[threadID]; state != nil && state.inputMode == "history" {
			state.responseID = responseID
			state.finished = true
			state.finishedAt = finishedAt
			state.status = status
			if responseID == "" && err == nil && retained != nil {
				// The warmup skipped preparation, so keep the prepared baseline.
				state.responseID = retained.responseID
			}
		}
		r.startupPrewarmMu.Unlock()
		r.recordStartupPhase("startup_prewarm_total", finishedAt.Sub(startedAt), status)
		r.recordStartupPrewarmDuration(status, "history", finishedAt.Sub(startedAt))
	}()
}

// reasoningEffortForPrewarm mirrors Rust #43795: a WebSocket prewarm resolves
// the request effort through the same pinning path as sampling, so later
// settings changes only add configuration updates instead of replacing the
// request baseline.
func (r *RuntimeRouter) reasoningEffortForPrewarm(cfg *config.Config, params *turn.TurnStartParams, modelID, providerID string) string {
	if r == nil || cfg == nil || params == nil || strings.TrimSpace(modelID) == "" {
		return ""
	}
	info := r.modelInfoForRuntimeWithConfig(modelID, cfg)
	overrideEffort, overrideAvailable := r.effortForConfigurationUpdate(params.ThreadID, cfg, params, info, providerID)
	return r.reasoningEffortForRequest(
		params.ThreadID,
		reasoningEffortModelSlug(info, modelID),
		appReasoningEffortForTurn(cfg, params),
		r.reasoningEffortOverrideEnabled(params.ThreadID, cfg, info, providerID),
		overrideEffort,
		overrideAvailable,
		requestEffortSampling,
	)
}

func (r *RuntimeRouter) scheduleCodeModeHostPrewarm(threadID, cwd string) {
	if r == nil || r.services.CodeModeProvider == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{
		ThreadID: threadID,
		CWD:      firstNonEmpty(strings.TrimSpace(cwd), r.services.DefaultCWD),
	})
	if err != nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "code_mode_prewarm") {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), startupPrewarmTimeout)
		defer cancel()
		if err := r.codeModeRuntimeForThread(threadID).Prewarm(ctx); err != nil && ctx.Err() == nil {
			slog.Debug("code-mode host startup prewarm failed", "thread_id", threadID, "error", err)
		}
	}()
}

// takeStartupPrewarmResponseID returns and consumes the prewarmed response id
// for the thread when the prewarm finished before the turn started; otherwise
// it returns "" and the turn proceeds without the prewarm (Rust
// consume_startup_prewarm_for_regular_turn -> Unavailable).
func (r *RuntimeRouter) takeStartupPrewarmResponseID(threadID string, historyItems []any, settingsKey string) (string, int) {
	if r == nil {
		return "", 0
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", 0
	}
	r.startupPrewarmMu.Lock()
	state := r.startupPrewarms[threadID]
	if state == nil || !state.finished || state.consumed {
		r.startupPrewarmMu.Unlock()
		return "", 0
	}
	consumed := 0
	if state.inputMode == "history" {
		if state.settingsKey != settingsKey || !historyExtendsPrewarm(state.historyKeys, prewarmHistoryKeys(historyItems)) {
			// A non-prefix prompt or changed settings cannot chain the prepared
			// response; consume it and let the regular request proceed.
			state.consumed = true
			r.startupPrewarmMu.Unlock()
			return "", 0
		}
		consumed = len(state.historyKeys)
	}
	responseID := strings.TrimSpace(state.responseID)
	age := time.Since(state.startedAt)
	if age < 0 {
		age = 0
	}
	if responseID == "" {
		// The prewarm finished without a usable response id; Rust reports the
		// prewarm setup failure as unavailable with status "failed".
		r.startupPrewarmMu.Unlock()
		r.recordStartupPrewarmAgeAtFirstTurn("failed", age)
		return "", 0
	}
	state.consumed = true
	r.startupPrewarmMu.Unlock()
	r.recordStartupPrewarmAgeAtFirstTurn("consumed", age)
	return responseID, consumed
}

func prewarmSettingsKey(modelID, instructions, serviceTier string) string {
	return strings.Join([]string{
		strings.TrimSpace(modelID),
		strings.TrimSpace(instructions),
		strings.TrimSpace(serviceTier),
	}, "\x00")
}

func prewarmHistoryKeys(items []any) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			keys = append(keys, "")
			continue
		}
		sum := sha256.Sum256(data)
		keys = append(keys, hex.EncodeToString(sum[:]))
	}
	return keys
}

func historyExtendsPrewarm(prewarmed []string, current []string) bool {
	if len(prewarmed) > len(current) {
		return false
	}
	for index := range prewarmed {
		if prewarmed[index] != current[index] {
			return false
		}
	}
	return true
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// startupPrewarmsSnapshot returns a copy of the prewarm states for tests and
// diagnostics.
func (r *RuntimeRouter) startupPrewarmsSnapshot() map[string]*startupPrewarmState {
	out := map[string]*startupPrewarmState{}
	if r == nil {
		return out
	}
	r.startupPrewarmMu.Lock()
	defer r.startupPrewarmMu.Unlock()
	for threadID, state := range r.startupPrewarms {
		clone := *state
		out[threadID] = &clone
	}
	return out
}

// startupPrewarmToolProvider mirrors Rust #47635: the startup prewarm captures
// its own step tools (including MCP discovery) so the generate=false warmup
// carries the same inventory as the first turn's request. The capture runs
// concurrently with the websocket handshake; a failure leaves the warmup
// tool-less instead of blocking the connection.
func (r *RuntimeRouter) startupPrewarmToolProvider(params *turn.TurnStartParams) func(context.Context) []any {
	if r == nil || params == nil {
		return nil
	}
	return func(ctx context.Context) []any {
		runtime, err := r.buildTurnRuntimeContext(ctx, params, "")
		if err != nil || runtime == nil {
			return nil
		}
		cfg, cfgErr := r.effectiveConfigForTurn(params)
		if cfgErr != nil {
			cfg = nil
		}
		modelID := strings.TrimSpace(params.Model)
		if modelID == "" && cfg != nil {
			modelID = strings.TrimSpace(stringFromAny(cfg.Values["model"]))
		}
		toolMode := ""
		if info := r.modelInfoForRuntimeWithConfig(modelID, cfg); info != nil {
			toolMode = info.ToolMode
		}
		disableFallback := false
		if cfg != nil {
			toolMode = model.ResolveToolMode(toolMode, cfg.FeatureSettings())
			disableFallback = cfg.DisableCodeModeInProcessFallback()
		}
		prepared, _ := runtime.PrepareToolMode(toolMode, disableFallback)
		return runtime.ModelVisibleToolDefinitions(prepared, disableFallback)
	}
}
