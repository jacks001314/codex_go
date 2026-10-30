package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/state"
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
