package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/compact"
	"codex_go/config"
	codexctx "codex_go/context"
	"codex_go/features"
	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

type GuardianReviewer interface {
	Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error)
}

type modelGuardianReviewer struct {
	agent                      model.AgentRunner
	store                      *state.ReviewStore
	breaker                    *state.CircuitBreaker
	scoreMu                    sync.Mutex
	scoreProgress              map[string]*guardianScoreProgress
	maxToolCallLag             int
	notify                     func(threadID string, event *state.Event)
	interrupt                  func(threadID, turnID string)
	transcript                 func(threadID string) []string
	model                      func(threadID, turnID string) string
	autoReviewMessages         func(threadID, turnID string) *model.AutoReviewMessages
	specialty                  func(threadID, turnID string) string
	nodeReplAutoReviewRequired func(threadID, turnID string) bool
	fullAccess                 func(threadID, turnID string) bool
	approvalsReviewer          func(threadID, turnID string) string
	environment                func(context.Context, string, string) ([]any, error)
	permissionProfile          func(threadID, turnID string) *sandbox.PermissionProfile
	nodeReplEvidence           func(threadID string, reviewedSequence uint64) *codexctx.NodeReplReviewEvidenceFragment
	rootUserAuthorization      func(threadID, turnID string) []string
	fastDecision               func(context.Context, string, string, string)
	timeout                    time.Duration
}

// defaultGuardianMaxToolCallLag mirrors Rust
// GuardianV2Config::DEFAULT_MAX_TOOL_CALL_LAG (#39001).
const defaultGuardianMaxToolCallLag = 3

// guardianScoreProgress tracks the latest tool call and the latest scored tool
// call per thread (Rust GuardianV2ScoreProgress, #39001): approval review is
// skipped when the score lags by more than max_tool_call_lag tool calls.
type guardianScoreProgress struct {
	latestToolCall       int
	latestScoredToolCall int
}

func (r *modelGuardianReviewer) beginToolCall(threadID string) *guardianScoreProgress {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	r.scoreMu.Lock()
	defer r.scoreMu.Unlock()
	if r.scoreProgress == nil {
		r.scoreProgress = map[string]*guardianScoreProgress{}
	}
	progress := r.scoreProgress[threadID]
	if progress == nil {
		progress = &guardianScoreProgress{}
		r.scoreProgress[threadID] = progress
	}
	progress.latestToolCall++
	return progress
}

func (r *modelGuardianReviewer) scoreLag(threadID string) int {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return 0
	}
	r.scoreMu.Lock()
	defer r.scoreMu.Unlock()
	if r.scoreProgress == nil {
		return 0
	}
	progress := r.scoreProgress[threadID]
	if progress == nil {
		return 0
	}
	return progress.latestToolCall - progress.latestScoredToolCall
}

func (r *modelGuardianReviewer) markScored(threadID string) {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	r.scoreMu.Lock()
	defer r.scoreMu.Unlock()
	if progress := r.scoreProgress[threadID]; progress != nil {
		progress.latestScoredToolCall = progress.latestToolCall
	}
}

// SetMaxToolCallLag configures the per-thread stale-score bound (Rust
// GuardianV2Config::max_tool_call_lag). Non-positive values restore the
// default.
func (r *modelGuardianReviewer) SetMaxToolCallLag(lag int) {
	if r == nil {
		return
	}
	if lag <= 0 {
		lag = defaultGuardianMaxToolCallLag
	}
	r.maxToolCallLag = lag
}

type guardianSessionRunner struct {
	mu       sync.Mutex
	agent    model.AgentRunner
	previous string
	seeded   string
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
	if strings.TrimSpace(request.PreviousResponseID) != "" && strings.TrimSpace(r.seeded) != "" {
		clone.PreviousResponseID = strings.TrimSpace(r.seeded)
		r.previous = strings.TrimSpace(r.seeded)
		r.seeded = ""
	} else if strings.TrimSpace(request.PreviousResponseID) != "" {
		// A caller-provided seed (e.g. from SeedForReview after a compaction
		// reset) takes precedence until the session produces its own response.
		clone.PreviousResponseID = strings.TrimSpace(request.PreviousResponseID)
		r.previous = strings.TrimSpace(request.PreviousResponseID)
	} else {
		clone.PreviousResponseID = r.previous
	}
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

// ResetAfterParentCompaction restarts the review session seeded with the
// latest encrypted parent compaction response ID (Rust c2bcb9a26b
// guardian_reuse_parent_compaction). An empty responseID keeps the existing
// reviewer so its authorization and restriction context is preserved.
func (r *guardianSessionRunner) ResetAfterParentCompaction(responseID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(responseID) != "" {
		r.previous = ""
		r.seeded = strings.TrimSpace(responseID)
		return
	}
	// No reusable compaction: preserve the current reviewer context.
}

func (r *guardianSessionRunner) SeedForReview(request *model.AgentRequest) {
	if r == nil || request == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(r.seeded) != "" {
		request.PreviousResponseID = strings.TrimSpace(r.seeded)
	}
}

func newModelGuardianReviewer(agent model.AgentRunner) GuardianReviewer {
	if agent == nil {
		return nil
	}
	return &modelGuardianReviewer{
		agent: &guardianSessionRunner{agent: agent}, store: state.NewReviewStore(),
		breaker: state.NewCircuitBreaker(), scoreProgress: map[string]*guardianScoreProgress{},
		maxToolCallLag: defaultGuardianMaxToolCallLag, timeout: state.ReviewTimeout,
	}
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
	// Rust #39001: skip approval review when the latest risk score lags by more
	// than max_tool_call_lag tool calls. The Go simplified reviewer fails closed
	// (no stale-score auto-approval), mirroring the stale-data guard while
	// keeping the established refuse-approval convention.
	if r.maxToolCallLag > 0 && r.scoreLag(threadID) > r.maxToolCallLag {
		return state.DecisionDenied, "guardian review skipped: risk score lag exceeds max_tool_call_lag", nil
	}
	if r.fullAccess != nil && r.fullAccess(threadID, turnID) {
		if r.fastDecision != nil {
			r.fastDecision(ctx, threadID, turnID, targetItemID)
		}
		return state.DecisionApproved, "full access", nil
	}
	if r.approvalsReviewer != nil && strings.EqualFold(r.approvalsReviewer(threadID, turnID), string(config.ApprovalsReviewerUser)) &&
		action.Type == "mcp_tool_call" && strings.EqualFold(strings.TrimSpace(action.Server), "node_repl") && strings.EqualFold(strings.TrimSpace(action.ToolName), "js") {
		if r.fastDecision != nil {
			r.fastDecision(ctx, threadID, turnID, targetItemID)
		}
		return state.DecisionApproved, "user approval mode", nil
	}
	r.beginToolCall(threadID)
	var transcript []string
	if r.transcript != nil {
		transcript = r.transcript(threadID)
	}
	nodeReplAutoReviewRequired := false
	if r.nodeReplAutoReviewRequired != nil {
		nodeReplAutoReviewRequired = r.nodeReplAutoReviewRequired(threadID, turnID)
	}
	var nodeReplEvidence *codexctx.NodeReplReviewEvidenceFragment
	if r.nodeReplEvidence != nil {
		nodeReplEvidence = r.nodeReplEvidence(threadID, 0)
	}
	promptNodeReplEvidence := nodeReplEvidence
	if nodeReplEvidence != nil && nodeReplEvidence.HasImages() {
		promptNodeReplEvidence = nil
	}
	prompt, err := state.BuildPromptWithOptions(action, transcript, state.BuildPromptOptions{
		NodeReplAutoReviewRequired: nodeReplAutoReviewRequired,
		NodeReplEvidence:           promptNodeReplEvidence,
	})
	if err == nil && r.rootUserAuthorization != nil {
		var root []string
		root = r.rootUserAuthorization(threadID, turnID)
		if len(root) > 0 {
			prompt, err = state.BuildPromptWithOptions(action, transcript, state.BuildPromptOptions{
				NodeReplAutoReviewRequired: nodeReplAutoReviewRequired,
				NodeReplEvidence:           promptNodeReplEvidence,
				RootUserAuthorization:      root,
			})
		}
	}
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
	var inputItems []any
	if r.environment != nil {
		inputItems, err = r.environment(reviewCtx, threadID, turnID)
		if err != nil {
			completed, _ := store.Abort(event.ID, err.Error())
			r.emit(threadID, completed)
			r.recordDecision(threadID, turnID, state.DecisionAborted)
			return state.DecisionAborted, "", err
		}
	}
	if nodeReplEvidence != nil && nodeReplEvidence.HasImages() {
		for _, item := range nodeReplEvidence.MultimodalInputItems() {
			inputItems = append(inputItems, item)
		}
	}
	reviewRequest := &model.AgentRequest{
		Prompt:       prompt,
		InputItems:   inputItems,
		Model:        r.modelForTurn(threadID, turnID),
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
	}
	if r.permissionProfile != nil {
		reviewRequest.PermissionProfile = r.permissionProfile(threadID, turnID)
	}
	if runner, ok := r.agent.(*guardianSessionRunner); ok {
		runner.SeedForReview(reviewRequest)
	}
	response, err := r.agent.Run(reviewCtx, reviewRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reviewCtx.Err(), context.DeadlineExceeded) {
			completed, finishErr := store.Timeout(event.ID)
			if finishErr == nil {
				r.emit(threadID, completed)
			}
			r.recordDecision(threadID, turnID, state.DecisionTimedOut)
			return state.DecisionTimedOut, guardianTimeoutMessage(r.autoReviewMessagesForTurn(threadID, turnID)), finishErr
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
	r.markScored(threadID)
	rationale := assessment.Rationale
	if decision == state.DecisionDenied {
		rationale = guardianRejectionMessage(r.autoReviewMessagesForTurn(threadID, turnID), rationale)
	}
	return decision, rationale, nil
}

func (r *modelGuardianReviewer) autoReviewMessagesForTurn(threadID, turnID string) *model.AutoReviewMessages {
	if r == nil || r.autoReviewMessages == nil {
		return nil
	}
	return r.autoReviewMessages(threadID, turnID)
}

// guardianRejectionMessage mirrors Rust run_guardian_review (#39741): the
// acting model's rejection_instructions replace the default when present.
func guardianRejectionMessage(messages *model.AutoReviewMessages, rationale string) string {
	if messages != nil && messages.RejectionInstructions != nil {
		return "This action was rejected due to unacceptable risk.\nReason: " + rationale + "\n" + *messages.RejectionInstructions
	}
	return rationale
}

// guardianTimeoutMessage mirrors Rust guardian_timeout_message (#39741): the
// acting model's timeout_instructions replace the default when present.
func guardianTimeoutMessage(messages *model.AutoReviewMessages) string {
	if messages != nil && messages.TimeoutInstructions != nil {
		return *messages.TimeoutInstructions
	}
	return state.GuardianTimeoutMessage()
}

func (r *modelGuardianReviewer) modelForTurn(threadID, turnID string) string {
	if r == nil || r.model == nil {
		return ""
	}
	return strings.TrimSpace(r.model(threadID, turnID))
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
	policy := state.CircuitBreakerPolicyStandard
	if r.specialty != nil && strings.TrimSpace(r.specialty(threadID, turnID)) == model.ModelSpecialtyCyber {
		policy = state.CircuitBreakerPolicyCyber
	}
	if action := r.breaker.RecordDenialWithPolicy(turnID, policy); action.InterruptTurn && r.interrupt != nil {
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
		// Rust #39791: standalone function_call_output items (no call id) are
		// external context; render them as tool results with the namespaced
		// tool name and a placeholder for non-text content.
		if strings.EqualFold(strings.TrimSpace(item.Type), "function_call_output") && strings.TrimSpace(item.CallID) == "" {
			name := strings.TrimSpace(item.Name)
			if namespace := strings.TrimSpace(item.Namespace); namespace != "" {
				name = namespace + "." + name
			}
			if name == "" {
				name = "tool"
			}
			text := strings.TrimSpace(item.Text)
			if text == "" {
				text = "[non-text output]"
			}
			lines = append(lines, "tool "+name+" result: "+text)
			continue
		}
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
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := validateResponseItemImageURLs(params.Items); err != nil {
		return nil, err
	}
	// Ephemeral forks (used by TUI side conversations) live only in the
	// runtime thread manager. They have no persisted store entry, so injected
	// boundary items must be appended through the ephemeral runtime path.
	now := runtimeRouterNow(r)
	items := make([]session.Item, 0, len(params.Items))
	for i, raw := range params.Items {
		item, err := sessionItemFromRaw(raw, now, i)
		if err != nil {
			return nil, err
		}
		if r.services.ThreadRouter != nil &&
			r.services.ThreadRouter.retainClientDeveloperMessages != nil &&
			r.services.ThreadRouter.retainClientDeveloperMessages() {
			markClientAuthoredDeveloperItem(&item)
		}
		items = append(items, item)
	}
	r.markThreadMemoryPollutedOnExternalContext(params.ThreadID, items)
	if _, ok := r.appendEphemeralThreadItems(session.ThreadID(params.ThreadID), items); ok {
		return &ThreadInjectItemsResponse{}, nil
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

// resetGuardianAfterParentCompaction restarts the thread's Guardian review
// session after a parent history rewrite when guardian_reuse_parent_compaction
// is enabled (Rust c2bcb9a26b). The latest reusable compaction response ID
// seeds the new session; an absent compaction keeps the existing reviewer.
func (r *RuntimeRouter) resetGuardianAfterParentCompaction(threadID string, compacted *compact.Result) {
	if r == nil || r.services.GuardianReviewer == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	params := &turn.TurnStartParams{ThreadID: strings.TrimSpace(threadID)}
	if record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), false, false); err == nil && record != nil {
		params.CWD = record.Metadata.CWD
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil || cfg == nil {
		return
	}
	if !features.Enabled(cfg.FeatureSettings(), "guardian_reuse_parent_compaction") {
		return
	}
	runner, ok := r.services.GuardianReviewer.(*modelGuardianReviewer)
	if !ok {
		return
	}
	sessionRunner, ok := runner.agent.(*guardianSessionRunner)
	if !ok {
		return
	}
	responseID := ""
	if compacted != nil && strings.TrimSpace(compacted.ResponseID) != "" {
		// Rust #38980: reuse the latest encrypted parent compaction only when
		// its complete serialized item fits within the configured
		// max_parent_compaction_tokens bound (default 25,000 tokens; 4 bytes
		// per token, mirroring TruncationPolicy::Tokens(n).byte_budget()).
		// Go approximates the serialized item size with the compaction summary
		// bytes. An oversized latest compaction fails closed: the existing
		// reviewer context is preserved instead of seeding with older context.
		responseID = guardianParentCompactionResponseID(compacted, cfg.GuardianV2MaxParentCompactionTokens())
	}
	sessionRunner.ResetAfterParentCompaction(responseID)
}

// guardianParentCompactionResponseID returns the reusable compaction response
// ID when the latest encrypted parent compaction fits within the configured
// token bound, and an empty ID when it is oversized (fail closed, #38980). The
// serialized item size is approximated by the compaction summary bytes (4
// bytes per token, mirroring Rust TruncationPolicy::Tokens byte_budget).
func guardianParentCompactionResponseID(compacted *compact.Result, maxTokens int) string {
	if compacted == nil || strings.TrimSpace(compacted.ResponseID) == "" {
		return ""
	}
	if maxTokens <= 0 {
		maxTokens = 25_000
	}
	if len(compacted.Summary) > maxTokens*4 {
		return ""
	}
	return strings.TrimSpace(compacted.ResponseID)
}
