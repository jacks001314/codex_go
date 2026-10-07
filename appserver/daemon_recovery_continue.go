package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"time"

	contextfrag "codex_go/context"
	"codex_go/daemonrecovery"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// Managed daemon recovery: the post-restore continuation turn.
//
// Rust app-server/src/request_processors/daemon_continuation.rs:23
// `continue_daemon_turn(thread_id, saved)` runs immediately after a saved
// thread is restored (the `ThreadResumeTarget::DaemonRecovery` match at
// thread_processor.rs:3690, call :3696, for an already-loaded thread; :4015,
// call :4016, right after a cold resume). It re-reads the interrupted turn's rollout,
// re-checks that the work is still unfinished, and starts exactly one new turn
// carrying a hidden recovery fragment, the saved output schema/service
// tier/cyber program, and the saved root turn id. The snapshot the previous
// generation wrote may be stale: the old process could have completed or
// replaced its turn during the shutdown grace period, so every gate below
// fails closed.
//
// Structural differences from Rust (recorded with the Phase C handoff):
//   - Rust compares the saved turn's full `PermissionProfile` with the thread's
//     current `config.permission_profile`. Go's turn context persists the
//     sandbox-policy tag (and reads a Rust-written `permission_profile` when
//     present), so the comparison uses that tag. It is coarser: it captures the
//     sandbox mode and whether the profile grants network/full disk access, but
//     not explicit deny globs or external enforcement. See
//     daemonRecoverySandboxTag.
//   - Rust's `continue_turn_if_idle` performs an atomic "expected previous turn
//     id" check against `SessionState::last_started_turn_id`. Go keeps no
//     retained last-started-turn id, so the equivalent guard is the rollout
//     gates 1-2 plus the idle reservation (`ThreadManager::ReserveTurn` rejects
//     a thread that already has an active turn).
//   - Rust appends the `TurnAborted` rollout item through the thread's own
//     writer; Go writes it through the shared rollout recorder
//     (`withRuntimeRollout`), which flushes before the continuation turn starts.

const (
	// daemonRecoveryTurnTrigger mirrors Rust's `turn_trigger` for a recovered
	// turn (daemon_continuation.rs:94).
	daemonRecoveryTurnTrigger = "daemon_recovery"
	// daemonRecoveryContinuationBody is the hidden context Rust injects into the
	// model input before continuing the unfinished work (daemon_continuation.rs:90).
	daemonRecoveryContinuationBody = "The server restarted and interrupted the previous turn. Continue the unfinished work from the saved conversation. Check the current state before repeating actions that may already have completed."
	// daemonRecoveryResumeWarning is the thread warning Rust emits once the
	// continuation starts (daemon_continuation.rs:126).
	daemonRecoveryResumeWarning = "Resuming interrupted work"
)

// continueDaemonTurn mirrors Rust `continue_daemon_turn`
// (app-server/src/request_processors/daemon_continuation.rs:23). It is
// best-effort: every gate that cannot be satisfied returns silently, because a
// stale snapshot must never restart finished or re-authorized work.
func (r *RuntimeRouter) continueDaemonTurn(threadID string, saved daemonrecovery.InterruptedTurn) {
	if r == nil || r.threads == nil || r.threads.IsClosing() {
		return
	}
	threadID = strings.TrimSpace(threadID)
	savedTurnID := strings.TrimSpace(saved.TurnID)
	if threadID == "" || savedTurnID == "" {
		return
	}
	// Gate 0: the thread is loaded, has a rollout, and that rollout is readable
	// (Rust daemon_continuation.rs:27 `get_thread`, :30 `rollout_path`, :33
	// `load_rollout_items`). A read failure only warns.
	if !r.threads.HasLiveThread(session.ThreadID(threadID)) {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return
	}
	rolloutPath := ""
	if r.services.ThreadRouter != nil {
		rolloutPath = r.services.ThreadRouter.threadRolloutPath(record)
	}
	if rolloutPath == "" {
		return
	}
	lines, _, err := rollout.Load(rolloutPath)
	if err != nil {
		slog.Warn("failed to read interrupted turn history", "thread_id", threadID, "error", err)
		return
	}
	// Gate 1: the rollout's newest `TurnStarted` (Rust daemon_continuation.rs:42).
	start := -1
	var startEvent daemonRecoveryRolloutEvent
	for i := len(lines) - 1; i >= 0; i-- {
		event, kind, ok := daemonRecoveryDecodeEvent(&lines[i])
		if !ok || kind != "turn_started" {
			continue
		}
		start = i
		startEvent = event
		break
	}
	if start < 0 {
		return
	}
	// Gate 2: that newest started turn is still the saved one and nothing after
	// it completed or aborted it, i.e. the work is neither finished nor
	// superseded (Rust daemon_continuation.rs:48-62).
	if daemonRecoveryEventTurnID(startEvent) != savedTurnID {
		return
	}
	for i := start; i < len(lines); i++ {
		event, kind, ok := daemonRecoveryDecodeEvent(&lines[i])
		if !ok {
			continue
		}
		switch kind {
		case "turn_complete":
			if daemonRecoveryEventTurnID(event) == savedTurnID {
				return
			}
		case "turn_aborted":
			if id := daemonRecoveryEventTurnID(event); id == "" || id == savedTurnID {
				return
			}
		}
	}
	// Gate 3: the saved turn's context record is the model-visible baseline the
	// continuation restores (Rust daemon_continuation.rs:63-73).
	previous := daemonRecoveryLastTurnContext(lines, savedTurnID)
	if previous == nil {
		return
	}
	// Gate 4: the thread's single environment is the thread-owned local one and
	// matches the identity the previous generation saved (Rust
	// daemon_continuation.rs:74-83). A snapshot without that identity is never
	// continued.
	currentEnvironment, ok := r.daemonRecoveryCurrentThreadEnvironment(record)
	if !ok {
		return
	}
	if !daemonRecoverySavedEnvironmentMatches(saved.LocalEnvironment, currentEnvironment) {
		return
	}
	// Gate 5: recovery must not override a stricter saved or newly configured
	// policy (Rust daemon_continuation.rs:84-87).
	previousTag := daemonRecoverySandboxTagFromPolicy(previous.SandboxPolicy)
	currentTag := r.daemonRecoveryCurrentSandboxTag(record)
	if previousTag == "" || currentTag == "" || previousTag != currentTag {
		return
	}
	// The saved turn is closed in persisted history before a new running turn is
	// exposed (Rust daemon_continuation.rs:102-119). A write failure aborts the
	// continuation.
	if err := r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return recorder.AppendTurnAborted(savedTurnID, "interrupted", time.Now().UTC(), 0)
	}); err != nil {
		slog.Warn("failed to close interrupted turn history", "thread_id", threadID, "error", err)
		return
	}
	// Continue only when the thread is idle. Rust performs this inside
	// `continue_turn_if_idle`; Go's idle guard is the turn reservation taken by
	// the continuation start below.
	params := daemonRecoveryContinuationParams(threadID, previous, saved)
	if err := r.startDaemonRecoveryContinuationTurn(params); err != nil {
		slog.Debug("recovery continuation was not started", "thread_id", threadID, "error", err)
		return
	}
	r.notify(NotificationWarning, &WarningNotification{
		ThreadID: stringPtrIfNotEmpty(threadID),
		Message:  daemonRecoveryResumeWarning,
	})
}

// daemonRecoveryContinuationParams builds the continuation turn's start params
// (Rust daemon_continuation.rs:88-101): a hidden recovery fragment instead of a
// user message, no new authorization, and the saved output schema, service tier,
// cyber program and root turn id.
func daemonRecoveryContinuationParams(threadID string, previous *rollout.TurnContextRecord, saved daemonrecovery.InterruptedTurn) *turn.TurnStartParams {
	params := &turn.TurnStartParams{
		ThreadID:    threadID,
		TurnTrigger: daemonRecoveryTurnTrigger,
		RootTurnID:  strings.TrimSpace(previous.RootTurnID),
	}
	if item := daemonRecoveryContinuationInputItem(); item != nil {
		params.AdditionalInputItems = []any{item}
	}
	if len(saved.OutputSchema) > 0 {
		var schema any
		if err := json.Unmarshal(saved.OutputSchema, &schema); err == nil {
			params.OutputSchema = schema
		}
	}
	if saved.ServiceTier != nil {
		tier := strings.TrimSpace(*saved.ServiceTier)
		params.ServiceTier = &tier
	}
	if len(saved.CyberAccessProgram) > 0 {
		var program string
		if err := json.Unmarshal(saved.CyberAccessProgram, &program); err == nil {
			params.CoreCyberAccessProgram = strings.TrimSpace(program)
		}
	}
	return params
}

// daemonRecoveryContinuationInputItem is the model-visible recovery fragment
// Rust injects as `InternalModelContextFragment::new("daemon_recovery", …)`
// (core/src/context/internal_model_context.rs): a user-role contextual message
// wrapped in the internal-context markers, which keeps it out of the user-turn
// boundary set that rollback and reconstruction use.
func daemonRecoveryContinuationInputItem() map[string]any {
	rendered := contextfrag.Render(contextfrag.NewSimpleFragmentWithKind(
		contextfrag.RoleUser,
		`<codex_internal_context source="daemon_recovery">`,
		"</codex_internal_context>",
		daemonRecoveryContinuationBody,
		"daemon_recovery.internal_context",
	))
	if rendered == nil || strings.TrimSpace(rendered.Content) == "" {
		return nil
	}
	return map[string]any{
		"type": "message",
		"role": "user",
		"content": []map[string]any{{
			"type": "input_text",
			"text": rendered.Content,
		}},
	}
}

// startDaemonRecoveryContinuationTurn starts the automatic continuation turn.
// It follows the same internal start shape as the goal continuation
// (startGoalContinuationTurn), minus the goal bookkeeping: the thread's
// environment selection is inherited, the turn is validated, and the runtime
// reserves the thread so a thread with an active turn is never continued.
func (r *RuntimeRouter) startDaemonRecoveryContinuationTurn(params *turn.TurnStartParams) error {
	if r == nil || params == nil {
		return errors.New("daemon recovery continuation params are required")
	}
	r.inheritTurnEnvironmentSelections(params)
	if err := r.prepareTurnStartParams(params); err != nil {
		return err
	}
	if err := r.validateTurnStartEnvironments(params); err != nil {
		return err
	}
	if err := params.Validate(); err != nil {
		return err
	}
	if err := r.runPendingSessionStartHook(context.Background(), params); err != nil {
		return err
	}
	// Rust #49262: a recovered turn is traced on `codex.turn_input` too, which
	// records the turn id once the recovery has started a turn.
	inputSpan := r.startTurnInputSpan(nil, params.ThreadID)
	acceptedTurnID := ""
	defer func() { endTurnInputSpan(inputSpan, acceptedTurnID) }()
	reservedRuntime := false
	if r.hasRuntimeThreadStore() {
		if err := r.reserveRuntimeThread(params.ThreadID); err != nil {
			return err
		}
		reservedRuntime = true
	}
	response, err := r.requireTurns().Start(params)
	if err != nil {
		if reservedRuntime {
			r.clearActiveRuntimeTurn(params.ThreadID, "")
		}
		return err
	}
	acceptedTurnID = response.Turn.ID
	_ = r.persistTurnStartRuntimeWorkspaceRoots(params)
	_ = r.persistTurnEnvironmentSelections(params)
	r.startTurnRuntimeAsync(params, response, "", nil)
	return nil
}

// daemonRecoveryRolloutEvent is the decoded subset of a turn-lifecycle event the
// continuation gates observe.
type daemonRecoveryRolloutEvent struct {
	Type        string `json:"type"`
	TurnID      string `json:"turn_id"`
	TurnIDCamel string `json:"turnId"`
}

// daemonRecoveryDecodeEvent decodes a `turn_started`/`turn_complete`/
// `turn_aborted` event from one rollout line. Other event types are reported as
// not-ok so callers skip them.
func daemonRecoveryDecodeEvent(line *rollout.Line) (daemonRecoveryRolloutEvent, string, bool) {
	if line == nil || line.Type != "event_msg" || len(line.Payload) == 0 {
		return daemonRecoveryRolloutEvent{}, "", false
	}
	var event daemonRecoveryRolloutEvent
	if err := json.Unmarshal(line.Payload, &event); err != nil {
		return daemonRecoveryRolloutEvent{}, "", false
	}
	kind := daemonRecoveryNormalizeEventType(event.Type)
	if kind == "" {
		return daemonRecoveryRolloutEvent{}, "", false
	}
	return event, kind, true
}

// daemonRecoveryNormalizeEventType maps a rollout event's wire type to the
// normalized lifecycle name, mirroring rollout.normalizeRolloutEventType for the
// three turn events the gates read.
func daemonRecoveryNormalizeEventType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "task_started", "turn_started", "turnstarted":
		return "turn_started"
	case "task_complete", "turn_complete", "turncomplete":
		return "turn_complete"
	case "turn_aborted", "turnaborted":
		return "turn_aborted"
	default:
		return ""
	}
}

func daemonRecoveryEventTurnID(event daemonRecoveryRolloutEvent) string {
	return firstNonEmpty(strings.TrimSpace(event.TurnID), strings.TrimSpace(event.TurnIDCamel))
}

// daemonRecoveryLastTurnContext returns the newest turn-context record that
// names turnID, or nil (Rust daemon_continuation.rs:63-73).
func daemonRecoveryLastTurnContext(lines []rollout.Line, turnID string) *rollout.TurnContextRecord {
	turnID = strings.TrimSpace(turnID)
	for i := len(lines) - 1; i >= 0; i-- {
		if len(lines[i].TurnContext) == 0 {
			continue
		}
		if daemonRecoveryTurnContextTurnID(lines[i].TurnContext) != turnID {
			continue
		}
		var record rollout.TurnContextRecord
		if err := json.Unmarshal(lines[i].TurnContext, &record); err != nil {
			continue
		}
		return &record
	}
	return nil
}

func daemonRecoveryTurnContextTurnID(raw json.RawMessage) string {
	var values map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(stringFromAny(values["turn_id"]), stringFromAny(values["turnId"])))
}

// daemonRecoverySavedEnvironmentMatches reports whether the saved environment
// identity equals the restored one. An absent saved identity (a legacy snapshot)
// never matches, matching Rust's `saved.local_environment.as_ref() != Some(..)`.
func daemonRecoverySavedEnvironmentMatches(saved json.RawMessage, current daemonRecoveryThreadEnvironment) bool {
	if len(saved) == 0 {
		return false
	}
	var decoded daemonRecoveryThreadEnvironment
	if err := json.Unmarshal(saved, &decoded); err != nil {
		return false
	}
	decoded.RuntimeWorkspaceRoots = daemonRecoveryTrimmedStrings(decoded.RuntimeWorkspaceRoots)
	decoded.CWD = strings.TrimSpace(decoded.CWD)
	decoded.EnvironmentID = strings.TrimSpace(decoded.EnvironmentID)
	return reflect.DeepEqual(decoded, current)
}

func daemonRecoveryTrimmedStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// daemonRecoveryCurrentThreadEnvironment re-derives the single thread-owned local
// environment exactly as the daemon snapshot captured it: the persisted
// environment selection when the thread has one (it must be the local one,
// thread-owned), otherwise the implicit local environment a selection-less
// thread resolves to (Rust EnvironmentManager::default_environment). The snapshot
// and this gate therefore compute the same identity, so an equal comparison
// proves the restored environment is the saved one (Rust
// daemon_continuation.rs:74-83).
func (r *RuntimeRouter) daemonRecoveryCurrentThreadEnvironment(record *session.Record) (daemonRecoveryThreadEnvironment, bool) {
	if record == nil {
		return daemonRecoveryThreadEnvironment{}, false
	}
	return r.daemonRecoveryLocalEnvironment(&turn.TurnStartParams{
		ThreadID: recordThreadID(record),
		CWD:      strings.TrimSpace(record.Metadata.CWD),
	}, record)
}

// daemonRecoverySandboxTagFromPolicy normalizes a persisted turn-context sandbox
// policy into the canonical tag the continuation gate compares. Go writes the
// tag string produced by analyticsSandboxPolicy; a Rust-written rollout records
// a structured SandboxPolicy object, which is decoded through the shared
// turn-sandbox parser.
func daemonRecoverySandboxTagFromPolicy(raw any) string {
	if raw == nil {
		return ""
	}
	if tag := normalizeDaemonRecoverySandboxTag(stringFromAny(raw)); tag != "" {
		return tag
	}
	if policy, err := parseTurnSandboxPolicy(raw); err == nil && policy != nil {
		return normalizeDaemonRecoverySandboxTag(string(policy.Kind))
	}
	return ""
}

// daemonRecoveryCurrentSandboxTag resolves the thread's currently effective
// sandbox policy tag: the persisted thread setting when present, otherwise the
// live configuration's resolved profile. Both go through the same normalization
// as the saved side.
func (r *RuntimeRouter) daemonRecoveryCurrentSandboxTag(record *session.Record) string {
	if tag := normalizeDaemonRecoverySandboxTag(metadataSandboxPolicy(record)); tag != "" {
		return tag
	}
	if r == nil {
		return ""
	}
	cwd := ""
	if record != nil {
		cwd = strings.TrimSpace(record.Metadata.CWD)
	}
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: recordThreadID(record), CWD: cwd})
	if err != nil || cfg == nil {
		return ""
	}
	resolution, err := threadDefaultSandboxPermissionProfile(cfg, cwd, nil)
	if err != nil || resolution == nil {
		return ""
	}
	return normalizeDaemonRecoverySandboxTag(analyticsSandboxPolicy(resolution, cwd))
}

func metadataSandboxPolicy(record *session.Record) string {
	if record == nil {
		return ""
	}
	return strings.TrimSpace(record.Metadata.SandboxPolicy)
}

func recordThreadID(record *session.Record) string {
	if record == nil {
		return ""
	}
	return strings.TrimSpace(string(record.ID))
}

// normalizeDaemonRecoverySandboxTag maps both the analytics tag vocabulary
// (read_only/workspace_write/full_access/external_sandbox) and the SandboxMode
// vocabulary (read-only/workspace-write/danger-full-access) onto one canonical
// spelling.
func normalizeDaemonRecoverySandboxTag(value string) string {
	tag := strings.ToLower(strings.TrimSpace(value))
	tag = strings.ReplaceAll(tag, "-", "_")
	switch tag {
	case "read_only", "readonly":
		return "read_only"
	case "workspace_write":
		return "workspace_write"
	case "full_access", "danger_full_access":
		return "full_access"
	case "external_sandbox", "external":
		return "external_sandbox"
	default:
		return ""
	}
}
