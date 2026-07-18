package appserver

import (
	"context"
	"fmt"
	"strings"

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
	default:
		return "", false
	}
}

func validPendingSessionStartSource(source SessionStartSource) bool {
	_, ok := sessionStartSourceFromString(string(source))
	return ok
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

func (r *RuntimeRouter) consumePendingSessionStartSource(threadID string) (SessionStartSource, *session.Record, bool, error) {
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
	extra := cloneAnyMapForRouter(record.Metadata.Extra)
	delete(extra, pendingSessionStartSourceExtraKey)
	if len(extra) == 0 {
		extra = nil
	}
	record.Metadata.Extra = extra
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return "", nil, false, err
	}
	return source, record, true, nil
}

func (r *RuntimeRouter) runPendingSessionStartHook(ctx context.Context, params *turn.TurnStartParams) error {
	if r == nil || params == nil {
		return nil
	}
	source, record, ok, err := r.consumePendingSessionStartSource(params.ThreadID)
	if err != nil || !ok {
		return err
	}
	if !r.hookRunnerConfigured() {
		return nil
	}
	cwd := firstNonEmpty(params.CWD, record.Metadata.CWD, r.services.DefaultCWD, ".")
	hooks := r.hooksForCWD(cwd)
	if len(hooks) == 0 {
		return nil
	}
	result, err := r.requireHookRunner().RunSessionStart(ctx, &HookSessionStartRequest{
		ThreadID:       params.ThreadID,
		CWD:            cwd,
		TranscriptPath: r.sessionStartTranscriptPath(record),
		Model:          firstNonEmpty(params.Model, record.Metadata.Model),
		PermissionMode: hookPermissionModeFromTurnStart(params),
		Source:         source,
		Hooks:          hooks,
	})
	if err != nil {
		return err
	}
	if result != nil && result.Stopped {
		return fmt.Errorf("%w: SessionStart hook stopped execution: %s", ErrInvalidHook, strings.TrimSpace(result.StopReason))
	}
	if result != nil && result.Blocked {
		return fmt.Errorf("%w: SessionStart hook blocked execution: %s", ErrInvalidHook, strings.TrimSpace(result.BlockReason))
	}
	mergeSessionStartAdditionalContext(params, result)
	return nil
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

func hookPermissionModeFromTurnStart(params *turn.TurnStartParams) string {
	if params == nil {
		return "default"
	}
	if value, ok := params.ApprovalPolicy.(string); ok && strings.EqualFold(strings.TrimSpace(value), "never") {
		return "bypassPermissions"
	}
	return "default"
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
