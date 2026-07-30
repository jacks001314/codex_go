package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

type GuardianReviewer interface {
	Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error)
}

type modelGuardianReviewer struct {
	agent      model.AgentRunner
	store      *state.ReviewStore
	breaker    *state.CircuitBreaker
	notify     func(threadID string, event *state.Event)
	interrupt  func(threadID, turnID string)
	transcript func(threadID string) []string
	timeout    time.Duration
}

type guardianSessionRunner struct {
	mu       sync.Mutex
	agent    model.AgentRunner
	previous string
}

type guardianPrewarmer interface {
	Prewarm(context.Context, *model.AgentRequest) (*model.AgentResponse, error)
}

type guardianWebSocketRunner interface {
	RunWebSocket(context.Context, *model.AgentRequest) (*model.AgentResponse, error)
}

func (r *guardianSessionRunner) Prewarm(ctx context.Context) error {
	if r == nil || r.agent == nil {
		return nil
	}
	prewarmer, ok := r.agent.(guardianPrewarmer)
	if !ok {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.previous != "" {
		return nil
	}
	response, err := prewarmer.Prewarm(ctx, &model.AgentRequest{
		Model: model.DefaultApprovalReviewPreferredModel, TaskKind: model.AgentTaskReview, Originator: "guardian",
		ClientMetadata: map[string]string{"x-openai-subagent": "guardian"},
	})
	if err == nil && response != nil && strings.TrimSpace(response.ResponseID) != "" {
		r.previous = strings.TrimSpace(response.ResponseID)
	}
	return err
}

func (r *guardianSessionRunner) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if r == nil || r.agent == nil {
		return nil, errors.New("guardian session runner is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *request
	clone.ClientMetadata = cloneStringMap(request.ClientMetadata)
	clone.PreviousResponseID = r.previous
	clone.Store = false
	var response *model.AgentResponse
	var err error
	if r.previous != "" {
		if ws, ok := r.agent.(guardianWebSocketRunner); ok {
			response, err = ws.RunWebSocket(ctx, &clone)
			if err != nil {
				clone.PreviousResponseID = ""
				r.previous = ""
				response, err = r.agent.Run(ctx, &clone)
			}
		} else {
			response, err = r.agent.Run(ctx, &clone)
		}
	} else {
		response, err = r.agent.Run(ctx, &clone)
	}
	if err == nil && response != nil && strings.TrimSpace(response.ResponseID) != "" {
		r.previous = strings.TrimSpace(response.ResponseID)
	}
	return response, err
}

func newModelGuardianReviewer(agent model.AgentRunner) GuardianReviewer {
	if agent == nil {
		return nil
	}
	return &modelGuardianReviewer{agent: &guardianSessionRunner{agent: agent}, store: state.NewReviewStore(), breaker: state.NewCircuitBreaker(), timeout: state.ReviewTimeout}
}

func (r *modelGuardianReviewer) Prewarm(ctx context.Context) error {
	session, ok := r.agent.(*guardianSessionRunner)
	if !ok {
		return nil
	}
	return session.Prewarm(ctx)
}

func (r *modelGuardianReviewer) Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
	if r == nil || r.agent == nil {
		return state.DecisionAborted, "", errors.New("guardian reviewer is unavailable")
	}
	var transcript []string
	if r.transcript != nil {
		transcript = r.transcript(threadID)
	}
	prompt, err := state.BuildPrompt(action, transcript)
	if err != nil {
		return state.DecisionAborted, "", err
	}
	store := r.store
	if store == nil {
		store = state.NewReviewStore()
	}
	event, err := store.Start(turnID, targetItemID, action)
	if err != nil {
		return state.DecisionAborted, "", err
	}
	r.emit(threadID, event)
	timeout := r.timeout
	if timeout <= 0 {
		timeout = state.ReviewTimeout
	}
	reviewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := r.agent.Run(reviewCtx, &model.AgentRequest{
		Prompt:       prompt,
		TaskKind:     model.AgentTaskReview,
		ThreadID:     threadID,
		TurnID:       turnID,
		Originator:   "guardian",
		OutputSchema: guardianAssessmentOutputSchema(),
		ClientMetadata: map[string]string{
			"x-openai-subagent": "guardian",
			"parent_turn_id":    turnID,
			"target_item_id":    targetItemID,
		},
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reviewCtx.Err(), context.DeadlineExceeded) {
			completed, finishErr := store.Timeout(event.ID)
			if finishErr == nil {
				r.emit(threadID, completed)
			}
			r.recordDecision(threadID, turnID, state.DecisionTimedOut)
			return state.DecisionTimedOut, state.GuardianTimeoutMessage(), finishErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(reviewCtx.Err(), context.Canceled) {
			completed, finishErr := store.Abort(event.ID, "Guardian review was aborted.")
			if finishErr == nil {
				r.emit(threadID, completed)
			}
			r.recordDecision(threadID, turnID, state.DecisionAborted)
			return state.DecisionAborted, "", finishErr
		}
		completed, _ := store.Abort(event.ID, err.Error())
		r.emit(threadID, completed)
		r.recordDecision(threadID, turnID, state.DecisionAborted)
		return state.DecisionAborted, "", err
	}
	assessment, err := state.ParseAssessment([]byte(guardianAssessmentText(response)))
	if err != nil {
		completed, _ := store.Abort(event.ID, "Guardian returned an invalid assessment.")
		r.emit(threadID, completed)
		r.recordDecision(threadID, turnID, state.DecisionAborted)
		return state.DecisionAborted, "", err
	}
	completed, err := store.Complete(event.ID, *assessment)
	if err != nil {
		return state.DecisionAborted, "", err
	}
	r.emit(threadID, completed)
	decision := state.DecisionFromEvent(completed)
	r.recordDecision(threadID, turnID, decision)
	return decision, assessment.Rationale, nil
}

func (r *modelGuardianReviewer) emit(threadID string, event *state.Event) {
	if r != nil && r.notify != nil && event != nil {
		r.notify(threadID, event)
	}
}

func (r *modelGuardianReviewer) recordDecision(threadID, turnID string, decision state.ReviewDecision) {
	if r == nil || r.breaker == nil {
		return
	}
	if decision == state.DecisionApproved {
		r.breaker.RecordNonDenial(turnID)
		return
	}
	if action := r.breaker.RecordDenial(turnID); action.InterruptTurn && r.interrupt != nil {
		r.interrupt(threadID, turnID)
	}
}

func guardianAssessmentText(response *model.AgentResponse) string {
	if response == nil {
		return ""
	}
	if message := strings.TrimSpace(response.Message); message != "" {
		return message
	}
	for i := len(response.Items) - 1; i >= 0; i-- {
		if response.Items[i].Type == "agent_message" || response.Items[i].Type == "" {
			if text := strings.TrimSpace(response.Items[i].Text); text != "" {
				return text
			}
		}
	}
	return ""
}

func guardianAssessmentOutputSchema() any {
	return json.RawMessage(`{"type":"object","properties":{"riskLevel":{"type":"string","enum":["low","medium","high","critical"]},"userAuthorization":{"type":"string","enum":["unknown","low","medium","high"]},"outcome":{"type":"string","enum":["allow","deny"]},"rationale":{"type":"string"}},"required":["riskLevel","userAuthorization","outcome","rationale"],"additionalProperties":false}`)
}

func (r *RuntimeRouter) guardianReviewTranscript(threadID string) []string {
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, true)
	if err != nil || record == nil {
		return nil
	}
	const maxLines = 12
	const maxChars = 4000
	lines := make([]string, 0, maxLines)
	for i := len(record.Items) - 1; i >= 0 && len(lines) < maxLines; i-- {
		item := record.Items[i]
		text := strings.TrimSpace(item.Text)
		if text == "" && len(item.Content) > 0 {
			for _, part := range item.Content {
				if strings.TrimSpace(part.Text) != "" {
					text = strings.TrimSpace(part.Text)
					break
				}
			}
		}
		if text == "" {
			continue
		}
		if len(text) > maxChars {
			text = text[:maxChars]
		}
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = strings.TrimSpace(item.Type)
		}
		if role != "" {
			text = role + ": " + text
		}
		lines = append(lines, text)
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

func (r *RuntimeRouter) notifyGuardianReviewEvent(threadID string, event *state.Event) {
	if r == nil || event == nil {
		return
	}
	targetItemID := optionalStringPointer(event.TargetItemID)
	action := GuardianApprovalReviewAction{
		Type:          event.Action.Type,
		Source:        GuardianCommandSource(event.Action.Source),
		Command:       event.Action.Command,
		Program:       event.Action.Program,
		Argv:          append([]string(nil), event.Action.Argv...),
		CWD:           event.Action.CWD,
		Files:         append([]string(nil), event.Action.Files...),
		Target:        event.Action.Target,
		Host:          event.Action.Host,
		Protocol:      NetworkApprovalProtocol(event.Action.Protocol),
		Port:          event.Action.Port,
		Server:        event.Action.Server,
		ToolName:      event.Action.ToolName,
		ConnectorID:   optionalStringPointer(event.Action.ConnectorID),
		ConnectorName: optionalStringPointer(event.Action.ConnectorName),
		ToolTitle:     optionalStringPointer(event.Action.ToolTitle),
		Reason:        optionalStringPointer(event.Action.Reason),
		Permissions:   guardianRequestPermissionProfile(event.Action.Permissions),
	}
	review := guardianApprovalReviewFromState(event)
	if event.Status == state.StatusInProgress {
		r.notify(NotificationItemGuardianApprovalReviewStarted, &ItemGuardianApprovalReviewStartedNotification{
			ThreadID: threadID, TurnID: event.TurnID, StartedAtMS: uint64(event.StartedAtMS), ReviewID: event.ID, TargetItemID: targetItemID, Review: review, Action: action,
		})
		return
	}
	completedAt := event.StartedAtMS
	if event.CompletedAtMS != nil {
		completedAt = *event.CompletedAtMS
	}
	r.notify(NotificationItemGuardianApprovalReviewCompleted, &ItemGuardianApprovalReviewCompletedNotification{
		ThreadID: threadID, TurnID: event.TurnID, StartedAtMS: uint64(event.StartedAtMS), CompletedAtMS: uint64(completedAt), ReviewID: event.ID, TargetItemID: targetItemID,
		DecisionSource: AutoReviewDecisionSourceAgent, Review: review, Action: action,
	})
}

func guardianRequestPermissionProfile(values map[string]any) *RequestPermissionProfile {
	if len(values) == 0 {
		return nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	var profile RequestPermissionProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil
	}
	return &profile
}

func (r *RuntimeRouter) interruptTurnForGuardianCircuitBreaker(threadID, turnID string) {
	if r == nil {
		return
	}
	if active, ok := r.cancelActiveRuntimeTurn(threadID, turnID); ok {
		r.finishTurnInterruptedAnalytics(threadID, turnID, active.StartedAtMS, analyticsContextFromActiveRuntimeTurn(active))
		return
	}
	_, _ = r.requireTurns().Interrupt(&turn.TurnInterruptParams{ThreadID: threadID, TurnID: turnID})
}

func (r *RuntimeRouter) handleThreadApproveGuardianDeniedActionRuntime(request *Request) (*ThreadApproveGuardianDeniedActionResponse, error) {
	var params ThreadApproveGuardianDeniedActionParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	var event state.Event
	if err := json.Unmarshal(params.Event, &event); err != nil {
		return nil, jsonRPCInvalidRequest("invalid Guardian denial event: " + err.Error())
	}
	if event.Status != state.StatusDenied {
		return &ThreadApproveGuardianDeniedActionResponse{}, nil
	}
	approved := map[string]any{"action": event.Action, "outcome": "allowed"}
	approvedJSON, err := json.MarshalIndent(approved, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize approved Guardian action: %w", err)
	}
	text := state.DeniedActionApprovalPrefix + "\n\n" +
		"Treat this as approval to perform that exact action in the same context in which it was originally requested.\n" +
		"Do not assume this also authorizes similar operations with different payloads.\n\n" +
		"Approved action:\n" + string(approvedJSON)
	item := session.Item{
		ID: "guardian-denied-action-approval-" + safeIdentifier(event.ID), Type: "message", Role: "developer", Text: text,
		Content: []session.ContentPart{{Type: "input_text", Text: text}}, CreatedAt: runtimeRouterNow(r).UTC(),
		Metadata: map[string]any{"turnId": event.TurnID, "turn_id": event.TurnID, "guardianReviewId": event.ID, "guardian_review_id": event.ID},
	}
	if _, err := r.runtimeAppendItem(session.ThreadID(params.ThreadID), item); err != nil {
		return nil, err
	}
	_ = r.appendRuntimeRollout(params.ThreadID, []session.Item{item}, item.CreatedAt)
	if active := r.activeRuntimeTurnStateSnapshot(params.ThreadID, event.TurnID); active != nil {
		input := map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": text}}}
		if err := r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: params.ThreadID, TurnID: event.TurnID, InputItems: []any{input}}); err != nil {
			return nil, err
		}
	}
	return &ThreadApproveGuardianDeniedActionResponse{}, nil
}

func (r *RuntimeRouter) handleThreadInjectItemsRuntime(request *Request) (*ThreadInjectItemsResponse, error) {
	var params ThreadInjectItemsParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	result, err := r.services.ThreadRouter.dispatch(request)
	if err != nil {
		return nil, err
	}
	response, ok := result.(*ThreadInjectItemsResponse)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected thread/inject_items response %T", ErrInvalidRequest, result)
	}
	active := r.activeRuntimeTurnSnapshot(params.ThreadID)
	if active == nil || strings.TrimSpace(active.ID) == "" || len(params.Items) == 0 {
		return response, nil
	}
	inputItems := make([]any, 0, len(params.Items))
	for _, raw := range params.Items {
		var item any
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, jsonRPCInvalidRequest(err.Error())
		}
		inputItems = append(inputItems, item)
	}
	if err := r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: params.ThreadID, TurnID: active.ID, InputItems: inputItems}); err != nil {
		return nil, err
	}
	return response, nil
}

func (r *RuntimeRouter) handleThreadElicitationCountRuntime(request *Request) (any, error) {
	var params struct {
		ThreadID string `json:"threadId"`
	}
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	result, err := r.services.ThreadRouter.dispatch(request)
	if err != nil {
		return nil, err
	}
	paused := false
	switch response := result.(type) {
	case *ThreadIncrementElicitationResponse:
		paused = response.Paused
	case *ThreadDecrementElicitationResponse:
		paused = response.Paused
	default:
		return nil, fmt.Errorf("%w: unexpected elicitation response %T", ErrInvalidRequest, result)
	}
	if r.services.UnifiedExec != nil {
		r.services.UnifiedExec.SetThreadElicitationPaused(params.ThreadID, paused)
	}
	return result, nil
}

func guardianApprovalReviewFromState(event *state.Event) GuardianApprovalReview {
	status := GuardianApprovalReviewStatus(event.Status)
	if event.Status == state.StatusInProgress {
		status = GuardianApprovalReviewInProgress
	} else if event.Status == state.StatusTimedOut {
		status = GuardianApprovalReviewTimedOut
	}
	var risk *GuardianRiskLevel
	if event.RiskLevel != nil {
		value := GuardianRiskLevel(*event.RiskLevel)
		risk = &value
	}
	var authorization *GuardianUserAuthorization
	if event.UserAuthorization != nil {
		value := GuardianUserAuthorization(*event.UserAuthorization)
		authorization = &value
	}
	return GuardianApprovalReview{Status: status, RiskLevel: risk, UserAuthorization: authorization, Rationale: optionalStringPointer(event.Rationale)}
}

func optionalStringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	copy := value
	return &copy
}
