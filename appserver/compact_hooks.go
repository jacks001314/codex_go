package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/compact"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

var ErrCompactHookStopped = errors.New("compact hook stopped")

type compactHookContext struct {
	ThreadID string
	TurnID   string
	CWD      string
	Model    string
	Trigger  compact.Trigger
	Hooks    []HookMetadata
}

func (r *RuntimeRouter) compactHookContext(record *session.Record, request *compact.Request) *compactHookContext {
	if r == nil || request == nil || !r.hookRunnerConfigured() {
		return nil
	}
	ctx := &compactHookContext{
		ThreadID: request.ThreadID,
		TurnID:   request.TurnID,
		Trigger:  request.Trigger,
	}
	if record != nil {
		ctx.CWD = record.Metadata.CWD
		ctx.Model = record.Metadata.Model
	}
	ctx.Hooks = r.compactHooksForCWD(ctx.CWD, ctx.ThreadID)
	if len(ctx.Hooks) == 0 {
		return nil
	}
	return ctx
}

func (r *RuntimeRouter) runPreCompactHooks(ctx context.Context, hookCtx *compactHookContext) ([]compact.Item, error) {
	if hookCtx == nil || len(hookCtx.Hooks) == 0 {
		return nil, nil
	}
	result, err := r.requireHookRunner().RunPreCompact(ctx, &HookPreCompactRequest{
		ThreadID: hookCtx.ThreadID,
		TurnID:   hookCtx.TurnID,
		CWD:      firstNonEmpty(hookCtx.CWD, r.services.DefaultCWD, "."),
		Model:    hookCtx.Model,
		Trigger:  string(hookCtx.Trigger),
		Hooks:    hookCtx.Hooks,
	})
	if err != nil {
		return nil, err
	}
	if result != nil && result.Stopped {
		return nil, fmt.Errorf("%w: %w: PreCompact hook stopped execution: %s", ErrInvalidHook, ErrCompactHookStopped, strings.TrimSpace(result.StopReason))
	}
	return compactInitialContextFromHookResult(result, time.Now().UTC()), nil
}

func (r *RuntimeRouter) runPostCompactHooks(ctx context.Context, hookCtx *compactHookContext) error {
	if hookCtx == nil || len(hookCtx.Hooks) == 0 {
		return nil
	}
	result, err := r.requireHookRunner().RunPostCompact(ctx, &HookPostCompactRequest{
		ThreadID: hookCtx.ThreadID,
		TurnID:   hookCtx.TurnID,
		CWD:      firstNonEmpty(hookCtx.CWD, r.services.DefaultCWD, "."),
		Model:    hookCtx.Model,
		Trigger:  string(hookCtx.Trigger),
		Hooks:    hookCtx.Hooks,
	})
	if err != nil {
		return err
	}
	if result != nil && result.Stopped {
		return fmt.Errorf("%w: %w: PostCompact hook stopped execution: %s", ErrInvalidHook, ErrCompactHookStopped, strings.TrimSpace(result.StopReason))
	}
	return nil
}

func (r *RuntimeRouter) compactHooksForCWD(cwd string, threadID string) []HookMetadata {
	return r.hooksForThreadTurn(threadID, cwd)
}

// hooksForTurn returns the turn's hook inventory, including hooks declared by
// its ready execution environments (Rust with_environment_hooks, #56304).
func (r *RuntimeRouter) hooksForTurn(params *turn.TurnStartParams) []HookMetadata {
	if r == nil || params == nil {
		return nil
	}
	hooks := r.hooksForCWD(params.CWD, params.ThreadID)
	// Rust's hooks engine is disabled wholesale when the hooks feature is off,
	// so environment declarations stay dormant too.
	if !r.configureHookDiscoveryForThread(params.ThreadID).hooksFeatureEnabled(params.CWD) {
		return hooks
	}
	sources, remote := r.environmentHookSourcesForTurn(params)
	if len(sources) == 0 {
		return hooks
	}
	discovered := discoverEnvironmentHooks(sources, nextHookDisplayOrder(hooks), environmentHookExecutors(remote))
	return append(hooks, discovered.Handlers...)
}

// hooksForThreadTurn is hooksForTurn for callers that only know the thread. The
// active turn's environment selections are the only environment snapshot this
// process retains, so a thread with no running turn falls back to local hooks.
func (r *RuntimeRouter) hooksForThreadTurn(threadID string, cwd string) []HookMetadata {
	if r == nil {
		return nil
	}
	params := r.activeTurnParams(threadID)
	if params == nil {
		return r.hooksForCWD(cwd, threadID)
	}
	if strings.TrimSpace(params.CWD) == "" {
		params.CWD = cwd
	}
	if strings.TrimSpace(params.ThreadID) == "" {
		params.ThreadID = threadID
	}
	return r.hooksForTurn(params)
}

func nextHookDisplayOrder(hooks []HookMetadata) int64 {
	order := int64(0)
	for _, hook := range hooks {
		if hook.DisplayOrder >= order {
			order = hook.DisplayOrder + 1
		}
	}
	return order
}

func nextHookDisplayOrderFromEntries(entries []HookListEntry) int64 {
	order := int64(0)
	for _, entry := range entries {
		for _, hook := range entry.Hooks {
			if hook.DisplayOrder >= order {
				order = hook.DisplayOrder + 1
			}
		}
	}
	return order
}

// environmentHookExecutors builds the executor map passed to environment hook
// discovery. Local environments are absent, so their handlers keep the local
// command runner.
func environmentHookExecutors(remote map[string]tool.UnifiedExecEnvironment) map[string]HookEnvironmentExecutor {
	if len(remote) == 0 {
		return nil
	}
	executor := newAppServerHookEnvironmentExecutor(remote)
	if executor == nil {
		return nil
	}
	executors := make(map[string]HookEnvironmentExecutor, len(remote))
	for environmentID := range remote {
		executors[environmentID] = executor
	}
	return executors
}

func (r *RuntimeRouter) hooksForCWD(cwd string, threadID string) []HookMetadata {
	if r == nil {
		return nil
	}
	hooks := []HookMetadata{}
	bypassHookTrust := r.bypassHookTrustFromConfig()
	if r.services.Hooks != nil {
		list := r.services.Hooks.List(&HookListParams{CWDs: []string{cwd}})
		if list != nil {
			for i := range list.Data {
				for j := range list.Data[i].Hooks {
					hook := list.Data[i].Hooks[j]
					hook.BypassTrust = hook.BypassTrust || bypassHookTrust
					hooks = append(hooks, hook)
				}
			}
		}
	}
	discovery := r.configureHookDiscoveryForThread(threadID)
	discovered := discovery.Discover(&HookListParams{CWDs: []string{cwd}}, r.services.DefaultCWD)
	if discovered != nil {
		for i := range discovered.Data {
			hooks = append(hooks, discovered.Data[i].Hooks...)
		}
	}
	return hooks
}

func compactInitialContextFromHookResult(result *HookRunResult, now time.Time) []compact.Item {
	if result == nil || len(result.AdditionalContexts) == 0 {
		return nil
	}
	items := make([]compact.Item, 0, len(result.AdditionalContexts))
	for i, text := range result.AdditionalContexts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		items = append(items, compact.Item{
			ID:      fmt.Sprintf("pre-compact-hook-context-%d", i+1),
			Type:    "message",
			Role:    "developer",
			Kind:    "hook_prompt",
			Text:    text,
			Created: now,
		})
	}
	return items
}
