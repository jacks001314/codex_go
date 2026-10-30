package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/features"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// Direct Guardian review protocol types (Rust #58119). `thread/guardianReview`
// reviews an idle thread's final injected exec_command without executing it and
// returns Guardian's structured assessment, or a null assessment when exec
// policy skips approval.

type ThreadGuardianReviewParams struct {
	ThreadID string `json:"threadId"`
}

func (p ThreadGuardianReviewParams) Validate() error {
	if strings.TrimSpace(p.ThreadID) == "" {
		return jsonRPCInvalidRequest("threadId is required")
	}
	return nil
}

type ThreadGuardianReviewResponse struct {
	CallID string `json:"callId"`
	// Assessment is null when exec policy skips approval.
	Assessment *ThreadGuardianAssessment `json:"assessment"`
}

type ThreadGuardianAssessment struct {
	Outcome           state.Outcome           `json:"outcome"`
	RiskLevel         state.RiskLevel         `json:"riskLevel"`
	UserAuthorization state.UserAuthorization `json:"userAuthorization"`
	Rationale         string                  `json:"rationale"`
}

// guardianRecordedExecCall is the recorded exec_command a direct review may
// assess (Rust's RecordedExecCall::from_item).
type guardianRecordedExecCall struct {
	CallID    string
	Arguments string
}

const guardianRecordedExecCallRequirement = "expected a completed plaintext exec_command call with a nonempty call ID"

func (r *RuntimeRouter) handleThreadGuardianReviewRuntime(request *Request) (*ThreadGuardianReviewResponse, error) {
	if r == nil {
		return nil, invalidParams("thread/guardianReview is unavailable")
	}
	var params ThreadGuardianReviewParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	// Rust #58119 cancels an in-flight review when the requesting connection
	// disconnects so the thread is released promptly.
	reviewCtx, unregister := r.registerRequestCancellation(request)
	defer unregister()
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, session.ErrThreadNotFound
	}
	if r.threads != nil && r.threads.ActiveTurn(threadID) != nil {
		return nil, invalidParams("thread/guardianReview requires an idle thread")
	}
	// Rust's ensure_direct_input_allowed: a v2 subagent thread never accepts
	// direct input, so it cannot be reviewed through this entry point either.
	if err := r.ensureDirectInputAllowed(request, threadID); err != nil {
		return nil, err
	}
	// Rust checks the retained provider before input reaches the thread, so a
	// provider switch cannot silently retarget the review.
	if err := r.checkThreadModelProviderForID(threadID); err != nil {
		return nil, err
	}
	// The review subscribes the requesting connection to the thread; the
	// subscription outlives the review (Rust #58119).
	r.subscribeThreadConnection(threadID, request.ConnectionID)
	call, err := guardianRecordedExecCallFromItems(record.Items)
	if err != nil {
		return nil, err
	}
	if guardianHistoryReplaysRequestPermissions(record.Items) {
		return nil, invalidParams("direct review does not support replaying request_permissions")
	}
	turnID := newMemoryTurnID()
	prepared, err := r.prepareRecordedExecCommand(reviewCtx, threadID, turnID, record, call)
	if err != nil {
		return nil, err
	}
	response := &ThreadGuardianReviewResponse{CallID: call.CallID}
	if !prepared.Request.ApprovalRequired {
		// Exec policy explicitly allows the command: nothing to assess, and
		// nothing is executed.
		return response, nil
	}
	reviewer := r.guardianReviewerForRequest()
	if reviewer == nil {
		return nil, errors.New("Guardian review failed: automatic approval review is unavailable")
	}
	assessmentReviewer, ok := reviewer.(GuardianAssessmentReviewer)
	if !ok {
		return nil, errors.New("Guardian review failed: the reviewer cannot report an assessment")
	}
	action := commandApprovalAction(prepared.Request)
	decision, assessment, reason, err := assessmentReviewer.ReviewAssessment(
		withGuardianOmittedCall(reviewCtx, call.CallID), threadID, turnID, call.CallID, action)
	if err != nil {
		return nil, errors.New("Guardian review failed: " + err.Error())
	}
	switch decision {
	case state.DecisionAborted:
		return nil, errors.New("Guardian review failed: the review was cancelled")
	case state.DecisionTimedOut:
		return nil, errors.New("Guardian review failed: the review timed out")
	}
	response.Assessment = threadGuardianAssessment(decision, assessment, reason)
	return response, nil
}

// guardianReviewerForRequest resolves the reviewer an approval review would use,
// creating the model reviewer on demand.
func (r *RuntimeRouter) guardianReviewerForRequest() GuardianReviewer {
	if r == nil {
		return nil
	}
	reviewer := r.services.GuardianReviewer
	if reviewer == nil && r.services.Agent != nil {
		reviewer = r.ensureGuardianReviewer(r.services.Agent)
	}
	return reviewer
}

// threadGuardianAssessment converts the reviewer's assessment, filling the
// fields a fast or failed decision never produced (Rust's
// parse_guardian_assessment defaults).
func threadGuardianAssessment(decision state.ReviewDecision, assessment *state.Assessment, reason string) *ThreadGuardianAssessment {
	if assessment != nil {
		return &ThreadGuardianAssessment{
			Outcome:           assessment.Outcome,
			RiskLevel:         assessment.RiskLevel,
			UserAuthorization: assessment.UserAuthorization,
			Rationale:         assessment.Rationale,
		}
	}
	out := state.OutcomeAllow
	risk := state.RiskLow
	rationale := strings.TrimSpace(reason)
	if decision != state.DecisionApproved {
		out = state.OutcomeDeny
		risk = state.RiskHigh
	}
	if rationale == "" {
		if out == state.OutcomeAllow {
			rationale = "Auto-review returned a low-risk allow decision."
		} else {
			rationale = "Auto-review returned a deny decision without a rationale."
		}
	}
	return &ThreadGuardianAssessment{
		Outcome:           out,
		RiskLevel:         risk,
		UserAuthorization: state.AuthorizationUnknown,
		Rationale:         rationale,
	}
}

// guardianRecordedExecCallFromItems reads the recorded call from the thread's
// final item (Rust's RecordedExecCall::from_item). A trailing tool output or any
// other item invalidates the request.
func guardianRecordedExecCallFromItems(items []session.Item) (*guardianRecordedExecCall, error) {
	if len(items) == 0 {
		return nil, invalidParams(guardianRecordedExecCallRequirement)
	}
	payload := guardianItemPayload(&items[len(items)-1])
	if len(payload) == 0 {
		return nil, invalidParams(guardianRecordedExecCallRequirement)
	}
	if err := guardianRecordedExecCallFields(payload); err != nil {
		return nil, err
	}
	return &guardianRecordedExecCall{
		CallID:    strings.TrimSpace(stringFromAny(payload["call_id"])),
		Arguments: strings.TrimSpace(stringFromAny(payload["arguments"])),
	}, nil
}

func guardianRecordedExecCallFields(payload map[string]any) error {
	if strings.TrimSpace(stringFromAny(payload["type"])) != "function_call" ||
		strings.TrimSpace(stringFromAny(payload["name"])) != "exec_command" {
		return invalidParams(guardianRecordedExecCallRequirement)
	}
	if !guardianDefaultFunctionNamespace(stringFromAny(payload["namespace"])) {
		return invalidParams(guardianRecordedExecCallRequirement)
	}
	if guardianEncryptedArgsPresent(payload["encrypted_function_args"]) {
		return invalidParams(guardianRecordedExecCallRequirement)
	}
	if strings.TrimSpace(stringFromAny(payload["call_id"])) == "" {
		return invalidParams(guardianRecordedExecCallRequirement)
	}
	switch status := strings.TrimSpace(stringFromAny(payload["status"])); status {
	case "", "completed":
	default:
		return invalidParams(guardianRecordedExecCallRequirement)
	}
	arguments := strings.TrimSpace(stringFromAny(payload["arguments"]))
	if arguments == "" {
		return invalidParams(guardianRecordedExecCallRequirement)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
		return invalidParams("exec_command arguments must be a JSON object")
	}
	return nil
}

// guardianDefaultFunctionNamespace mirrors Rust's ToolName::is_default_namespace.
func guardianDefaultFunctionNamespace(namespace string) bool {
	switch strings.TrimSpace(namespace) {
	case "", "functions":
		return true
	default:
		return false
	}
}

func guardianEncryptedArgsPresent(raw any) bool {
	switch value := raw.(type) {
	case nil:
		return false
	case []any:
		return len(value) > 0
	case []string:
		return len(value) > 0
	case string:
		return strings.TrimSpace(value) != ""
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return true
		}
		trimmed := strings.TrimSpace(string(data))
		return trimmed != "" && trimmed != "null" && trimmed != "[]"
	}
}

// guardianItemPayload returns a session item's Responses-API payload: the raw
// item injected by thread/inject_items, the rollout item when history was read
// from a rollout, or a reconstruction from the stored fields.
func guardianItemPayload(item *session.Item) map[string]any {
	if item == nil {
		return nil
	}
	if item.Metadata != nil {
		if raw, ok := item.Metadata["rawResponseItem"].(map[string]any); ok && len(raw) > 0 {
			return raw
		}
	}
	if len(item.Raw) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(item.Raw, &payload); err == nil && len(payload) > 0 {
			return payload
		}
	}
	payload := map[string]any{}
	if itemType := strings.TrimSpace(item.Type); itemType != "" {
		payload["type"] = itemType
	}
	if name := strings.TrimSpace(item.Name); name != "" {
		payload["name"] = name
	}
	if namespace := strings.TrimSpace(item.Namespace); namespace != "" {
		payload["namespace"] = namespace
	}
	if callID := strings.TrimSpace(item.CallID); callID != "" {
		payload["call_id"] = callID
	}
	if status := strings.TrimSpace(item.Status); status != "" {
		payload["status"] = status
	}
	if item.Data != nil {
		if arguments := strings.TrimSpace(stringFromAny(item.Data["arguments"])); arguments != "" {
			payload["arguments"] = arguments
		}
	}
	return payload
}

// guardianHistoryReplaysRequestPermissions reports whether the recorded history
// contains a plaintext request_permissions call, which a direct review cannot
// replay (Rust #58028).
func guardianHistoryReplaysRequestPermissions(items []session.Item) bool {
	for i := range items {
		payload := guardianItemPayload(&items[i])
		if len(payload) == 0 {
			continue
		}
		if strings.TrimSpace(stringFromAny(payload["type"])) != "function_call" {
			continue
		}
		if strings.TrimSpace(stringFromAny(payload["name"])) != "request_permissions" {
			continue
		}
		if guardianDefaultFunctionNamespace(stringFromAny(payload["namespace"])) {
			return true
		}
	}
	return false
}

// prepareRecordedExecCommand resolves the recorded call through the same
// environment and permission resolution a live exec_command uses, without
// executing it (Rust's RecordedExecCall::prepare). PreToolUse hooks run before
// the approval requirement is evaluated so a rewrite or denial cannot be
// bypassed by policy (#58357/#58368 apply_pre_tool_use).
func (r *RuntimeRouter) prepareRecordedExecCommand(ctx context.Context, threadID string, turnID string, record *session.Record, call *guardianRecordedExecCall) (*tool.ExecCommandPreparation, error) {
	params := r.guardianReviewTurnParams(threadID, record)
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, invalidParams("exec_command is unavailable on this thread")
	}
	cwd := firstNonEmpty(strings.TrimSpace(params.CWD), strings.TrimSpace(record.Metadata.CWD), r.services.DefaultCWD)
	validation := tool.ShellValidationOptions{
		CWD:            cwd,
		ApprovalPolicy: turnApprovalPolicyForTurn(cfg, params),
		PathConvention: tool.PathConventionForPath(cwd),
	}
	if cfg != nil {
		validation.AdditionalPermissionsAllowed = features.Enabled(cfg.FeatureSettings(), "exec_permission_approvals")
		validation.AllowLoginShell = cfg.AllowLoginShell()
		validation.LoginShellPackagePath = features.Enabled(cfg.FeatureSettings(), "login_shell_package_path")
	}
	resolution, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil {
		return nil, invalidParams("exec_command is unavailable on this thread")
	}
	if resolution != nil && resolution.Profile != nil {
		validation.PermissionProfileID = strings.TrimSpace(resolution.ID)
		validation.PermissionProfile = resolution.Profile
	}
	if r.commandApprovalForSession(threadID) {
		validation.PermissionsPreapproved = true
	}
	invocation := &tool.Invocation{
		CallID:   call.CallID,
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: call.Arguments},
	}
	executor := tool.NewShellExecutor(&tool.ShellExecutorOptions{
		Shell:                   tool.NewDefaultShell(),
		Validation:              validation,
		UnifiedExecEnvironments: r.unifiedExecEnvironmentsForTurn(params),
	})
	effective, err := r.applyRecordedExecPreToolUse(ctx, params, turnID, executor, invocation)
	if err != nil {
		return nil, err
	}
	prepared, err := executor.PrepareExecCommand(effective)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	return prepared, nil
}

// applyRecordedExecPreToolUse runs the shared PreToolUse hook handling for a
// recorded exec_command and returns the possibly rewritten invocation. A hook
// denial fails the review even when exec policy would otherwise skip it.
func (r *RuntimeRouter) applyRecordedExecPreToolUse(ctx context.Context, params *turn.TurnStartParams, turnID string, executor *tool.ShellExecutor, invocation *tool.Invocation) (*tool.Invocation, error) {
	hooks := r.turnHookAdapter(params, turnID)
	if hooks == nil || executor == nil || invocation == nil {
		return invocation, nil
	}
	payload, ok := executor.PreToolUsePayload(invocation)
	if !ok || payload == nil {
		return invocation, nil
	}
	if environments := executor.HookEnvironmentIDs(invocation); len(environments) > 0 {
		payload.TargetEnvironments = environments
	}
	outcome, err := hooks.RunPreToolUse(ctx, invocation, payload)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return nil, errors.New("Guardian review failed: the review was cancelled")
		}
		return nil, invalidParams("exec_command hook failed: " + err.Error())
	}
	if outcome == nil {
		return invocation, nil
	}
	if outcome.Fatal {
		return nil, invalidParams(tool.PreToolUseStoppedMessage(outcome.FatalReason))
	}
	if outcome.Blocked {
		return nil, invalidParams(tool.PreToolUseBlockMessage(invocation, outcome.BlockReason))
	}
	if outcome.UpdatedInput == nil {
		return invocation, nil
	}
	updated, err := executor.WithUpdatedHookInput(invocation, outcome.UpdatedInput)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	return updated, nil
}

// guardianReviewTurnParams rebuilds the thread's turn parameters for an idle
// review: Rust starts the review in a fresh turn that inherits the thread's
// settings.
func (r *RuntimeRouter) guardianReviewTurnParams(threadID string, record *session.Record) *turn.TurnStartParams {
	params := &turn.TurnStartParams{ThreadID: threadID}
	if record != nil {
		params.Config = threadRecordConfigOverrides(record)
		params.CWD = strings.TrimSpace(record.Metadata.CWD)
		params.Model = strings.TrimSpace(record.Metadata.Model)
	}
	settings := r.threadSettingsForTurn(threadID)
	if settings != nil {
		params.CWD = firstNonEmpty(threadSettingsCWD(settings), params.CWD)
		params.Model = firstNonEmpty(threadSettingsModel(settings), params.Model)
		if policy := strings.TrimSpace(settings.ApprovalPolicy); policy != "" {
			params.ApprovalPolicy = policy
		}
		if reviewer := strings.TrimSpace(settings.ApprovalsReviewer); reviewer != "" {
			params.ApprovalsReviewer = &reviewer
		}
		if settings.ActivePermissionProfile != nil && strings.TrimSpace(*settings.ActivePermissionProfile) != "" {
			params.Permissions = cloneString(settings.ActivePermissionProfile)
		} else if strings.TrimSpace(settings.SandboxPolicy) != "" {
			params.SandboxPolicy = settings.SandboxPolicy
		}
		applyThreadSettingsToTurnStartParams(params, settings)
	}
	if strings.TrimSpace(params.CWD) == "" {
		params.CWD = r.services.DefaultCWD
	}
	r.inheritTurnEnvironmentSelections(params)
	return params
}

// registerRequestCancellation registers a request-scoped cancellation that a
// connection close also cancels (Rust #58119). It reuses thread/sample's
// connection-keyed registry so disconnects cancel every in-flight request.
func (r *RuntimeRouter) registerRequestCancellation(request *Request) (context.Context, func()) {
	return r.registerThreadSampleCancel(request)
}
