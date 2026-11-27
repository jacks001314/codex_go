package appserver

import (
	"context"
	"fmt"
	"strings"

	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

const pendingSessionStartSourceExtraKey = "pending_session_start_source"

func threadStartSessionStartSource(params *ThreadStartParams) SessionStartSource {
	if params != nil && params.SessionStartSource != nil && *params.SessionStartSource == string(SessionStartSourceClear) {
		return SessionStartSourceClear
	}
	return SessionStartSourceStartup
}

func setThreadRecordPendingSessionStartSource(record *session.Record, source SessionStartSource) {
	if record == nil || !validPendingSessionStartSource(source) {
		return
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra[pendingSessionStartSourceExtraKey] = string(source)
}

func pendingSessionStartSourceFromRecord(record *session.Record) (SessionStartSource, bool) {
	if record == nil {
		return "", false
	}
	source, ok := sessionStartSourceFromString(stringFromMap(record.Metadata.Extra, pendingSessionStartSourceExtraKey))
	return source, ok
}

func sessionStartSourceFromString(value string) (SessionStartSource, bool) {
	switch SessionStartSource(strings.TrimSpace(value)) {
	case SessionStartSourceStartup:
		return SessionStartSourceStartup, true
	case SessionStartSourceResume:
		return SessionStartSourceResume, true
	case SessionStartSourceClear:
		return SessionStartSourceClear, true
	case SessionStartSourceCompact:
		return SessionStartSourceCompact, true
	case SessionStartSourceFork:
		return SessionStartSourceFork, true
	default:
		return "", false
	}
}

func validPendingSessionStartSource(source SessionStartSource) bool {
	_, ok := sessionStartSourceFromString(string(source))
	return ok
}

// lifecycleSubagentHookContext mirrors Rust #64310. Native thread-spawn
// children use SubagentStart/SubagentStop. Hosted workers can retain the
// host's session source while inheriting the root session ID, so that identity
// split is enough to classify them on this host-owned runtime.
func lifecycleSubagentHookContext(record *session.Record) *SubagentHookContext {
	if record == nil || senderSessionSourceIsInternal(record) {
		return nil
	}
	threadID := strings.TrimSpace(string(record.ID))
	sessionID := strings.TrimSpace(record.SessionID)
	hostedWorker := sessionID != "" && sessionID != threadID
	if !runtimeRecordIsThreadSpawn(record) && !hostedWorker {
		return nil
	}
	agentType := strings.TrimSpace(record.Metadata.AgentRole)
	if agentType == "" {
		agentType = "default"
	}
	return &SubagentHookContext{AgentID: threadID, AgentType: agentType}
}

func (r *RuntimeRouter) markThreadPendingSessionStartSource(threadID string, source SessionStartSource) error {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || !validPendingSessionStartSource(source) {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return err
	}
	setThreadRecordPendingSessionStartSource(record, source)
	return r.runtimeSaveThreadRecord(record)
}

// peekPendingSessionStartSource reads the queued SessionStart event without
// consuming it. Rust #63523 keeps the event queued until its outcome has been
// handled, so an interrupted attempt is retried by the next turn.
func (r *RuntimeRouter) peekPendingSessionStartSource(threadID string) (SessionStartSource, *session.Record, bool, error) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return "", nil, false, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return "", nil, false, err
	}
	source, ok := pendingSessionStartSourceFromRecord(record)
	if !ok {
		return "", record, false, nil
	}
	return source, record, true, nil
}

// clearPendingSessionStartSource drops a SessionStart event whose outcome was
// handled.
func (r *RuntimeRouter) clearPendingSessionStartSource(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return err
	}
	extra := cloneAnyMapForRouter(record.Metadata.Extra)
	delete(extra, pendingSessionStartSourceExtraKey)
	if len(extra) == 0 {
		extra = nil
	}
	record.Metadata.Extra = extra
	return r.runtimeSaveThreadRecord(record)
}

// runQueuedSessionStartHook runs one queued event and clears it only once the
// attempt reports a handled outcome. A retryable failure leaves the event
// queued so the next turn runs it again (Rust #63523).
func (r *RuntimeRouter) runQueuedSessionStartHook(threadID string, run func(SessionStartSource, *session.Record) (retry bool, err error)) error {
	source, record, ok, err := r.peekPendingSessionStartSource(threadID)
	if err != nil || !ok {
		return err
	}
	retry, runErr := run(source, record)
	if retry {
		return runErr
	}
	if clearErr := r.clearPendingSessionStartSource(threadID); clearErr != nil {
		if runErr != nil {
			return runErr
		}
		return clearErr
	}
	return runErr
}

func (r *RuntimeRouter) runPendingSessionStartHook(ctx context.Context, params *turn.TurnStartParams) error {
	if r == nil || params == nil {
		return nil
	}
	return r.runQueuedSessionStartHook(params.ThreadID, func(source SessionStartSource, record *session.Record) (bool, error) {
		if !r.hookRunnerConfigured() {
			return false, nil
		}
		if record == nil {
			record = &session.Record{}
		}
		cwd := firstNonEmpty(params.CWD, record.Metadata.CWD, r.services.DefaultCWD, ".")
		hooks := r.hooksForTurn(params)
		if len(hooks) == 0 {
			return false, nil
		}
		sessionID := hookSessionIdentifier(record.SessionID, params.ThreadID)
		model := firstNonEmpty(params.Model, record.Metadata.Model)
		permissionMode := r.hookPermissionModeForTurn(params)
		transcriptPath := r.sessionStartTranscriptPath(record)
		subagent := lifecycleSubagentHookContext(record)
		event := HookEventSessionStart
		matchers := []string{string(source)}
		if subagent != nil {
			event = HookEventSubagentStart
			matchers = []string{subagent.AgentType}
			if source != SessionStartSourceStartup && source != SessionStartSourceFork {
				// Internal/synthetic child sessions do not replay root lifecycle
				// hooks when resumed, compacted, or cleared.
				return false, nil
			}
		}
		if deferredLifecycleStartOnCancel(ctx, hooks, event, matchers) {
			// Rust #63523: a cancelled turn leaves an event queued when a
			// matching synchronous hook would run, so the next turn retries it.
			// Events that only match asynchronous hooks still run now.
			return true, nil
		}
		var result *HookRunResult
		var err error
		if subagent != nil {
			turnID := ""
			if active := r.threads.ActiveTurn(params.ThreadID); active != nil {
				turnID = strings.TrimSpace(active.TurnID)
			}
			result, err = r.requireHookRunner().RunSubagentStart(ctx, &HookSubagentStartRequest{
				SessionID:      sessionID,
				ThreadID:       params.ThreadID,
				TurnID:         turnID,
				CWD:            cwd,
				TranscriptPath: transcriptPath,
				Model:          model,
				PermissionMode: permissionMode,
				AgentID:        subagent.AgentID,
				AgentType:      subagent.AgentType,
				Hooks:          hooks,
			})
		} else {
			result, err = r.requireHookRunner().RunSessionStart(ctx, &HookSessionStartRequest{
				SessionID:      sessionID,
				ThreadID:       params.ThreadID,
				CWD:            cwd,
				TranscriptPath: transcriptPath,
				Model:          model,
				PermissionMode: permissionMode,
				Source:         source,
				Hooks:          hooks,
			})
		}
		if err != nil {
			// The runner failed or the turn was torn down mid-hook: keep the
			// event queued so the next turn retries it (Rust #63523).
			return true, err
		}
		if subagent == nil && result != nil && result.Stopped {
			return false, fmt.Errorf("%w: SessionStart hook stopped execution: %s", ErrInvalidHook, strings.TrimSpace(result.StopReason))
		}
		if subagent == nil && result != nil && result.Blocked {
			return false, fmt.Errorf("%w: SessionStart hook blocked execution: %s", ErrInvalidHook, strings.TrimSpace(result.BlockReason))
		}
		mergeSessionStartAdditionalContext(params, result)
		mergeSessionStartEncryptedContext(params, result)
		return false, nil
	})
}

// sessionStartHasSynchronousMatch reports whether a SessionStart event selects
// at least one synchronous hook. An unset execution mode runs synchronously,
// matching hooks/list and the runner.
func sessionStartHasSynchronousMatch(hooks []HookMetadata, source SessionStartSource) bool {
	return lifecycleStartHasSynchronousMatch(hooks, HookEventSessionStart, []string{string(source)})
}

func lifecycleStartHasSynchronousMatch(hooks []HookMetadata, event HookEventName, matchers []string) bool {
	for _, metadata := range selectHookHandlers(hooks, event, matchers) {
		if metadata.ExecutionMode != HookExecutionAsync {
			return true
		}
	}
	return false
}

// deferredSessionStartOnCancel mirrors Rust #63523's cancelled-turn rule: when
// cancellation was already requested, an event with a matching synchronous hook
// stays queued instead of running, while asynchronous-only matches still run.
func deferredSessionStartOnCancel(ctx context.Context, hooks []HookMetadata, source SessionStartSource) bool {
	return deferredLifecycleStartOnCancel(ctx, hooks, HookEventSessionStart, []string{string(source)})
}

func deferredLifecycleStartOnCancel(ctx context.Context, hooks []HookMetadata, event HookEventName, matchers []string) bool {
	if ctx == nil || ctx.Err() == nil {
		return false
	}
	return lifecycleStartHasSynchronousMatch(hooks, event, matchers)
}

// mergeSessionStartEncryptedContext hands the ciphertext a synchronous MCP
// SessionStart hook returned to the turn, which sends it to the model as
// standalone encrypted function_call_output items (Rust #63474).
func mergeSessionStartEncryptedContext(params *turn.TurnStartParams, result *HookRunResult) {
	if params == nil || result == nil || len(result.EncryptedContexts) == 0 {
		return
	}
	params.HookEncryptedContexts = append(params.HookEncryptedContexts, result.EncryptedContexts...)
}

func (r *RuntimeRouter) sessionStartTranscriptPath(record *session.Record) *string {
	if r == nil || r.services.ThreadRouter == nil || record == nil || runtimeRecordEphemeral(record) {
		return nil
	}
	path := strings.TrimSpace(r.services.ThreadRouter.threadRolloutPath(record))
	if path == "" {
		return nil
	}
	return &path
}

// hookPermissionMode mirrors Rust's hook_runtime::hook_permission_mode: only a
// never-approval policy maps to bypassPermissions; every other policy
// (unless-trusted, on-request, granular) reports the default mode.
func hookPermissionMode(policy sandbox.AskForApproval) string {
	if policy == sandbox.ApprovalNever {
		return "bypassPermissions"
	}
	return "default"
}

// hookPermissionModeForTurn resolves the turn's effective approval policy
// (turn/start override first, then the loaded config) and maps it the way Rust
// reports permission mode to hooks.
func (r *RuntimeRouter) hookPermissionModeForTurn(params *turn.TurnStartParams) string {
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		cfg = nil
	}
	return hookPermissionMode(turnApprovalPolicyForTurn(cfg, params))
}

// hookTurnAttribution resolves the model and permission mode a hook reports for
// a turn: the captured settings that issued the tool call (Rust
// `step_context.settings` in run_pre_tool_use_hooks / run_post_tool_use_hooks).
// Go has no per-step settings capture yet, so the turn's effective settings are
// used and a mid-turn settings update lands on the next turn.
func (r *RuntimeRouter) hookTurnAttribution(params *turn.TurnStartParams) (string, string) {
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		cfg = nil
	}
	model := firstNonEmpty(turnParamModel(params), stringConfigValue(cfg, "model"), defaultModelForAppTurn())
	return model, hookPermissionMode(turnApprovalPolicyForTurn(cfg, params))
}

func mergeSessionStartAdditionalContext(params *turn.TurnStartParams, result *HookRunResult) {
	if params == nil || result == nil || len(result.AdditionalContexts) == 0 {
		return
	}
	if params.AdditionalContext == nil {
		params.AdditionalContext = map[string]turn.AdditionalContextEntry{}
	}
	for _, text := range result.AdditionalContexts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		key := nextSessionStartAdditionalContextKey(params.AdditionalContext)
		params.AdditionalContext[key] = turn.AdditionalContextEntry{
			Value: text,
			Kind:  turn.AdditionalContextApplication,
		}
	}
}

func nextSessionStartAdditionalContextKey(values map[string]turn.AdditionalContextEntry) string {
	const base = "sessionStartHook"
	if _, ok := values[base]; !ok {
		return base
	}
	for i := 2; ; i++ {
		key := fmt.Sprintf("%s%d", base, i)
		if _, ok := values[key]; !ok {
			return key
		}
	}
}
