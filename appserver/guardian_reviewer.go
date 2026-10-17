package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"codex_go/agent"
	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/config"
	codexctx "codex_go/context"
	"codex_go/features"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/retainedctx"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

type GuardianReviewer interface {
	Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error)
}

type modelGuardianReviewer struct {
	agent         model.AgentRunner
	store         *state.ReviewStore
	breaker       *state.CircuitBreaker
	scoreMu       sync.Mutex
	scoreProgress map[string]*guardianScoreProgress
	// admittedImagesMu guards admittedImages, the per-thread ledger of Guardian
	// image references a reviewer conversation already received. Rust's
	// ComposedContext::retain_new_images (#54278) proves delivery only from
	// admitted history, so the ledger - not the pending prompt - decides which
	// images a later review injects.
	admittedImagesMu sync.Mutex
	admittedImages   map[string]*codexctx.GuardianAdmittedImages
	// reviewedEvidenceMu guards reviewedEvidence, the per-thread node_repl
	// evidence cursor. Rust's reviewer session asks for the evidence admitted
	// after its last review, so the Go transcript follows the same cursor
	// (#54278) instead of replaying every retained response.
	reviewedEvidenceMu sync.Mutex
	reviewedEvidence   map[string]uint64
	maxToolCallLag     int
	// maxToolCallLagFor, when set, resolves the bound for the review's turn
	// (configured value -> model catalog default -> Rust's default).
	maxToolCallLagFor func(threadID, turnID string) int
	notify            func(threadID string, event *state.Event)
	interrupt         func(threadID, turnID, reason string)
	transcript        func(threadID string) []string
	// transcriptEntries carries host-attributed entries, including native
	// encrypted agent messages that cannot be rendered as text (#49038).
	transcriptEntries func(threadID string) []state.GuardianTranscriptEntry
	transcriptFormat  func(threadID string) state.GuardianTranscriptFormat
	// feedbackCaptureEnabled gates the bounded failed-review records exported
	// by log-enabled feedback uploads (#41936): ephemeral threads and disabled
	// feedback collection retain nothing.
	feedbackCaptureEnabled func(threadID string) bool
	// reviewModel resolves the catalog-backed reviewer selection for the
	// reviewed turn: the model, its request-level reasoning effort, and the
	// reviewer's policy instructions (Rust's resolve_review_model /
	// select_review_model plus build_guardian_review_session_config).
	reviewPlan                 func(threadID, turnID string) guardianReviewPlan
	specialty                  func(threadID, turnID string) string
	nodeReplAutoReviewRequired func(threadID, turnID string) bool
	fullAccess                 func(threadID, turnID string) bool
	approvalsReviewer          func(threadID, turnID string) string
	environment                func(context.Context, string, string) ([]any, error)
	permissionProfile          func(threadID, turnID string) *sandbox.PermissionProfile
	// turnPermissionProfile resolves the reviewed turn's own profile and cwd for
	// the permission evidence, which is distinct from the read-only profile the
	// reviewer's session uses, plus the reviewed environment's selection id when
	// one was resolved. The last argument is the tool payload's own environment
	// id when it names one (Rust's `for_tool`).
	turnPermissionProfile func(threadID, turnID, environmentID string) (*sandbox.PermissionProfile, string, *string)
	// latestResponseID resolves the reviewed turn's newest response id for the
	// review request's `parent_response_id` client metadata (Rust #45441).
	latestResponseID      func(threadID, turnID string) string
	nodeReplEvidence      func(threadID string, reviewedSequence uint64) *codexctx.NodeReplReviewEvidenceFragment
	rootUserAuthorization func(threadID, turnID string) []string
	// retainedContext resolves the thread's retained evidence, which the review
	// prompt renders as the retained user-instruction section (Rust's
	// SectionHistory::retained_context and the RetainedUserInstructionsSection).
	retainedContext func(threadID, turnID string) *retainedctx.RetainedContext
	// Rust #42807 gives extension contributors a request-scoped `decide` hook
	// (Allow / Reviewed / AskUser) plus structured reasons for a fresh review.
	// The Go port has no ApprovalReviewContributor extension registry; the
	// equivalent decision policy lives here as synchronous fast paths (Full
	// Access approval, user-mode node_repl auto-accept) feeding the shared
	// model review, and host permission enforcement stays outside the
	// reviewer. Structured fresh-review reasons are therefore not exposed to
	// contributors, which makes #42807 rust-coupled for the Go architecture.
	fastDecision func(context.Context, string, string, string)
	timeout      time.Duration
	// installationID supplies the Codex installation id for the review
	// request's turn metadata (Rust #44298).
	installationID func() string
	// metrics receives the guardian review metrics (Rust's
	// emit_guardian_review_metrics).
	metrics telemetry.TurnMetricSink
	// subagentThread reports whether the review's thread is a delegated
	// subagent, which selects Rust's delegated_subagent approval source.
	subagentThread func(threadID string) bool
	// warn surfaces a guardian warning notification (Rust's
	// EventMsg::GuardianWarning) for a timeout or a failed review.
	warn func(threadID string, message string)
}

// defaultGuardianMaxToolCallLag mirrors Rust
// GuardianV2Config::DEFAULT_MAX_TOOL_CALL_LAG (#39001).
const defaultGuardianMaxToolCallLag = 2

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

// recordNonDenial mirrors Rust's record_guardian_non_denial: only a completed
// assessment with outcome deny counts as a policy denial, so an approved review
// and every review failure reset the denial window instead.
func (r *modelGuardianReviewer) recordNonDenial(turnID string) {
	if r == nil || r.breaker == nil {
		return
	}
	r.breaker.RecordNonDenial(turnID)
}

// guardianReviewFailureInstructions mirrors Rust's REVIEW_FAILURE_INSTRUCTIONS.
const guardianReviewFailureInstructions = "The action was not executed because automatic approval review could not be completed. " +
	"This is a review failure, not a determination that the action is unsafe. " +
	"Do not bypass the approval check; resolve the error or ask the user for guidance."

// guardianReviewFailureRationale mirrors Rust's failed-review event rationale
// (guardian-reviewer::complete_review): the assessment could not be completed,
// so the review fails closed. It is also the GuardianWarning text.
func guardianReviewFailureRationale(message string) string {
	return "Automatic approval review failed: " + strings.TrimSpace(message)
}

// guardianReviewFailureMessage mirrors Rust's failed-review rejection: the
// rationale, then the review-failure instructions.
func guardianReviewFailureMessage(message string) string {
	return guardianReviewFailureRationale(message) + "\n" + guardianReviewFailureInstructions
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

// maxToolCallLagValue resolves the stale-score bound for one review: the
// per-turn resolver (configured value -> model catalog default -> Rust's
// default) wins, then an explicit SetMaxToolCallLag, then the default.
func (r *modelGuardianReviewer) maxToolCallLagValue(threadID, turnID string) int {
	if r == nil {
		return defaultGuardianMaxToolCallLag
	}
	if r.maxToolCallLagFor != nil {
		if lag := r.maxToolCallLagFor(threadID, turnID); lag > 0 {
			return lag
		}
	}
	if r.maxToolCallLag > 0 {
		return r.maxToolCallLag
	}
	return defaultGuardianMaxToolCallLag
}

// guardianAdmittedImages returns the thread's admitted-image ledger, creating it
// on first use. Rust #54278 keeps the admitted history of each reviewer
// conversation, so the ledger is scoped per thread like the score progress.
func (r *modelGuardianReviewer) guardianAdmittedImages(threadID string) *codexctx.GuardianAdmittedImages {
	if r == nil {
		return nil
	}
	r.admittedImagesMu.Lock()
	defer r.admittedImagesMu.Unlock()
	if r.admittedImages == nil {
		r.admittedImages = map[string]*codexctx.GuardianAdmittedImages{}
	}
	ledger := r.admittedImages[threadID]
	if ledger == nil {
		ledger = codexctx.NewGuardianAdmittedImages()
		r.admittedImages[threadID] = ledger
	}
	return ledger
}

// guardianReviewedEvidenceSequence returns the highest node_repl evidence
// sequence the reviewer has already been shown for the thread, mirroring the
// transcript cursor of Rust's reviewer session (#54278).
func (r *modelGuardianReviewer) guardianReviewedEvidenceSequence(threadID string) uint64 {
	if r == nil {
		return 0
	}
	r.reviewedEvidenceMu.Lock()
	defer r.reviewedEvidenceMu.Unlock()
	return r.reviewedEvidence[threadID]
}

// noteGuardianReviewedEvidence advances the thread's evidence cursor. The
// reviewer session keeps the evidence it already admitted, so a later review
// asks only for what was admitted after this one (Rust #54278).
func (r *modelGuardianReviewer) noteGuardianReviewedEvidence(threadID string, sequence uint64) {
	if r == nil || sequence == 0 {
		return
	}
	r.reviewedEvidenceMu.Lock()
	defer r.reviewedEvidenceMu.Unlock()
	if r.reviewedEvidence == nil {
		r.reviewedEvidence = map[string]uint64{}
	}
	if sequence > r.reviewedEvidence[threadID] {
		r.reviewedEvidence[threadID] = sequence
	}
}

type guardianSessionRunner struct {
	mu       sync.Mutex
	agent    model.AgentRunner
	previous string
	seeded   string
	// priorReview mirrors Rust's ConversationState::cursor: set once review
	// input was admitted by the guardian session, even if inference failed. The
	// next review therefore reports had_prior_review_context = true.
	priorReview bool
	// completedReviews mirrors Rust's ConversationState::completed_review_count.
	// A failed review may admit its input without counting as completed (#54264).
	completedReviews int
}

// reviewSessionAttribution mirrors Rust's GuardianReviewSessionKind selection
// plus the prior-review cursor: a session seeded from the parent's compaction is
// an ephemeral fork, a session with a previous response is the reused trunk, and
// anything else is a new trunk.
func (r *guardianSessionRunner) reviewSessionAttribution() (kind string, hadPriorContext *bool) {
	if r == nil {
		kind = "none"
		return kind, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case strings.TrimSpace(r.seeded) != "":
		kind = "ephemeral_forked"
	case strings.TrimSpace(r.previous) != "":
		kind = "trunk_reused"
	default:
		kind = "trunk_new"
	}
	prior := r.priorReview
	return kind, &prior
}

// admitTranscript mirrors Rust's ConversationState::admit_transcript (#54264):
// input retained by the live conversation advances the transcript cursor even
// when inference failed, without counting as a completed review.
func (r *guardianSessionRunner) admitTranscript() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.priorReview = true
	r.mu.Unlock()
}

// completeReview mirrors Rust's ConversationState::complete_review: a completed
// assessment advances the cursor and the completed-review count.
func (r *guardianSessionRunner) completeReview() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.priorReview = true
	r.completedReviews++
	r.mu.Unlock()
}

// markPriorReview records that a review ran in this guardian session. Any
// submitted review turn admits its input, so a later review builds on it.
func (r *guardianSessionRunner) markPriorReview() {
	r.admitTranscript()
}

// completedReviewCount exposes Rust's completed_review_count for attribution and
// tests; admission without completion (#54264) leaves it unchanged.
func (r *guardianSessionRunner) completedReviewCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.completedReviews
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

// DropStaleParentCompaction clears the seeded parent-compaction checkpoint so
// strict Guardian thread-context sessions cannot reuse an unusable or
// incompatible parent checkpoint (Rust #42852).
func (r *guardianSessionRunner) DropStaleParentCompaction() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.previous = ""
	r.seeded = ""
	r.mu.Unlock()
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
	// Rust #51330 (f5fa209bb0, ext/guardian-reviewer/src/routing.rs): the total
	// decision timer wraps the complete approval decision -- fast decisions,
	// preparation failures and cancellation, not just the model review that
	// emitGuardianReviewMetrics times. Rust's `decide` has no unavailable arm, so
	// the guard above stays outside the timer.
	decisionStartedAt := time.Now()
	defer r.recordGuardianDecisionDuration(decisionStartedAt)
	// Rust #39001: skip approval review when the latest risk score lags by more
	// than max_tool_call_lag tool calls. The Go simplified reviewer fails closed
	// (no stale-score auto-approval), mirroring the stale-data guard while
	// keeping the established refuse-approval convention.
	if lag := r.maxToolCallLagValue(threadID, turnID); lag > 0 && r.scoreLag(threadID) > lag {
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
	// The review attempt starts here: Rust builds ReviewReport/tracking before
	// the prompt, and the metrics duration is measured from that point.
	reviewStartedAt := time.Now().UTC()
	attribution := r.newGuardianReviewAttribution(threadID, turnID, action)
	feedbackCapture := &guardianReviewFeedbackCapture{}
	defer func() {
		if r.feedbackCaptureEnabled != nil && !r.feedbackCaptureEnabled(threadID) {
			return
		}
		feedbackCapture.record(threadID, turnID, attribution, action)
	}()
	contextChanged := false
	emitReviewMetrics := func() {
		attribution.ContextChanged = contextChanged
		r.emitGuardianReviewMetrics(attribution, guardianReviewMetricsDurationMS(reviewStartedAt.UnixMilli(), time.Now().UTC()))
	}
	store := r.store
	if store == nil {
		store = state.NewReviewStore()
	}
	event, err := store.Start(turnID, targetItemID, action)
	if err != nil {
		attribution.Decision = state.DecisionAborted
		attribution.TerminalStatus = "aborted"
		attribution.FailureReason = "session_error"
		emitReviewMetrics()
		return state.DecisionAborted, "", err
	}
	r.emit(threadID, event)
	timeout := r.timeout
	if timeout <= 0 {
		timeout = state.ReviewTimeout
	}
	reviewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	plan := r.planForTurn(threadID, turnID)
	feedbackCapture.model = plan.Selection.Model
	feedbackCapture.instructions = plan.Instructions
	// Rust #53987/#53797: retries share one attempt budget and deadline. A
	// changed local review context is stale authorization, refreshed without
	// the service backoff; parse failures keep Rust's exponential backoff.
	finishTimedOut := func() (state.ReviewDecision, string, error) {
		completed, finishErr := store.Timeout(event.ID)
		if finishErr == nil {
			r.emit(threadID, completed)
		}
		// Rust records a non-denial for a failed review: only a completed
		// assessment with outcome deny counts as a policy denial.
		r.recordNonDenial(turnID)
		attribution.Decision = state.DecisionTimedOut
		attribution.TerminalStatus = "timed_out"
		attribution.FailureReason = "timeout"
		emitReviewMetrics()
		timeoutMessage := guardianTimeoutMessage(plan.AutoReview)
		// Rust's timed-out completion records the review rationale on the
		// assessment event and publishes it as the GuardianWarning, while the
		// tool rejection carries the resolved timeout instructions.
		r.emitWarning(threadID, state.GuardianTimeoutRationale())
		return state.DecisionTimedOut, timeoutMessage, finishErr
	}
	finishAborted := func() (state.ReviewDecision, string, error) {
		completed, finishErr := store.Abort(event.ID, "Guardian review was aborted.")
		if finishErr == nil {
			r.emit(threadID, completed)
		}
		r.recordNonDenial(turnID)
		attribution.Decision = state.DecisionAborted
		attribution.TerminalStatus = "aborted"
		attribution.FailureReason = "cancelled"
		emitReviewMetrics()
		return state.DecisionAborted, "", finishErr
	}
	attempts := 0
	var assessment *state.Assessment
	var response *model.AgentResponse
reviewAttempt:
	attempts++
	var transcript []string
	var transcriptEntries []state.GuardianTranscriptEntry
	transcriptFormat := state.GuardianTranscriptFormatLine
	if r.transcriptFormat != nil {
		transcriptFormat = r.transcriptFormat(threadID)
	}
	if r.transcriptEntries != nil {
		transcriptEntries = r.transcriptEntries(threadID)
		transcript = state.RenderGuardianTranscript(transcriptEntries, transcriptFormat)
	} else if r.transcript != nil {
		transcript = r.transcript(threadID)
	}
	nodeReplAutoReviewRequired := false
	if r.nodeReplAutoReviewRequired != nil {
		nodeReplAutoReviewRequired = r.nodeReplAutoReviewRequired(threadID, turnID)
	}
	var nodeReplEvidence *codexctx.NodeReplReviewEvidenceFragment
	if r.nodeReplEvidence != nil {
		nodeReplEvidence = r.nodeReplEvidence(threadID, r.guardianReviewedEvidenceSequence(threadID))
	}
	promptNodeReplEvidence := nodeReplEvidence
	if nodeReplEvidence != nil && nodeReplEvidence.HasImages() {
		promptNodeReplEvidence = nil
	}
	// Rust's Guardian permission evidence: the reviewed environment's active
	// profile supplies the denied read paths and globs the reviewer must not
	// approve an escalation to read (core/src/guardian/permissions.rs).
	var permissionContext *state.PermissionContext
	if r.turnPermissionProfile != nil {
		if profile, cwd, environmentID := r.turnPermissionProfile(threadID, turnID, strings.TrimSpace(action.EnvironmentID)); profile != nil {
			deniedPaths := sandbox.UnreadableRootsWithCWD(profile, cwd)
			deniedGlobs := sandbox.UnreadableGlobsWithCWD(profile, cwd)
			if environmentID != nil || len(deniedPaths) > 0 || len(deniedGlobs) > 0 {
				permissionContext = &state.PermissionContext{
					EnvironmentID: environmentID,
					DeniedPaths:   deniedPaths,
					DeniedGlobs:   deniedGlobs,
				}
			}
		}
	}
	// Rust's review prompt renders the retained user-instruction section from the
	// session's retained context, so the reviewer sees the thread's original
	// instructions even after compaction.
	var retainedContext *retainedctx.RetainedContext
	if r.retainedContext != nil {
		retainedContext = r.retainedContext(threadID, turnID)
	}
	// The approval reviewer is the synchronous, stateful consumer, so it renders
	// the retained snapshot in Rust's synchronous layout (#51627): one retained
	// section ahead of the transcript, with the assistant context inside it. The
	// asynchronous independent snapshot's split belongs to ActionPresentationAsync.
	buildOptions := state.BuildPromptOptions{
		Presentation:      state.ActionPresentationSyncFull,
		TranscriptFormat:  transcriptFormat,
		NodeReplEvidence:  promptNodeReplEvidence,
		PermissionContext: permissionContext,
		RetainedContext:   retainedContext,
	}
	var prompt string
	var promptParts []state.GuardianPromptPart
	if transcriptEntries != nil {
		promptParts, err = state.BuildPromptPartsWithOptions(action, transcriptEntries, buildOptions)
	} else {
		prompt, err = state.BuildPromptWithOptions(action, transcript, buildOptions)
	}
	var rootAuthorization []string
	if err == nil && r.rootUserAuthorization != nil {
		rootAuthorization = r.rootUserAuthorization(threadID, turnID)
		if len(rootAuthorization) > 0 {
			buildOptions.RootUserAuthorization = rootAuthorization
			if transcriptEntries != nil {
				promptParts, err = state.BuildPromptPartsWithOptions(action, transcriptEntries, buildOptions)
			} else {
				prompt, err = state.BuildPromptWithOptions(action, transcript, buildOptions)
			}
		}
	}
	if err != nil {
		rationale := guardianReviewFailureRationale(err.Error())
		message := guardianReviewFailureMessage(err.Error())
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "prompt_build_error"
		emitReviewMetrics()
		r.emitWarning(threadID, rationale)
		return state.DecisionDenied, message, nil
	}
	var promptInputItems []any
	if promptParts != nil {
		prompt, promptInputItems = guardianPromptDelivery(promptParts)
	}
	inputItems := append([]any(nil), promptInputItems...)
	if r.environment != nil {
		environmentItems, environmentErr := r.environment(reviewCtx, threadID, turnID)
		err = environmentErr
		if err != nil {
			rationale := guardianReviewFailureRationale(err.Error())
			message := guardianReviewFailureMessage(err.Error())
			completed, _ := store.FailClosed(event.ID, rationale)
			r.emit(threadID, completed)
			r.recordNonDenial(turnID)
			attribution.Decision = state.DecisionDenied
			attribution.TerminalStatus = "failed_closed"
			attribution.FailureReason = "session_error"
			emitReviewMetrics()
			r.emitWarning(threadID, rationale)
			return state.DecisionDenied, message, nil
		}
		inputItems = append(inputItems, environmentItems...)
	}
	var (
		admittedLedger      *codexctx.GuardianAdmittedImages
		admittedImages      []codexctx.GuardianImageRef
		reviewedEvidence    uint64
		hasReviewedEvidence bool
	)
	if nodeReplEvidence != nil && nodeReplEvidence.HasImages() {
		// Rust ComposedContext::retain_new_images (#54278): a reviewer that already
		// admitted an image must not be shown it again. The host-prepared reference
		// is compared against admitted history ignoring image detail, so a repeated
		// reference in newer evidence is dropped while that evidence's text stays.
		ledger := r.guardianAdmittedImages(threadID)
		newImages := ledger.RetainNewImages(nodeReplEvidence.ImageRefs(), nil)
		keepImage := func(ref codexctx.GuardianImageRef) bool {
			for _, candidate := range newImages {
				if candidate == ref {
					return true
				}
			}
			return false
		}
		for _, item := range nodeReplEvidence.MultimodalInputItemsFiltered(keepImage) {
			inputItems = append(inputItems, item)
		}
		admittedLedger = ledger
		admittedImages = newImages
	}
	if nodeReplEvidence != nil {
		reviewedEvidence = nodeReplEvidence.Sequence()
		hasReviewedEvidence = true
	}
	// Rust ensure_guardian_node_repl_policy: the node-REPL rules are their own
	// unmarked developer fragment, injected only when the reviewed action is a
	// `js` call on a node-repl-backed server and the parent model requires
	// computer-use review.
	if nodeReplAutoReviewRequired && isNodeReplJSApprovalAction(action) {
		if fragment := codexctx.NewGuardianNodeReplPolicy(plan.NodeReplPolicy); fragment != nil {
			if item := buildRenderedMessage(codexctx.Render(fragment)); item != nil {
				inputItems = append(inputItems, item)
			}
		}
	}
	reviewRequest := &model.AgentRequest{
		Prompt:          prompt,
		Instructions:    plan.Instructions,
		InputItems:      inputItems,
		Model:           plan.Selection.Model,
		ReasoningEffort: plan.Selection.ReasoningEffort,
		// Rust #52689 forwards the parent turn's Cyber program to the reviewer
		// (turn_start_options.cyber_access_program), normalized to Blue for
		// either Daybreak selection.
		CyberAccessProgram: plan.CyberAccessProgram,
		TaskKind:           model.AgentTaskReview,
		ThreadID:           threadID,
		TurnID:             turnID,
		Originator:         "guardian",
		OutputSchema:       guardianAssessmentOutputSchema(),
		ClientMetadata:     r.reviewClientMetadata(threadID, turnID, targetItemID),
	}
	if r.permissionProfile != nil {
		reviewRequest.PermissionProfile = r.permissionProfile(threadID, turnID)
	}
	feedbackCapture.history = guardianFeedbackHistoryInput(prompt, inputItems)
	if runner, ok := r.agent.(*guardianSessionRunner); ok {
		runner.SeedForReview(reviewRequest)
	}
	response, err = r.agent.Run(reviewCtx, reviewRequest)
	if runner, ok := r.agent.(*guardianSessionRunner); ok {
		// Rust records the reviewed-transcript cursor once a review ran in the
		// guardian session.
		runner.markPriorReview()
	}
	if err == nil && r.guardianReviewContextChanged(threadID, turnID, transcript, transcriptEntries, rootAuthorization) {
		// Rust #53797 widens the invalidation predicate to local or root
		// review context. Rust #53987 treats it as stale authorization, a local
		// evidence refresh rather than a failed service request: yield, check
		// cancellation and the shared deadline, then retry without backoff.
		contextChanged = true
		if attempts < guardianMaxReviewAttempts {
			runtime.Gosched()
			if waitErr := reviewCtx.Err(); waitErr != nil {
				err = waitErr
			} else {
				goto reviewAttempt
			}
		}
	}
	if err != nil && attempts < guardianMaxReviewAttempts && guardianReviewRetryableError(err) {
		// Rust should_retry_guardian_review: recoverable session failures keep
		// the same attempt budget and pay the service backoff, honouring the
		// server's Retry-After when it asks for a longer wait.
		delay := guardianReviewRetryDelay(attempts)
		if retryAfter := codexapi.RetryDelay(err); retryAfter > delay {
			delay = retryAfter
		}
		if waitErr := waitGuardianReviewRetry(reviewCtx, delay); waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) {
				return finishTimedOut()
			}
			return finishAborted()
		}
		goto reviewAttempt
	}
	if err == nil {
		// Delivery accounting (Rust guardian-context/src/images.rs:29: "Only
		// admitted history proves delivery; images kept here are not committed
		// yet"). The retained images and the transcript snapshot count as
		// delivered only once the reviewer request actually reached the session:
		// a failed, timed-out or cancelled review leaves them retained, so the
		// next review still injects that evidence (Independent probe V-b5).
		if admittedLedger != nil {
			admittedLedger.Admit(admittedImages...)
		}
		if hasReviewedEvidence {
			r.noteGuardianReviewedEvidence(threadID, reviewedEvidence)
		}
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reviewCtx.Err(), context.DeadlineExceeded) {
			return finishTimedOut()
		}
		if errors.Is(err, context.Canceled) || errors.Is(reviewCtx.Err(), context.Canceled) {
			return finishAborted()
		}
		rationale := guardianReviewFailureRationale(err.Error())
		message := guardianReviewFailureMessage(err.Error())
		failedClosed, _ := store.FailClosed(event.ID, rationale)
		r.emit(threadID, failedClosed)
		r.recordNonDenial(turnID)
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "session_error"
		emitReviewMetrics()
		r.emitWarning(threadID, rationale)
		return state.DecisionDenied, message, nil
	}
	assessment, err = state.ParseAssessment([]byte(guardianAssessmentText(response)))
	if err != nil && attempts < guardianMaxReviewAttempts {
		// Rust retries Parse failures inside the same attempt budget.
		if waitErr := waitGuardianReviewRetry(reviewCtx, guardianReviewRetryDelay(attempts)); waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) {
				return finishTimedOut()
			}
			return finishAborted()
		}
		goto reviewAttempt
	}
	if err != nil {
		feedbackCapture.decisionText = guardianAssessmentText(response)
		rationale := guardianReviewFailureRationale(err.Error())
		message := guardianReviewFailureMessage(err.Error())
		failedClosed, _ := store.FailClosed(event.ID, rationale)
		r.emit(threadID, failedClosed)
		r.recordNonDenial(turnID)
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "parse_error"
		emitReviewMetrics()
		r.emitWarning(threadID, rationale)
		return state.DecisionDenied, message, nil
	}
	completed, err := store.Complete(event.ID, *assessment)
	if err != nil {
		attribution.Decision = state.DecisionAborted
		attribution.TerminalStatus = "aborted"
		attribution.FailureReason = "session_error"
		emitReviewMetrics()
		return state.DecisionAborted, "", err
	}
	r.emit(threadID, completed)
	if runner, ok := r.agent.(*guardianSessionRunner); ok {
		// Rust complete_review: a completed assessment advances the transcript
		// cursor and the completed-review count, unlike a failed-but-admitted
		// attempt which only admits its input (#54264).
		runner.completeReview()
	}
	decision := state.DecisionFromEvent(completed)
	r.recordDecision(threadID, turnID, decision, assessment.Rationale)
	r.markScored(threadID)
	riskLevel := assessment.RiskLevel
	userAuthorization := assessment.UserAuthorization
	outcome := assessment.Outcome
	attribution.Decision = decision
	if decision == state.DecisionApproved {
		attribution.TerminalStatus = "approved"
	} else {
		attribution.TerminalStatus = "denied"
	}
	attribution.FailureReason = "none"
	attribution.RiskLevel = &riskLevel
	attribution.UserAuthorization = &userAuthorization
	attribution.Outcome = &outcome
	if response != nil {
		usage := response.Usage
		attribution.TokenUsage = &usage
	}
	emitReviewMetrics()
	rationale := assessment.Rationale
	if decision == state.DecisionDenied {
		feedbackCapture.decisionText = guardianAssessmentText(response)
		rationale = guardianRejectionMessage(plan.AutoReview, rationale)
	}
	return decision, rationale, nil
}

// reviewClientMetadata builds the guardian review request's client metadata.
// Rust #44298 attributes guardian reviews to the `guardian_review` turn trigger
// and emits it in x-codex-turn-metadata; Go's reviewer issues a direct agent
// request, so the review reuses the parent turn's identity.
func (r *modelGuardianReviewer) reviewClientMetadata(threadID, turnID, targetItemID string) map[string]string {
	installationID := ""
	if r != nil && r.installationID != nil {
		installationID = strings.TrimSpace(r.installationID())
	}
	metadata := codexapi.NewClientMetadata(installationID, threadID, threadID, "")
	metadata.RequestKind = codexapi.ClientRequestTurn
	metadata.TurnTrigger = "guardian_review"
	metadata.TurnID = turnID
	metadata.ParentTurnID = turnID
	metadata.RootTurnID = turnID
	metadata.SubagentHeader = "guardian"
	targetItemID = strings.TrimSpace(targetItemID)
	if targetItemID != "" {
		metadata.Extra["target_item_id"] = targetItemID
	}
	client := metadata.ClientMetadata()
	if targetItemID != "" {
		// Preserve the historical top-level key alongside the turn metadata.
		client["target_item_id"] = targetItemID
	}
	// Rust #45441: a review request reports the reviewed turn's latest response
	// id as `parent_response_id`, and omits the key when the turn has none yet.
	if r != nil && r.latestResponseID != nil {
		if parent := strings.TrimSpace(r.latestResponseID(threadID, turnID)); parent != "" {
			client["parent_response_id"] = parent
		}
	}
	return client
}

// guardianRejectionMessage mirrors Rust's denied completion
// (guardian-reviewer::complete_review -> codex_prompts::render_guardian_rejection):
// the acting model's rejection_instructions replace the bundled default only
// when the field is present, and the rationale is always rendered through the
// rejection feedback wrapper.
func guardianRejectionMessage(messages *model.AutoReviewMessages, rationale string) string {
	instructions := state.GuardianRejectionInstructions()
	if messages != nil && messages.RejectionInstructions != nil {
		instructions = *messages.RejectionInstructions
	}
	return state.RenderGuardianRejection(rationale, instructions)
}

// guardianTimeoutMessage mirrors Rust's ReviewDecision::TimedOut handling: the
// tool rejection carries the acting model's timeout_instructions, or the
// bundled default when the catalog omits the field.
func guardianTimeoutMessage(messages *model.AutoReviewMessages) string {
	if messages != nil && messages.TimeoutInstructions != nil {
		return *messages.TimeoutInstructions
	}
	return state.GuardianTimeoutInstructions()
}

// isNodeReplJSApprovalAction mirrors Rust's node-REPL policy eligibility: a `js`
// tool call on a node-repl-backed server (node_repl or cua_repl).
func isNodeReplJSApprovalAction(action state.Action) bool {
	return action.Type == "mcp_tool_call" &&
		mcp.IsNodeReplBackedServer(strings.TrimSpace(action.Server)) &&
		strings.TrimSpace(action.ToolName) == "js"
}

// planForTurn resolves the reviewer plan the review request uses. A reviewer
// without a resolver keeps the pre-selection shape: no explicit model, effort,
// or instructions, so the agent's own defaults apply.
func (r *modelGuardianReviewer) planForTurn(threadID, turnID string) guardianReviewPlan {
	if r == nil || r.reviewPlan == nil {
		return guardianReviewPlan{}
	}
	return r.reviewPlan(threadID, turnID)
}

// modelForTurn keeps the model-only view used by review metrics and telemetry.
func (r *modelGuardianReviewer) modelForTurn(threadID, turnID string) string {
	return strings.TrimSpace(r.planForTurn(threadID, turnID).Selection.Model)
}

func (r *modelGuardianReviewer) emit(threadID string, event *state.Event) {
	if r != nil && r.notify != nil && event != nil {
		r.notify(threadID, event)
	}
}

// emitWarning surfaces a guardian warning notification (Rust's
// EventMsg::GuardianWarning for a timeout or a failed review).
func (r *modelGuardianReviewer) emitWarning(threadID, message string) {
	if r == nil || r.warn == nil || strings.TrimSpace(message) == "" {
		return
	}
	r.warn(threadID, message)
}

func (r *modelGuardianReviewer) recordDecision(threadID, turnID string, decision state.ReviewDecision, reason string) {
	if r == nil || r.breaker == nil {
		return
	}
	if decision == state.DecisionApproved {
		r.recordNonDenial(turnID)
		return
	}
	policy := state.CircuitBreakerPolicyStandard
	if r.specialty != nil && strings.TrimSpace(r.specialty(threadID, turnID)) == model.ModelSpecialtyCyber {
		policy = state.CircuitBreakerPolicyCyber
	}
	action := r.breaker.RecordDenialWithPolicy(turnID, policy)
	if !action.InterruptTurn {
		return
	}
	// Rust's ReviewDenials::record_denial returns the denial-limit warning only
	// once per turn, and the reviewer counts that interruption immediately
	// before it interrupts the turn (Rust #51334,
	// codex-rs/ext/guardian-reviewer/src/review.rs).
	if r.metrics != nil {
		r.metrics.Counter(telemetry.GuardianDenialLimitReachedMetric, 1, nil)
	}
	if r.interrupt != nil {
		r.interrupt(threadID, turnID, reason)
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
	format := state.GuardianTranscriptFormatLine
	if r != nil {
		format = r.guardianTranscriptFormatForThread(threadID)
	}
	return state.RenderGuardianTranscript(r.guardianReviewTranscriptEntries(threadID), format)
}

// guardianTranscriptFormatForThread resolves the configured transcript
// rendering for a thread (#51465).
func (r *RuntimeRouter) guardianTranscriptFormatForThread(threadID string) state.GuardianTranscriptFormat {
	if r == nil {
		return state.GuardianTranscriptFormatLine
	}
	if cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: strings.TrimSpace(threadID)}); err == nil && cfg != nil {
		return state.ParseGuardianTranscriptFormat(cfg.GuardianV2TranscriptFormat())
	}
	return state.GuardianTranscriptFormatLine
}

// guardianPromptDelivery splits ordered prompt parts into the request prompt
// and the input items that follow it. Native encrypted agent messages stay
// native response items so their ciphertext reaches the reviewer unchanged
// (#49038).
func guardianPromptDelivery(parts []state.GuardianPromptPart) (string, []any) {
	prompt := ""
	start := 0
	if len(parts) > 0 && len(parts[0].Native) == 0 {
		prompt = parts[0].Text
		start = 1
	}
	items := make([]any, 0, len(parts)-start)
	for _, part := range parts[start:] {
		if len(part.Native) > 0 {
			items = append(items, json.RawMessage(part.Native))
			continue
		}
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		items = append(items, map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": part.Text},
			},
		})
	}
	return prompt, items
}

const (
	// guardianNativeAgentMessageMaxBytes mirrors Rust's sync transcript
	// native-message budget (#49038): the serialized item must fit
	// TruncationPolicy::Tokens(message_tokens=5_000).byte_budget(), otherwise
	// the whole message becomes an omission notice at the same position.
	guardianNativeAgentMessageMaxBytes = 5_000 * 4
	guardianTranscriptOmissionNotice   = "Some conversation entries were omitted."
)

// guardianReviewTranscriptEntries collects the bounded transcript with the
// host-assigned attribution both formats share: author, label and position come
// from the stored item, never from its text (#51465).
func (r *RuntimeRouter) guardianReviewTranscriptEntries(threadID string) []state.GuardianTranscriptEntry {
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, true)
	if err != nil || record == nil {
		return nil
	}
	const maxLines = 12
	const maxChars = 4000
	entries := make([]state.GuardianTranscriptEntry, 0, maxLines)
	for i := len(record.Items) - 1; i >= 0 && len(entries) < maxLines; i-- {
		item := record.Items[i]
		// Rust #54316 (guardian-context/src/history.rs): compaction summaries are
		// excluded from the transcript while the surrounding user messages
		// survive, including a message whose text matches the summary.
		if guardianTranscriptExcludedItem(&item) {
			continue
		}
		// Rust #49038: encrypted agent messages carry evidence such as a
		// parent reply. They cannot be truncated, so the complete native item
		// is delivered in order, or replaced whole by an omission notice.
		if native, ok := guardianNativeAgentMessage(&item); ok {
			if len(native) > guardianNativeAgentMessageMaxBytes {
				entries = append(entries, state.GuardianTranscriptEntry{
					Author:   "assistant",
					LineRole: "assistant",
					Text:     guardianTranscriptOmissionNotice,
				})
			} else {
				entries = append(entries, state.GuardianTranscriptEntry{Author: "assistant", Native: native})
			}
			continue
		}
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
			entries = append(entries, state.GuardianTranscriptEntry{
				Author:   "tool",
				Label:    name,
				LineRole: "tool " + name + " result",
				Text:     text,
			})
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
		if guardianTranscriptIsCompactionSummary(&item) {
			// Rust #54281: local compaction stores generated summaries with the
			// `user` wire role, but a summary is not user authorization, so the
			// transcript attributes it to the assistant while real user
			// messages keep their attribution.
			role = "assistant"
		}
		if role != "" {
			entries = append(entries, state.GuardianTranscriptEntry{
				Author:   guardianTranscriptAuthor(role),
				Label:    guardianTranscriptLabel(role),
				LineRole: role,
				Text:     text,
			})
		} else {
			entries = append(entries, state.GuardianTranscriptEntry{
				Author: guardianTranscriptAuthor(role),
				Text:   text,
			})
		}
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	for index := range entries {
		entries[index].Index = index
	}
	return entries
}

// guardianTranscriptAuthor mirrors Rust's ConversationTranscriptEntryKind
// author mapping. Unknown roles stay assistant-authored with their role kept as
// a descriptive label.
func guardianTranscriptAuthor(role string) string {
	switch strings.TrimSpace(role) {
	case "user", "developer", "assistant", "tool":
		return strings.TrimSpace(role)
	default:
		return "assistant"
	}
}

// guardianTranscriptLabel keeps a role that differs from its author as the
// descriptive label Rust renders in JSON records.
func guardianTranscriptLabel(role string) string {
	trimmed := strings.TrimSpace(role)
	if trimmed == "" || trimmed == guardianTranscriptAuthor(trimmed) {
		return ""
	}
	return trimmed
}

// guardianTranscriptExcludedItem reports whether a session item is kept out of
// the Guardian review transcript. Reasoning, context-compaction and
// compaction-trigger items carry no guardrail evidence (Rust's TranscriptHistory
// drops every non-message item), so they never reach the reviewer. A copied
// manual approval is model context rather than the child's own trusted approval
// evidence (Rust guardian-context/src/transcript.rs::is_inherited_manual_approval,
// #52535), so it is dropped as well.
func guardianTranscriptExcludedItem(item *session.Item) bool {
	if item == nil {
		return false
	}
	switch strings.TrimSpace(item.Type) {
	case "reasoning", "context_compaction", "compaction_trigger":
		return true
	}
	return guardianTranscriptIsInheritedManualApproval(item)
}

// sessionItemIsManualApprovalMessage mirrors the role/marker half of Rust's
// guardian-context/src/transcript.rs::is_inherited_manual_approval (#52535): an
// explicit manual action approval is written as a developer message whose text
// begins with the shared manual-approval marker.
func sessionItemIsManualApprovalMessage(item *session.Item) bool {
	if item == nil {
		return false
	}
	switch strings.TrimSpace(item.Type) {
	case "message", "user_message", "userMessage":
	default:
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(item.Role), "developer") {
		return false
	}
	if sessionItemManualApprovalText(item.Text) {
		return true
	}
	for _, part := range item.Content {
		if sessionItemManualApprovalText(part.Text) {
			return true
		}
	}
	return false
}

// sessionItemManualApprovalText mirrors Rust's `text.starts_with(marker)` test.
// The stored text is verbatim, so the marker is compared as-is; the trimmed form
// keeps parity with the transcript's own text normalization.
func sessionItemManualApprovalText(text string) bool {
	if text == "" {
		return false
	}
	return strings.HasPrefix(text, state.DeniedActionApprovalPrefix) ||
		strings.HasPrefix(strings.TrimSpace(text), state.DeniedActionApprovalPrefix)
}

// guardianTranscriptIsInheritedManualApproval mirrors Rust
// guardian-context/src/transcript.rs::is_inherited_manual_approval (#52535): a
// manual approval copied from the parent carries inherited provenance and must
// not be recaptured as the child's own authorization evidence. Rust filters it
// before dropping envelope provenance, including when replaying review
// checkpoints; a Go session item keeps that provenance in its harness metadata.
func guardianTranscriptIsInheritedManualApproval(item *session.Item) bool {
	if !sessionItemIsManualApprovalMessage(item) {
		return false
	}
	metadata := harnessMetadataFromSessionItem(item)
	return metadata != nil && metadata.InheritedUserMessage
}

// guardianTranscriptIsCompactionSummary reports whether a session item is a
// generated compaction summary (Rust #54281): either the harness-owned content
// classification names the shared compaction summary kind, or Go's persisted
// compaction marker names it. Both are model context, not user evidence.
func guardianTranscriptIsCompactionSummary(item *session.Item) bool {
	if item == nil {
		return false
	}
	for _, kind := range sessionItemContentItemKinds(item) {
		if strings.EqualFold(kind, codexctx.CompactionSummaryKind) {
			return true
		}
	}
	return sessionItemIsCompactionOutput(item)
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

func (r *RuntimeRouter) interruptTurnForGuardianCircuitBreaker(threadID, turnID, reason string) {
	if r == nil {
		return
	}
	if active, ok := r.cancelActiveRuntimeTurn(threadID, turnID); ok {
		r.finishTurnInterruptedAnalytics(threadID, turnID, active.StartedAtMS, analyticsContextFromActiveRuntimeTurn(active), turnAbortReasonInterrupted)
		r.deliverGuardianInterruptionToParent(threadID, reason)
		return
	}
	_, _ = r.requireTurns().Interrupt(&turn.TurnInterruptParams{ThreadID: threadID, TurnID: turnID})
	r.deliverGuardianInterruptionToParent(threadID, reason)
}

// deliverGuardianInterruptionToParent forwards a strict-mode
// `tooManyDenials` interruption to a MultiAgentV2 child's parent (Rust #49312).
// Ordinary interruptions and the default circuit-break action stay silent.
func (r *RuntimeRouter) deliverGuardianInterruptionToParent(threadID, reason string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: threadID})
	if err != nil || cfg == nil || cfg.GuardianCircuitBreakAction() != "strict" {
		return
	}
	if !r.threadUsesMultiAgentV2(threadID) {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil || strings.TrimSpace(record.Metadata.AgentPath) == "" || record.ParentThreadID == "" {
		return
	}
	rootID, _ := r.runtimeAgentIdentity(threadID)
	parentPath := "/root"
	if parent, parentErr := r.threadRecord(record.ParentThreadID, true, false); parentErr == nil && parent != nil && strings.TrimSpace(parent.Metadata.AgentPath) != "" {
		parentPath = strings.TrimSpace(parent.Metadata.AgentPath)
	}
	message, ok := agent.FormatGuardianInterruptionMessage(
		parentPath,
		record.Metadata.AgentPath,
		firstNonEmpty(strings.TrimSpace(reason), "Guardian denied the action repeatedly."),
	)
	if !ok {
		return
	}
	input := map[string]any{
		"type": "agent_message", "author": record.Metadata.AgentPath, "recipient": parentPath,
		"content": []any{map[string]any{"type": "input_text", "text": message}},
	}
	parentID := string(record.ParentThreadID)
	if active := r.activeRuntimeTurnSnapshot(parentID); active != nil {
		r.recordMultiAgentResultDelivery(r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: parentID, TurnID: active.ID, InputItems: []any{input}}))
	} else {
		r.enqueueRuntimeAgentMessage(parentID, input)
		r.recordMultiAgentResultDelivery(nil)
	}
	r.notifyRuntimeAgentActivity(rootID, "Wait completed.")
}

// threadUsesMultiAgentV2 reports whether the thread's effective multi-agent
// version is V2, the only family whose Guardian interruptions reach a parent.
func (r *RuntimeRouter) threadUsesMultiAgentV2(threadID string) bool {
	if r == nil {
		return false
	}
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: threadID})
	if err != nil || cfg == nil {
		return false
	}
	agentsConfig, err := cfg.AgentsConfig(r.configBaseDirForAgents())
	if err != nil {
		return false
	}
	return r.runtimeMultiAgentVersionForThread(threadID, cfg, agentsConfig) == agent.VersionV2
}

func (r *RuntimeRouter) handleThreadApproveGuardianDeniedActionRuntime(request *Request) (*ThreadApproveGuardianDeniedActionResponse, error) {
	var params ThreadApproveGuardianDeniedActionParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	// Rust `thread_approve_guardian_denied_action_inner` loads the thread and
	// then enforces the direct-input policy before it submits the op.
	if err := r.ensureDirectInputAllowed(request, params.ThreadID); err != nil {
		return nil, err
	}
	var event state.Event
	if err := json.Unmarshal(params.Event, &event); err != nil {
		return nil, jsonRPCInvalidRequest("invalid Guardian denial event: " + err.Error())
	}
	// Rust deserializes the field into a typed GuardianAssessmentEvent, so a
	// non-object payload fails the request instead of silently no-opping.
	if trimmed := bytes.TrimSpace(params.Event); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, jsonRPCInvalidRequest("invalid Guardian denial event: expected an object")
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
// This is the Go equivalent of the sync30 Guardian history-retention and
// compaction cluster (#41846-#41858, #41861, #41870, #41879,
// #41931-#42085, #42290, #42579-#42852): review continuity is preserved
// through the bounded parent-compaction response ID plus model-hash
// compatibility (#42852), root-conversation authorization and node_repl
// evidence stay in the review prompt, and missing/oversized/incompatible
// checkpoints fail closed. Rust's thread-context/structured-subagent history
// machinery has no Go counterpart, so the cluster is satisfied by these Go
// mechanisms rather than per-PR ports.
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
	// Rust #47686/#47688/#47689/#47690: thread-owned Guardian context capture is
	// unconditional; the guardianv2.thread_context flag was promoted, then
	// retired as a removed compatibility no-op, so the Go gate is fixed on.
	threadContext := true
	reuseParentCompaction := features.Enabled(cfg.FeatureSettings(), "guardian_reuse_parent_compaction")
	if !threadContext && !reuseParentCompaction {
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
	if threadContext && responseID == "" {
		sessionRunner.DropStaleParentCompaction()
		return
	}
	if threadContext && compacted != nil && strings.TrimSpace(compacted.CompactionModelHash) != "" {
		currentHash := r.guardianReviewModelHashForTurn(threadID, compacted.Request.TurnID)
		if currentHash != "" && currentHash != strings.TrimSpace(compacted.CompactionModelHash) {
			sessionRunner.DropStaleParentCompaction()
			return
		}
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
	// Rust #53691: encrypted checkpoint payloads include sampled-token replay
	// caches, so budget their model-visible length at twelve ciphertext bytes per
	// token. The configured token limit and the previous 100k-token wire limit
	// are enforced independently.
	if item := latestGuardianParentCompactionItem(compacted); item != nil {
		wireBytes, ok := compact.CompactionItemWireBytes(item)
		if !ok || wireBytes > guardianParentCompactionMaxWireBytes || compact.EstimateItemTokens(item) > maxTokens {
			return ""
		}
		return strings.TrimSpace(compacted.ResponseID)
	}
	if len(compacted.Summary) > maxTokens*4 {
		return ""
	}
	return strings.TrimSpace(compacted.ResponseID)
}

const guardianParentCompactionMaxWireBytes = 100_000 * 4

func latestGuardianParentCompactionItem(compacted *compact.Result) *compact.Item {
	if compacted == nil {
		return nil
	}
	for index := len(compacted.NewHistory) - 1; index >= 0; index-- {
		itemType := strings.ToLower(strings.TrimSpace(compacted.NewHistory[index].Type))
		if itemType == "compaction" || itemType == "context_compaction" {
			return &compacted.NewHistory[index]
		}
	}
	return nil
}

// guardianMaxReviewAttempts mirrors Rust's MAX_REVIEW_ATTEMPTS.
const guardianMaxReviewAttempts = 3

// guardianReviewRetryDelay mirrors wait_before_guardian_retry's service
// backoff: 200ms * 2^(attempt-1) with a 0.9..1.1 jitter. Stale-context retries
// never use it.
func guardianReviewRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := 200.0 * math.Pow(2, float64(attempt-1))
	jitter := 0.9 + rand.Float64()*0.2
	return time.Duration(base * jitter * float64(time.Millisecond))
}

// guardianReviewRetryableError mirrors should_retry_guardian_review's session
// arm: rate-limit, capacity and transient HTTP failures are recoverable, while
// policy, budget, invalid-request and unknown failures fail closed immediately.
func guardianReviewRetryableError(err error) bool {
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return false
	}
	switch apiErr.Kind {
	case codexapi.ErrorRateLimitExceeded, codexapi.ErrorFlexUnavailable, codexapi.ErrorServerOverloaded:
		return true
	case codexapi.ErrorTransport, codexapi.ErrorStream, codexapi.ErrorRetryable, codexapi.ErrorAPI:
		return apiErr.Status == 0 || apiErr.Status == 408 || apiErr.Status == 429 ||
			(apiErr.Status >= 500 && apiErr.Status <= 599)
	}
	return false
}

// waitGuardianReviewRetry waits for the service backoff while honouring the
// shared review deadline and cancellation.
func waitGuardianReviewRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// guardianReviewContextChanged mirrors Rust #53797's widened invalidation
// predicate: local or root review evidence that changed while the reviewer was
// running makes the assessment stale and triggers a fresh attempt.
func (r *modelGuardianReviewer) guardianReviewContextChanged(threadID, turnID string, transcript []string, entries []state.GuardianTranscriptEntry, rootAuthorization []string) bool {
	if r == nil {
		return false
	}
	if r.transcriptEntries != nil {
		if !reflect.DeepEqual(entries, r.transcriptEntries(threadID)) {
			return true
		}
	} else if r.transcript != nil && !slices.Equal(transcript, r.transcript(threadID)) {
		return true
	}
	if r.rootUserAuthorization != nil && !slices.Equal(rootAuthorization, r.rootUserAuthorization(threadID, turnID)) {
		return true
	}
	return false
}
