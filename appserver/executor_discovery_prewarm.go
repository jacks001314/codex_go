package appserver

import (
	"context"
	"log/slog"
	"strings"
	"time"

	execserverclient "codex_go/execserver"
	"codex_go/features"
	"codex_go/turn"
)

// executorDiscoveryPrewarmTimeout bounds one executor's prewarm (Rust #62674
// `DISCOVERY_PREWARM_TIMEOUT`). A timed-out load records no cooldown, so the
// first step can retry it.
const executorDiscoveryPrewarmTimeout = 5 * time.Second

// startExecutorCapabilityDiscoveryPrewarm fetches the thread's remote executor
// inventories while the turn runtime is still being prepared (Rust #62674
// prewarms them while remote environments finish startup). The step that needs
// an inventory then either reuses the finished load or joins the one already in
// flight through the thread's discovery cache.
func (r *RuntimeRouter) startExecutorCapabilityDiscoveryPrewarm(ctx context.Context, threadID string, params *turn.TurnStartParams) {
	if r == nil || r.services.Environment == nil || r.threads == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	cache := r.threads.capabilityDiscoveryV2Cache(threadID)
	if cache == nil || cache.cache == nil {
		return
	}
	go r.prewarmExecutorCapabilityDiscoveryV2(ctx, threadID, params, cache)
}

// prewarmExecutorCapabilityDiscoveryV2 loads every supported remote executor's
// inventory into the thread cache, reserving capacity for the whole selection
// so the first step can reuse it. Discovery errors and timeouts leave turn
// startup untouched.
func (r *RuntimeRouter) prewarmExecutorCapabilityDiscoveryV2(ctx context.Context, threadID string, params *turn.TurnStartParams, cache *runtimeCapabilityDiscoveryV2Cache) {
	if r == nil || r.services.Environment == nil || cache == nil || cache.cache == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "executor_capability_discovery_v2") {
		return
	}
	selections := []map[string]any(nil)
	if params != nil {
		selections = params.Environments
	}
	if len(selections) == 0 {
		selections = r.threadEnvironmentSelections(strings.TrimSpace(threadID))
	}
	if len(selections) == 0 {
		return
	}
	stepTurnID := ""
	if active := r.threads.ActiveTurn(strings.TrimSpace(threadID)); active != nil {
		stepTurnID = strings.TrimSpace(active.TurnID)
	}
	// The step's cache key includes the sandbox context, so prewarm with the
	// same one the first step will request.
	sandboxContexts, _ := r.executorSkillSandboxContextsForTurn(cfg, turnCWD(params), strings.TrimSpace(threadID), stepTurnID, params)

	remoteEnvironmentCount := 0
	for _, selection := range selections {
		environmentID := selectionEnvironmentID(selection)
		if environmentID == "" || environmentID == execserverclient.LocalEnvironmentID {
			continue
		}
		if _, ok := r.services.Environment.Record(environmentID); ok {
			remoteEnvironmentCount++
		}
	}
	cache.cache.EnsureCapacity(remoteEnvironmentCount)

	for _, selection := range selections {
		if ctx.Err() != nil {
			return
		}
		state, stateErr := environmentConfigStateFromAnyMap(selection)
		if stateErr == nil && (state.Kind == EnvironmentConfigPending || state.Kind == EnvironmentConfigFailed) {
			continue
		}
		environmentID := selectionEnvironmentID(selection)
		if environmentID == "" || environmentID == execserverclient.LocalEnvironmentID {
			continue
		}
		record, ok := r.services.Environment.Record(environmentID)
		if !ok || record == nil {
			continue
		}
		cwd := environmentSelectionCWD(selection)
		if cwd == "" && record.CWD != nil {
			cwd = strings.TrimSpace(*record.CWD)
		}
		if !strings.HasPrefix(cwd, "file://") {
			continue
		}
		client, clientErr := cache.client(ctx, *record)
		if clientErr != nil {
			slog.Warn("executor discovery V2 prewarm could not connect", "environment_id", environmentID, "error", clientErr)
			continue
		}
		supported, capabilityErr := client.SupportsCapabilityDiscoveryV2(ctx)
		if capabilityErr != nil {
			slog.Warn("could not read executor discovery V2 support during prewarm", "environment_id", environmentID, "error", capabilityErr)
			continue
		}
		if !supported {
			// Rust #57192 keeps V1 discovery for executors without the V2
			// contract.
			continue
		}
		target := &executorCapabilityDiscoveryV2Target{service: cache, record: *record}
		prewarmCtx, cancel := context.WithTimeout(ctx, executorDiscoveryPrewarmTimeout)
		_, discoverErr := cache.cache.Discover(prewarmCtx, environmentID, target, &execserverclient.DiscoverV2CapabilitiesParams{
			CWD:     cwd,
			Sandbox: sandboxContexts[environmentID],
		})
		timedOut := prewarmCtx.Err() != nil && ctx.Err() == nil
		cancel()
		switch {
		case ctx.Err() != nil:
			return
		case timedOut:
			slog.Warn("executor discovery V2 prewarm timed out; continuing turn startup",
				"environment_id", environmentID,
				"timeout_ms", executorDiscoveryPrewarmTimeout.Milliseconds())
		case discoverErr != nil:
			slog.Warn("executor discovery V2 prewarm failed", "environment_id", environmentID, "error", discoverErr)
		}
	}
}
