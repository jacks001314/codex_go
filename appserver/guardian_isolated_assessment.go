package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	codexctx "codex_go/context"
	"codex_go/model"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/state"
)

// guardianIsolatedReviewInput bundles a prepared action with the parent
// transcript and review context captured when the action was prepared (Rust
// #57834's ReviewInput). The captured entries are the only history the
// isolated reviewer sees, so instructions recorded after capture never leak
// into the assessment.
type guardianIsolatedReviewInput struct {
	ThreadID     string
	TurnID       string
	TargetItemID string
	Action       state.Action
	// TranscriptEntries is the rendered parent history captured with the
	// action. It is never re-read from the live thread.
	TranscriptEntries      []state.GuardianTranscriptEntry
	TranscriptOmissionNote string
	TranscriptFormat       state.GuardianTranscriptFormat
	// NodeReplEvidence is the host-prepared evidence captured with the action.
	NodeReplEvidence *codexctx.NodeReplReviewEvidenceFragment
	// ParentResponseID and RoutingAffinity keep the isolated review routed like
	// the connection that produced the captured parent response (Rust #57710).
	ParentResponseID string
	RoutingAffinity  *network.RoutingAffinity
}

// AssessIsolated runs the supplied prepared action in a fresh reviewer without
// publishing a live approval (Rust #57834 ReviewInput::assess_isolated). It
// honors the deadline and context cancellation and returns the parsed
// assessment or the review error.
func (r *modelGuardianReviewer) AssessIsolated(ctx context.Context, input guardianIsolatedReviewInput, deadline time.Duration) (*state.Assessment, error) {
	if r == nil || r.agent == nil {
		return nil, errors.New("guardian reviewer is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reviewCtx, cancel := guardianReviewContext(ctx, deadline)
	defer cancel()
	if err := reviewCtx.Err(); err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(input.ThreadID)
	turnID := strings.TrimSpace(input.TurnID)
	plan := r.planForTurn(reviewCtx, threadID, turnID)
	if plan.PreparedError != "" {
		return nil, errors.New(plan.PreparedError)
	}
	format := input.TranscriptFormat
	if format == "" {
		format = state.GuardianTranscriptFormatLine
	}
	entries := input.TranscriptEntries
	buildOptions := state.BuildPromptOptions{
		Presentation:           state.ActionPresentationSyncFull,
		TranscriptFormat:       format,
		TranscriptOmissionNote: input.TranscriptOmissionNote,
		NodeReplEvidence:       input.NodeReplEvidence,
	}
	if r.turnPermissionProfile != nil {
		if profile, cwd, environmentID := r.turnPermissionProfile(threadID, turnID, strings.TrimSpace(input.Action.EnvironmentID)); profile != nil {
			deniedPaths := sandbox.UnreadableRootsWithCWD(profile, cwd)
			deniedGlobs := sandbox.UnreadableGlobsWithCWD(profile, cwd)
			if environmentID != nil || len(deniedPaths) > 0 || len(deniedGlobs) > 0 {
				buildOptions.PermissionContext = &state.PermissionContext{
					EnvironmentID: environmentID,
					DeniedPaths:   deniedPaths,
					DeniedGlobs:   deniedGlobs,
				}
			}
		}
	}
	parts, _, err := state.BuildPromptPartsWithSectionCosts(input.Action, entries, buildOptions)
	if err != nil {
		return nil, fmt.Errorf("guardian isolated assessment prompt: %w", err)
	}
	if r.rootUserAuthorization != nil {
		if rootAuthorization := r.rootUserAuthorization(threadID, turnID); len(rootAuthorization) > 0 {
			buildOptions.RootUserAuthorization = rootAuthorization
			if parts, _, err = state.BuildPromptPartsWithSectionCosts(input.Action, entries, buildOptions); err != nil {
				return nil, fmt.Errorf("guardian isolated assessment prompt: %w", err)
			}
		}
	}
	prompt, inputItems := guardianPromptDelivery(parts)
	items := append([]any(nil), inputItems...)
	if r.environment != nil {
		environmentItems, environmentErr := r.environment(reviewCtx, threadID, turnID)
		if environmentErr != nil {
			return nil, fmt.Errorf("guardian isolated assessment environment: %w", environmentErr)
		}
		items = append(items, environmentItems...)
	}
	if input.NodeReplEvidence != nil && input.NodeReplEvidence.HasImages() {
		for _, item := range input.NodeReplEvidence.MultimodalInputItemsFiltered(nil) {
			items = append(items, item)
		}
	}
	if fragment := codexctx.NewGuardianPolicy(plan.Instructions); fragment != nil {
		if item := buildRenderedMessage(codexctx.Render(fragment)); item != nil {
			items = append(items, item)
		}
	}
	// Rust #41385: the isolated assessment is its own classification turn, so it
	// gets a fresh identity while the captured action's turn stays its parent.
	classificationTurnID := r.classificationTurnID()
	request := &model.AgentRequest{
		Prompt:             prompt,
		InputItems:         items,
		Model:              plan.Selection.Model,
		ProviderID:         plan.ProviderID,
		Endpoint:           r.reviewEndpointValue(threadID, turnID),
		ReasoningEffort:    plan.Selection.ReasoningEffort,
		CyberAccessProgram: plan.CyberAccessProgram,
		TaskKind:           model.AgentTaskReview,
		ThreadID:           threadID,
		SessionID:          plan.SessionID,
		TurnID:             classificationTurnID,
		Originator:         "guardian",
		Ephemeral:          plan.Ephemeral,
		PromptCacheKey:     guardianPromptCacheKeyOverride(guardianReviewerName, threadID),
		OutputSchema:       guardianAssessmentOutputSchema(),
		RoutingAffinity:    input.RoutingAffinity,
		ClientMetadata:     r.reviewClientMetadata(threadID, turnID, classificationTurnID, input.TargetItemID, input.ParentResponseID),
	}
	if plan.Prepared != nil && plan.Prepared.PermissionProfile != nil {
		request.PermissionProfile = plan.Prepared.PermissionProfile
	}
	if request.PermissionProfile == nil && r.permissionProfile != nil {
		request.PermissionProfile = r.permissionProfile(threadID, turnID)
	}
	var response *model.AgentResponse
	if isolated, ok := r.agent.(interface {
		RunIsolated(context.Context, *model.AgentRequest) (*model.AgentResponse, error)
	}); ok {
		response, err = isolated.RunIsolated(reviewCtx, request)
	} else {
		response, err = r.agent.Run(reviewCtx, request)
	}
	// A cancelled or expired review is never reported as an assessment.
	if waitErr := reviewCtx.Err(); waitErr != nil {
		return nil, waitErr
	}
	if err != nil {
		return nil, err
	}
	assessment, err := state.ParseAssessment([]byte(guardianAssessmentText(response)))
	if err != nil {
		return nil, fmt.Errorf("guardian isolated assessment: %w", err)
	}
	return assessment, nil
}
