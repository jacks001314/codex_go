package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/internal/compact"
	"codex_go/internal/session"
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
	if r == nil || request == nil || r.services.HookRunner == nil {
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
	ctx.Hooks = r.compactHooksForCWD(ctx.CWD)
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

func (r *RuntimeRouter) compactHooksForCWD(cwd string) []HookMetadata {
	return r.hooksForCWD(cwd)
}

func (r *RuntimeRouter) hooksForCWD(cwd string) []HookMetadata {
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
	discovery := r.configureHookDiscovery()
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
