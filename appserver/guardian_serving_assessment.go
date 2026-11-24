package appserver

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/state"
	"codex_go/tool"
)

// guardianServingAuthorization captures the authorization and policy a serving
// assessment is bound to (Rust #58075 ScoreAuthorization::for_session). The
// reviewer revalidates it against the same snapshot before letting a low-risk
// assessment replace the synchronous review.
func (r *RuntimeRouter) guardianServingAuthorization(threadID, turnID string) state.ServingAuthorization {
	authorization := state.ServingAuthorization{}
	if r == nil {
		return authorization
	}
	threadID, turnID = strings.TrimSpace(threadID), strings.TrimSpace(turnID)
	if profile, cwd, environmentID := r.guardianTurnPermissionProfileForTurn(threadID, turnID, ""); profile != nil {
		authorization.EnvironmentID = environmentID
		authorization.DeniedPaths = sandbox.UnreadableRootsWithCWD(profile, cwd)
		authorization.DeniedGlobs = sandbox.UnreadableGlobsWithCWD(profile, cwd)
		authorization.RetainedContextComplete = true
	}
	if retained := r.guardianRetainedContextForTurn(threadID, turnID); retained != nil &&
		(!retained.VerifiedAnswersComplete() || !retained.UserMessagesComplete() || retained.HasOmittedAssistantMessages()) {
		authorization.RetainedContextComplete = false
	}
	authorization.PolicyFingerprint = r.guardianServingPolicyFingerprint(threadID, turnID)
	authorization.UserMessageRevision = r.guardianUserMessageRevision(threadID)
	return authorization
}

func (r *RuntimeRouter) guardianServingPolicyFingerprint(threadID, turnID string) string {
	parts := []string{}
	if active := r.activeRuntimeTurnStateSnapshot(threadID, turnID); active != nil && active.Params != nil {
		parts = append(parts, strings.TrimSpace(active.Params.Model))
		if raw, err := json.Marshal(active.Params.ApprovalPolicy); err == nil {
			parts = append(parts, string(raw))
		}
		if raw, err := json.Marshal(active.Params.SandboxPolicy); err == nil {
			parts = append(parts, string(raw))
		}
		if active.Params.ApprovalsReviewer != nil {
			parts = append(parts, strings.TrimSpace(*active.Params.ApprovalsReviewer))
		}
	}
	parts = append(parts, strings.TrimSpace(r.guardianApprovalsReviewerForTurn(threadID, turnID)))
	return strings.Join(parts, "|")
}

// guardianServingOwnerAuthorizationMatches reports whether the prepared MCP
// server owner's captured rules match the permission evidence a serving
// assessment was scored under (Rust #59489: the score must also match the rules
// of the server that will handle the call).
func guardianServingOwnerAuthorizationMatches(origin *guardianReviewOrigin, authorization state.ServingAuthorization) bool {
	if origin == nil || origin.PermissionProfile == nil {
		return false
	}
	ownerEnvironment := strings.TrimSpace(origin.EnvironmentID)
	scoredEnvironment := ""
	if authorization.EnvironmentID != nil {
		scoredEnvironment = strings.TrimSpace(*authorization.EnvironmentID)
	}
	if ownerEnvironment != scoredEnvironment || (ownerEnvironment == "") != (authorization.EnvironmentID == nil) {
		return false
	}
	return slices.Equal(sandbox.UnreadableRootsWithCWD(origin.PermissionProfile, origin.CWD), authorization.DeniedPaths) &&
		slices.Equal(sandbox.UnreadableGlobsWithCWD(origin.PermissionProfile, origin.CWD), authorization.DeniedGlobs)
}

// recordGuardianServingAssessment retains a matched serving value with the
// invocation that produced it, so later waits and retries still find it.
func (r *RuntimeRouter) recordGuardianServingAssessment(threadID, turnID, callID string, assessment state.ServingAssessment) {
	if r == nil || r.threads == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	r.threads.UpdateTurn(strings.TrimSpace(threadID), strings.TrimSpace(turnID), func(active *activeRuntimeTurn) {
		if active.ServingAssessments == nil {
			active.ServingAssessments = map[string]state.ServingAssessment{}
		}
		active.ServingAssessments[callID] = assessment
	})
}

// guardianServingAssessmentForReview mirrors Rust #62607: with a host-selected
// Guardian provider the actor stream's serving assessment belongs to a
// different inference route, so reviews run instead of trusting it.
func (r *RuntimeRouter) guardianServingAssessmentForReview(threadID, turnID, callID string) *state.ServingAssessment {
	if _, ok := r.guardianInferenceProvider(); ok {
		return nil
	}
	return r.guardianServingAssessmentFor(threadID, turnID, callID)
}

// guardianServingAssessmentFor resolves the serving value retained for one
// invocation. Missing values fall back to the synchronous review.
func (r *RuntimeRouter) guardianServingAssessmentFor(threadID, turnID, callID string) *state.ServingAssessment {
	if r == nil {
		return nil
	}
	active := r.activeRuntimeTurnStateSnapshot(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
	if active == nil {
		return nil
	}
	assessment, ok := active.ServingAssessments[strings.TrimSpace(callID)]
	if !ok {
		return nil
	}
	return &assessment
}

// forgetGuardianServingAssessment discards a value that no longer describes the
// invocation, for example after a hook rewrote its input (Rust #58075).
func (r *RuntimeRouter) forgetGuardianServingAssessment(threadID, turnID, callID string) {
	if r == nil || r.threads == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	r.threads.UpdateTurn(strings.TrimSpace(threadID), strings.TrimSpace(turnID), func(active *activeRuntimeTurn) {
		delete(active.ServingAssessments, callID)
	})
}

// applyStreamGuardianAssessment matches the final serving value to the output
// item it was emitted for (Rust #58075: one value immediately before the
// matching item's completion) and retains it with that invocation.
func (r *RuntimeRouter) applyStreamGuardianAssessment(threadID, turnID string, event *model.ResponsesStreamEvent, snapshot *responsesStreamNotificationState) {
	if r == nil || event == nil || snapshot == nil || snapshot.pendingGuardianAssessment == nil {
		return
	}
	pending := snapshot.pendingGuardianAssessment
	snapshot.pendingGuardianAssessment = nil
	itemID := strings.TrimSpace(event.ItemID)
	if itemID == "" && event.Item != nil {
		itemID = firstNonEmpty(event.Item.ID, event.Item.CallID)
	}
	if itemID == "" || strings.TrimSpace(pending.ItemID) != itemID {
		return
	}
	if pending.ResponseID != "" && event.ResponseID != "" && strings.TrimSpace(pending.ResponseID) != strings.TrimSpace(event.ResponseID) {
		return
	}
	r.recordGuardianServingAssessment(threadID, turnID, itemID, state.ServingAssessment{
		RequiresReview: pending.RequiresReview,
		Authorization:  r.guardianServingAuthorization(threadID, turnID),
	})
}

// guardianProbeComparisonEvent mirrors Rust's codex.guardian_probe_comparison
// record (#62571): the streamed serving flag is an observation joined to the
// tool call, the async classification and the synchronous review by action id.
const guardianProbeComparisonEvent = "codex.guardian_probe_comparison"

// emitGuardianProbeComparison writes one shadow-comparison record to the
// thread's session telemetry. The record never influences a decision.
func (r *RuntimeRouter) emitGuardianProbeComparison(threadID string, fields map[string]string) {
	if r == nil || len(fields) == 0 {
		return
	}
	session := r.sessionTelemetryForThread(strings.TrimSpace(threadID))
	if session == nil {
		return
	}
	session.LogEvent(context.Background(), guardianProbeComparisonEvent, fields, nil)
}

// guardianProbeKey names the item whose serving flag describes an invocation:
// a nested Code Mode call inherits its enclosing cell's item (#62571), while a
// direct call uses its own item id. The scope names which of the two applied.
func guardianProbeKey(invocation *tool.Invocation) (string, string) {
	if invocation == nil {
		return "", "tool_item"
	}
	if strings.TrimSpace(invocation.Source) == tool.InvocationSourceCodeMode {
		if cellID, _ := invocation.Context[tool.CodeModeCellIDContextKey].(string); strings.TrimSpace(cellID) != "" {
			return strings.TrimSpace(cellID), "enclosing_item"
		}
	}
	return strings.TrimSpace(invocation.CallID), "tool_item"
}

// carryGuardianProbeToCodeModeCall copies the enclosing cell's serving value to
// the nested call so its review and comparison record see the same flag. A
// value recorded for the call itself wins.
func (r *RuntimeRouter) carryGuardianProbeToCodeModeCall(threadID, turnID string, invocation *tool.Invocation) {
	if r == nil || invocation == nil || strings.TrimSpace(invocation.Source) != tool.InvocationSourceCodeMode {
		return
	}
	key, scope := guardianProbeKey(invocation)
	callID := strings.TrimSpace(invocation.CallID)
	if scope != "enclosing_item" || key == "" || callID == "" || key == callID {
		return
	}
	if r.guardianServingAssessmentFor(threadID, turnID, callID) != nil {
		return
	}
	if cell := r.guardianServingAssessmentFor(threadID, turnID, key); cell != nil {
		r.recordGuardianServingAssessment(threadID, turnID, callID, *cell)
	}
}

// guardianProbeToolStageFields builds the tool half of the comparison record.
func guardianProbeToolStageFields(threadID, turnID, callID, model string, contextMode state.GuardianContextMode, scope, probe string) map[string]string {
	threadID = strings.TrimSpace(threadID)
	callID = strings.TrimSpace(callID)
	if threadID == "" || callID == "" {
		return nil
	}
	mode := contextMode.String()
	if mode == "" {
		mode = "not_reported"
	}
	return map[string]string{
		"stage":                 "tool",
		"thread_id":             threadID,
		"turn_id":               strings.TrimSpace(turnID),
		"call_id":               callID,
		"model":                 strings.TrimSpace(model),
		"guardian_context_mode": mode,
		"scope":                 scope,
		"probe_review":          probe,
	}
}

// logGuardianProbeToolStage records the tool-side observation for one
// invocation before its review starts.
func (r *RuntimeRouter) logGuardianProbeToolStage(threadID, turnID string, invocation *tool.Invocation) {
	if r == nil || invocation == nil {
		return
	}
	key, scope := guardianProbeKey(invocation)
	model := ""
	if invocation.StepAttribution != nil {
		model = invocation.StepAttribution.Model
	}
	fields := guardianProbeToolStageFields(threadID, turnID, invocation.CallID, model,
		r.guardianContextModeForTurn(threadID, turnID), scope,
		guardianProbeReviewLabel(r.guardianServingAssessmentFor(threadID, turnID, key)))
	r.emitGuardianProbeComparison(threadID, fields)
}
