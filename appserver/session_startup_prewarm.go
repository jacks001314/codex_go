package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"reflect"
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

// Prewarm input labels mirror Rust's PrewarmInput::as_str
// (core/src/session/startup_prewarm.rs).
const (
	// startupPrewarmInputBase warms only the connection and base instructions.
	startupPrewarmInputBase = "base"
	// startupPrewarmInputHistory warms an idle thread's existing history (Rust
	// #48812).
	startupPrewarmInputHistory = "history"
	// startupPrewarmInputRestoredHistory warms the history a cold resume loaded,
	// including its file images, and drops its own response id (Rust #61959).
	startupPrewarmInputRestoredHistory = "restored_history"
)

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
	// inputMode is startupPrewarmInputBase for the empty startup prewarm,
	// startupPrewarmInputHistory for the idle-thread prewarm (Rust #48812), or
	// startupPrewarmInputRestoredHistory for a cold-resume image warmup (Rust
	// #61959), whose response id is never chained by a later turn.
	inputMode string
	// historyKeys and settingsKey gate reuse: only a prompt that extends the
	// prewarmed history under the same model/settings may chain its response id.
	historyKeys []string
	settingsKey string
}

// startupPrewarmJob is one scheduled warm-up: the resolved generate=false
// request plus the bookkeeping a later turn uses to decide whether it may chain
// the prepared response.
type startupPrewarmJob struct {
	threadID     string
	inputMode    string
	modelID      string
	serviceTier  string
	effort       string
	instructions string
	inputItems   []any
	params       *turn.TurnStartParams
	historyKeys  []string
	settingsKey  string
	retained     *startupPrewarmState
	startedAt    time.Time
}

// beginStartupPrewarm publishes a warm-up before its network work starts so a
// later resume shares the in-flight one instead of dialing twice (Rust keeps a
// single startup prewarm per session). It reports false when another warm-up is
// still running.
func (r *RuntimeRouter) beginStartupPrewarm(threadID string, inputMode string, historyKeys []string, settingsKey string) (startupPrewarmJob, bool) {
	r.startupPrewarmMu.Lock()
	defer r.startupPrewarmMu.Unlock()
	if r.startupPrewarms == nil {
		r.startupPrewarms = map[string]*startupPrewarmState{}
	}
	if existing := r.startupPrewarms[threadID]; existing != nil && !existing.finished {
		return startupPrewarmJob{}, false
	}
	// Rust #47709: a completed warmup returns its client to the cache and the
	// next resume re-checks the socket; when that check skips preparation the
	// retained baseline below stays valid.
	retained := r.startupPrewarms[threadID]
	switch inputMode {
	case startupPrewarmInputHistory:
		if retained != nil && (retained.consumed ||
			retained.settingsKey != settingsKey ||
			!equalStringSlices(retained.historyKeys, historyKeys)) {
			retained = nil
		}
	case startupPrewarmInputRestoredHistory:
		// A restored-history warmup never hands its response to a turn, so no
		// baseline from an earlier warmup can survive it.
		retained = nil
	default:
		if retained != nil && retained.consumed {
			retained = nil
		}
	}
	startedAt := time.Now().UTC()
	r.startupPrewarms[threadID] = &startupPrewarmState{
		startedAt:   startedAt,
		inputMode:   inputMode,
		historyKeys: historyKeys,
		settingsKey: settingsKey,
	}
	return startupPrewarmJob{
		threadID:    threadID,
		inputMode:   inputMode,
		historyKeys: historyKeys,
		settingsKey: settingsKey,
		retained:    retained,
		startedAt:   startedAt,
	}, true
}

// runStartupPrewarm dials and sends the job's generate=false request. The
// handshake metadata is built before the goroutine so the prepared connection
// carries the window and fork lineage of the thread it warms.
func (r *RuntimeRouter) runStartupPrewarm(job startupPrewarmJob) {
	prewarmer, ok := r.services.Agent.(guardianPrewarmer)
	if !ok {
		return
	}
	threadID := job.threadID
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
		Model:                      job.modelID,
		ReasoningEffort:            job.effort,
		UseResponsesLite:           r.modelUsesResponsesLite(job.modelID),
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), startupPrewarmTimeout)
		defer cancel()
		var responseID string
		status := "failed"
		resp, err := prewarmer.Prewarm(ctx, &model.AgentRequest{
			Model:           job.modelID,
			ServiceTier:     job.serviceTier,
			ReasoningEffort: job.effort,
			// Rust #61535: the startup prewarm turn captures the thread's
			// trusted host country like any other sampling request.
			RequestCountry: r.requestCountryForThread(threadID),
			Instructions:   job.instructions,
			InputItems:     job.inputItems,
			// Rust #47912: the creating connection is registered before the
			// startup prewarm dials, so the handshake can request attestation
			// for the thread.
			ThreadID:            threadID,
			AttestationProvider: r.appServerAttestationProvider(),
			PrewarmTools:        r.startupPrewarmToolProvider(job.params),
			Originator:          "session_startup",
			ClientMetadata:      prewarmMetadata,
			PrewarmInputMode:    job.inputMode,
		})
		if err == nil {
			status = "ready"
		}
		if err == nil && resp != nil {
			responseID = strings.TrimSpace(resp.ResponseID)
		}
		finishedAt := time.Now().UTC()
		r.startupPrewarmMu.Lock()
		if state := r.startupPrewarms[threadID]; state != nil && state.inputMode == job.inputMode {
			state.responseID = responseID
			state.finished = true
			state.finishedAt = finishedAt
			state.status = status
			if responseID == "" && err == nil && job.retained != nil {
				// The warmup skipped preparation on a socket that is still warm,
				// so the previous baseline remains the connection's last request.
				state.responseID = job.retained.responseID
			}
		}
		r.startupPrewarmMu.Unlock()
		// Rust's prewarm task records these when it completes.
		r.recordStartupPhase("startup_prewarm_total", finishedAt.Sub(job.startedAt), status)
		r.recordStartupPrewarmDuration(status, job.inputMode, finishedAt.Sub(job.startedAt))
	}()
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
	if _, ok := r.services.Agent.(guardianPrewarmer); !ok {
		return
	}
	job, ok := r.beginStartupPrewarm(threadID, startupPrewarmInputBase, nil, "")
	if !ok {
		return
	}

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
	job.modelID = modelID
	job.serviceTier = serviceTier
	job.effort = effort
	job.params = params
	r.runStartupPrewarm(job)
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
	if _, ok := r.services.Agent.(guardianPrewarmer); !ok {
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
	job, ok := r.beginStartupPrewarm(threadID, startupPrewarmInputHistory, historyKeys, settingsKey)
	if !ok {
		return
	}
	job.modelID = modelID
	job.serviceTier = strings.TrimSpace(record.Metadata.ServiceTier)
	job.effort = effort
	job.instructions = instructions
	job.inputItems = historyItems
	job.params = params
	r.runStartupPrewarm(job)
}

// scheduleResumePrewarm mirrors Rust #61959: a cold resume starts the
// websocket handshake while the restored history is loaded. When the
// `restored_history_prewarm` feature is enabled and that history carries file
// images, the warm-up carries the full history so the socket (and its
// normalized-image cache) already knows them, but it never hands its response
// id to the first turn. A history without file images keeps the plain base
// warm-up. When the handshake falls back to HTTP the warm-up prepares nothing
// and generation prepares its own input, exactly like Rust.
func (r *RuntimeRouter) scheduleResumePrewarm(threadID string) {
	if r == nil || r.services.Agent == nil || r.services.ThreadRouter == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || r.threads == nil || r.threads.LiveThread(session.ThreadID(threadID)) == nil {
		return
	}
	if _, ok := r.services.Agent.(guardianPrewarmer); !ok {
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
	inputMode := startupPrewarmInputBase
	var inputItems []any
	modelInfo := r.modelInfoForRuntimeWithConfig(modelID, cfg)
	if features.Enabled(cfg.FeatureSettings(), "restored_history_prewarm") {
		historyItems, _ := r.historyInputItemsForTurn(threadID)
		// Rust projects the history through `for_prompt` before deciding, so a
		// model without image input never warms the restored images.
		if restoredHistoryPrewarmCandidate(historyItems, modelInfo) {
			inputMode = startupPrewarmInputRestoredHistory
			inputItems = historyItems
		}
	}
	// Rust defers this read until history is ready because the restore can
	// change settings; the warm-up must still carry the instructions the first
	// turn will send.
	instructions, err := appBaseInstructionsForConfig(cfg)
	if err != nil {
		return
	}
	instructions = firstNonEmpty(turnBaseInstructions(params), instructions)
	if instructions == "" && modelInfo != nil {
		instructions = modelInfo.ModelInstructions(appPersonalityForTurn(cfg, params))
	}
	job, ok := r.beginStartupPrewarm(threadID, inputMode, nil, "")
	if !ok {
		return
	}
	job.modelID = modelID
	job.serviceTier = strings.TrimSpace(record.Metadata.ServiceTier)
	job.effort = r.reasoningEffortForPrewarm(cfg, params, modelID, providerID)
	job.instructions = instructions
	job.inputItems = inputItems
	job.params = params
	r.runStartupPrewarm(job)
}

// restoredHistoryPrewarmCandidate reports whether a cold resume warms the
// restored history instead of only the connection (Rust #61959): the snapshot
// is non-empty, the model accepts images, and the projected history still
// carries a file image (inline images already live in the request body and get
// no benefit from the socket's normalized-image cache).
func restoredHistoryPrewarmCandidate(items []any, info *model.ModelInfo) bool {
	if len(items) == 0 || !appModelInfoSupportsImageInput(info) {
		return false
	}
	return prewarmHistoryHasFileImage(items)
}

// prewarmHistoryHasFileImage mirrors Rust's `has_file_image`: only message
// parts and tool-output content items that reference a stored file count.
func prewarmHistoryHasFileImage(items []any) bool {
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(stringFromAny(item["type"])) {
		case "message":
			if prewarmContentHasFileImage(item["content"]) {
				return true
			}
		case "function_call_output", "custom_tool_call_output":
			if prewarmContentHasFileImage(item["output"]) || prewarmContentHasFileImage(item["content_items"]) {
				return true
			}
		}
	}
	return false
}

func prewarmContentHasFileImage(value any) bool {
	// History builders produce concrete slices ([]any, []map[string]any), so
	// walk the value reflectively instead of asserting one static type.
	parts := reflect.ValueOf(value)
	if !parts.IsValid() || (parts.Kind() != reflect.Slice && parts.Kind() != reflect.Array) {
		return false
	}
	for index := 0; index < parts.Len(); index++ {
		entry, ok := parts.Index(index).Interface().(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringFromAny(entry["type"])) != "input_image" {
			continue
		}
		if strings.TrimSpace(stringFromAny(entry["file_id"])) != "" {
			return true
		}
	}
	return false
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
	return r.reasoningEffortForRequestWithModel(
		params.ThreadID,
		info,
		catalogModelOrigin(r.catalogModelForAppTurn(modelID, cfg, params)),
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
	if state != nil && state.inputMode == startupPrewarmInputRestoredHistory && !state.consumed {
		// Rust #61959: the restored-history warmup retained the socket and its
		// normalized images but dropped its response id, so generation resends
		// the full history. The decision does not wait for the warm-up: the
		// generation's first request serializes behind it on the socket.
		state.consumed = true
		age := time.Since(state.startedAt)
		r.startupPrewarmMu.Unlock()
		if age < 0 {
			age = 0
		}
		r.recordStartupPrewarmAgeAtFirstTurn("consumed", age)
		return "", 0
	}
	if state == nil || !state.finished || state.consumed {
		r.startupPrewarmMu.Unlock()
		return "", 0
	}
	consumed := 0
	if state.inputMode == startupPrewarmInputHistory {
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

// consumeStartupPrewarmForTurn resolves one regular turn's claim on the
// thread's prepared warm-up. It returns the response id to chain (and the
// history prefix it already covers), or reports that the turn must drop its
// continuation and resend the full history (Rust #61959's
// `reuse_response = false`).
func (r *RuntimeRouter) consumeStartupPrewarmForTurn(threadID string, historyItems []any, settingsKey string, previousResponseID string) (string, int, bool) {
	if strings.TrimSpace(previousResponseID) == "" {
		id, consumed := r.takeStartupPrewarmResponseID(threadID, historyItems, settingsKey)
		return id, consumed, false
	}
	if r.discardRestoredHistoryPrewarm(threadID) {
		return "", 0, true
	}
	return "", 0, false
}

// discardRestoredHistoryPrewarm consumes a restored-history warmup that would
// otherwise be skipped because the thread already carries a persisted
// continuation. Rust never persists `previous_response_id`, so a resumed turn
// always resends the full history; dropping the restored warm-up's response is
// what lets Go do the same (Rust #61959 `reuse_response = false`).
func (r *RuntimeRouter) discardRestoredHistoryPrewarm(threadID string) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	r.startupPrewarmMu.Lock()
	state := r.startupPrewarms[threadID]
	if state == nil || state.consumed || state.inputMode != startupPrewarmInputRestoredHistory {
		r.startupPrewarmMu.Unlock()
		return false
	}
	state.consumed = true
	age := time.Since(state.startedAt)
	r.startupPrewarmMu.Unlock()
	if age < 0 {
		age = 0
	}
	r.recordStartupPrewarmAgeAtFirstTurn("consumed", age)
	return true
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
