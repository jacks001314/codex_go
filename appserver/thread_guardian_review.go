package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/execserver"
	"codex_go/features"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// Direct Guardian review protocol types (Rust #58119). `thread/guardianReview`
// reviews an idle thread's final injected exec_command or apply_patch call
// without running it and returns Guardian's structured assessment, or a null
// assessment when the approval policy skips review.

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

type guardianRecordedCallKind string

const (
	guardianRecordedCallExecCommand        guardianRecordedCallKind = "exec_command"
	guardianRecordedCallApplyPatch         guardianRecordedCallKind = "apply_patch"
	guardianRecordedCallRequestPermissions guardianRecordedCallKind = "request_permissions"
)

// guardianRecordedToolCall is the recorded tool call a direct review may
// assess (Rust's RecordedToolCall::from_item).
type guardianRecordedToolCall struct {
	CallID string
	Kind   guardianRecordedCallKind
	// Arguments carries exec_command's JSON arguments.
	Arguments string
	// Input carries apply_patch's freeform patch body.
	Input string
	// Reason, Permissions and EnvironmentID carry a request_permissions call.
	Reason        string
	Permissions   map[string]any
	EnvironmentID string
}

const guardianRecordedToolCallRequirement = "expected a completed plaintext exec_command, apply_patch or request_permissions call with a nonempty call ID"

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
	call, err := guardianRecordedToolCallFromItems(record.Items)
	if err != nil {
		return nil, err
	}
	if guardianHistoryReplaysRequestPermissions(record.Items) {
		return nil, invalidParams("direct review does not support replaying request_permissions")
	}
	turnID := newMemoryTurnID()
	response := &ThreadGuardianReviewResponse{CallID: call.CallID}
	var action state.Action
	switch call.Kind {
	case guardianRecordedCallExecCommand:
		prepared, err := r.prepareRecordedExecCommand(reviewCtx, threadID, turnID, record, call)
		if err != nil {
			return nil, err
		}
		if !prepared.Request.ApprovalRequired || r.commandApprovalForSession(threadID, prepared.Request) {
			// Exec policy explicitly allows the command: nothing to assess, and
			// nothing is executed.
			return response, nil
		}
		action = commandApprovalAction(prepared.Request)
	case guardianRecordedCallApplyPatch:
		prepared, err := r.prepareRecordedApplyPatch(reviewCtx, threadID, turnID, record, call)
		if err != nil {
			return nil, err
		}
		if !prepared.ApprovalRequired || r.fileChangeApprovalForSession(threadID) {
			// Writable files need no review, and preparing a patch never writes.
			return response, nil
		}
		action = applyPatchApprovalAction(prepared.Request)
	case guardianRecordedCallRequestPermissions:
		params := r.guardianReviewTurnParams(threadID, record)
		cfg, _ := r.effectiveConfigForTurn(params)
		approvalPolicy := turnApprovalPolicyForTurn(cfg, params)
		if approvalPolicy == sandbox.ApprovalNever {
			return response, nil
		}
		if approvalPolicy == sandbox.ApprovalGranular {
			granular := granularApprovalConfigForTurn(cfg, params)
			if granular == nil || !granular.AllowsRequestPermissions() {
				return response, nil
			}
		}
		if _, err := additionalPermissionProfileFromJSONMap(call.Permissions); err != nil {
			return nil, invalidParams(err.Error())
		}
		// Live review runs PermissionRequest hooks before Guardian; a hook
		// decision replaces the assessment (Rust Session::request_approval).
		if verdict, ok := r.permissionRequestHookVerdict(reviewCtx, threadID, turnID, call.CallID, "request_permissions", nil, requestPermissionsHookToolInput(call.Reason, call.Permissions), nil, firstNonEmpty(call.EnvironmentID, execserver.LocalEnvironmentID)); ok && verdict != nil {
			return response, nil
		}
		action = state.Action{
			Type:          "request_permissions",
			EnvironmentID: call.EnvironmentID,
			TurnID:        turnID,
			Reason:        call.Reason,
			Permissions:   call.Permissions,
		}
	default:
		return nil, invalidParams(guardianRecordedToolCallRequirement)
	}
	reviewer := r.guardianReviewerForRequest()
	if reviewer == nil {
		return nil, errors.New("Guardian review failed: automatic approval review is unavailable")
	}
	assessmentReviewer, ok := reviewer.(GuardianAssessmentReviewer)
	if !ok {
		return nil, errors.New("Guardian review failed: the reviewer cannot report an assessment")
	}
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

// guardianRecordedToolCallFromItems reads the recorded call from the thread's
// final item (Rust's RecordedToolCall::from_item). A trailing tool output or
// any other item invalidates the request.
func guardianRecordedToolCallFromItems(items []session.Item) (*guardianRecordedToolCall, error) {
	if len(items) == 0 {
		return nil, invalidParams(guardianRecordedToolCallRequirement)
	}
	payload := guardianItemPayload(&items[len(items)-1])
	if len(payload) == 0 {
		return nil, invalidParams(guardianRecordedToolCallRequirement)
	}
	return guardianRecordedToolCallFields(payload)
}

func guardianRecordedToolCallFields(payload map[string]any) (*guardianRecordedToolCall, error) {
	if !guardianDefaultFunctionNamespace(stringFromAny(payload["namespace"])) {
		return nil, invalidParams(guardianRecordedToolCallRequirement)
	}
	callID := strings.TrimSpace(stringFromAny(payload["call_id"]))
	if callID == "" {
		return nil, invalidParams(guardianRecordedToolCallRequirement)
	}
	switch status := strings.TrimSpace(stringFromAny(payload["status"])); status {
	case "", "completed":
	default:
		return nil, invalidParams(guardianRecordedToolCallRequirement)
	}
	payloadType := strings.TrimSpace(stringFromAny(payload["type"]))
	name := strings.TrimSpace(stringFromAny(payload["name"]))
	switch {
	case payloadType == "function_call" && name == "exec_command":
		if guardianEncryptedArgsPresent(payload["encrypted_function_args"]) {
			return nil, invalidParams(guardianRecordedToolCallRequirement)
		}
		arguments := strings.TrimSpace(stringFromAny(payload["arguments"]))
		if arguments == "" {
			return nil, invalidParams(guardianRecordedToolCallRequirement)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
			return nil, invalidParams("exec_command arguments must be a JSON object")
		}
		return &guardianRecordedToolCall{CallID: callID, Kind: guardianRecordedCallExecCommand, Arguments: arguments}, nil
	case payloadType == "custom_tool_call" && name == "apply_patch":
		input := strings.TrimSpace(stringFromAny(payload["input"]))
		if input == "" {
			return nil, invalidParams(guardianRecordedToolCallRequirement)
		}
		return &guardianRecordedToolCall{CallID: callID, Kind: guardianRecordedCallApplyPatch, Input: input}, nil
	case payloadType == "function_call" && name == "request_permissions":
		if guardianEncryptedArgsPresent(payload["encrypted_function_args"]) {
			return nil, invalidParams(guardianRecordedToolCallRequirement)
		}
		arguments := strings.TrimSpace(stringFromAny(payload["arguments"]))
		if arguments == "" {
			return nil, invalidParams(guardianRecordedToolCallRequirement)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
			return nil, invalidParams("request_permissions arguments must be a JSON object")
		}
		permissions, _ := decoded["permissions"].(map[string]any)
		if len(permissions) == 0 {
			return nil, invalidParams("request_permissions requires a permissions object")
		}
		return &guardianRecordedToolCall{
			CallID:        callID,
			Kind:          guardianRecordedCallRequestPermissions,
			Reason:        strings.TrimSpace(stringFromAny(decoded["reason"])),
			Permissions:   permissions,
			EnvironmentID: strings.TrimSpace(stringFromAny(decoded["environment_id"])),
		}, nil
	default:
		return nil, invalidParams(guardianRecordedToolCallRequirement)
	}
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
		if input := strings.TrimSpace(stringFromAny(item.Data["input"])); input != "" {
			payload["input"] = input
		}
	}
	return payload
}

// guardianHistoryReplaysRequestPermissions reports whether history before the
// reviewed call contains a plaintext request_permissions call. A direct review
// may assess the final such call but never replays earlier grants (Rust
// #58028/#58903).
func guardianHistoryReplaysRequestPermissions(items []session.Item) bool {
	for i := 0; i < len(items)-1; i++ {
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
func (r *RuntimeRouter) prepareRecordedExecCommand(ctx context.Context, threadID string, turnID string, record *session.Record, call *guardianRecordedToolCall) (*tool.ExecCommandPreparation, error) {
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
	effective, err := r.applyRecordedPreToolUse(ctx, params, turnID, executor, invocation)
	if err != nil {
		return nil, err
	}
	prepared, err := executor.PrepareExecCommand(effective)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	return prepared, nil
}

// prepareRecordedApplyPatch verifies a recorded apply_patch call through the
// same hook, environment and permission resolution a live turn uses, without
// applying it (Rust #58899).
func (r *RuntimeRouter) prepareRecordedApplyPatch(ctx context.Context, threadID string, turnID string, record *session.Record, call *guardianRecordedToolCall) (*tool.ApplyPatchPreparation, error) {
	params := r.guardianReviewTurnParams(threadID, record)
	cwd := firstNonEmpty(strings.TrimSpace(params.CWD), strings.TrimSpace(record.Metadata.CWD), r.services.DefaultCWD)
	router, err := r.toolRouterForTurn(cwd, params, turnID)
	if err != nil {
		return nil, invalidParams("apply_patch is unavailable on this thread")
	}
	executor, ok := router.Executor(tool.PlainName(tool.DefaultApplyPatchToolName))
	if !ok {
		return nil, invalidParams("apply_patch is unavailable on this thread")
	}
	patchExecutor, ok := executor.(*tool.ApplyPatchExecutor)
	if !ok {
		return nil, invalidParams("apply_patch is unavailable on this thread")
	}
	invocation := &tool.Invocation{
		CallID:   call.CallID,
		ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
		Payload:  tool.Payload{Kind: tool.PayloadCustom, Input: call.Input},
	}
	effective, err := r.applyRecordedPreToolUse(ctx, params, turnID, patchExecutor, invocation)
	if err != nil {
		return nil, err
	}
	prepared, err := patchExecutor.PrepareApplyPatch(ctx, effective)
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	return prepared, nil
}

// recordedPreToolUseExecutor is the payload surface a recorded tool call needs
// for the shared PreToolUse handling.
type recordedPreToolUseExecutor interface {
	PreToolUsePayload(*tool.Invocation) (*tool.PreToolUsePayload, bool)
	HookEnvironmentIDs(*tool.Invocation) []string
	WithUpdatedHookInput(*tool.Invocation, any) (*tool.Invocation, error)
}

// applyRecordedPreToolUse runs the shared PreToolUse hook handling for a
// recorded tool call and returns the possibly rewritten invocation. A hook
// denial fails the review even when the approval policy would otherwise skip it.
func (r *RuntimeRouter) applyRecordedPreToolUse(ctx context.Context, params *turn.TurnStartParams, turnID string, executor recordedPreToolUseExecutor, invocation *tool.Invocation) (*tool.Invocation, error) {
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
		return nil, invalidParams("direct review hook failed: " + err.Error())
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
