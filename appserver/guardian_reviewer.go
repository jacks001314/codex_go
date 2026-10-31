package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	"codex_go/network"
	"codex_go/retainedctx"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
	"codex_go/utils"

	"github.com/google/uuid"
)

type GuardianReviewer interface {
	Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error)
}

// GuardianAssessmentReviewer reports the structured assessment behind a review
// decision. A direct review (Rust #58028/#58119 thread/guardianReview) returns
// Guardian's own assessment, so it needs more than the decision and message the
// approval path consumes.
type GuardianAssessmentReviewer interface {
	ReviewAssessment(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, *state.Assessment, string, error)
}

// guardianOmittedCallIDKey carries the recorded call a direct review must omit
// from the reviewer transcript because it is delivered as the proposed action.
type guardianOmittedCallIDKey struct{}

// withGuardianOmittedCall marks a review as reviewing callID itself, so the
// transcript renders that item as an omission placeholder.
func withGuardianOmittedCall(ctx context.Context, callID string) context.Context {
	callID = strings.TrimSpace(callID)
	if callID == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, guardianOmittedCallIDKey{}, callID)
}

func guardianOmittedCallIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	callID, _ := ctx.Value(guardianOmittedCallIDKey{}).(string)
	return strings.TrimSpace(callID)
}

// guardianTimingCallIDKey carries the tool call whose critical-path entry should
// absorb an automatic review. Network approvals and MCP elicitations keep their
// own display identity, which is not a direct-tool invocation ID (Rust #57207).
type guardianTimingCallIDKey struct{}

// withGuardianTimingCallID overrides the review's critical-path owner.
func withGuardianTimingCallID(ctx context.Context, callID string) context.Context {
	callID = strings.TrimSpace(callID)
	if callID == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, guardianTimingCallIDKey{}, callID)
}

func guardianTimingCallIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	callID, ok := ctx.Value(guardianTimingCallIDKey{}).(string)
	if !ok || strings.TrimSpace(callID) == "" {
		return "", false
	}
	return callID, true
}

// guardianReviewOrigin carries the environment snapshot of the execution that
// asked for a review (Rust #44575 GuardianReviewContext::environments). Review
// settings stay those of the current turn; only the environment the reviewed
// process started in comes from its owner.
type guardianReviewOrigin struct {
	Environments      []map[string]any
	CWD               string
	EnvironmentID     string
	PermissionProfile *sandbox.PermissionProfile
}

type guardianReviewOriginKey struct{}

func withGuardianReviewOrigin(ctx context.Context, origin *guardianReviewOrigin) context.Context {
	if origin == nil || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, guardianReviewOriginKey{}, origin)
}

func guardianReviewOriginFromContext(ctx context.Context) *guardianReviewOrigin {
	if ctx == nil {
		return nil
	}
	origin, _ := ctx.Value(guardianReviewOriginKey{}).(*guardianReviewOrigin)
	return origin
}

// guardianReviewPlanWithOrigin keeps the current review settings and swaps only
// the environment state the reviewer is prepared against. Rust #44575 reviews a
// background execution in the environments it started with even when the
// current turn selects different ones.
func guardianReviewPlanWithOrigin(plan guardianReviewPlan, origin *guardianReviewOrigin) guardianReviewPlan {
	if origin == nil {
		return plan
	}
	if plan.PrepareInput.ParentConfig != nil {
		input := plan.PrepareInput
		input.ParentEnvironments = cloneMapSlice(origin.Environments)
		if origin.PermissionProfile != nil {
			input.ParentPermissionProfile = origin.PermissionProfile
		}
		prepared, err := guardianPrepareReviewerOptions(input)
		if err != nil {
			plan.Prepared = nil
			plan.PreparedError = err.Error()
		} else {
			plan.Prepared = prepared
			plan.PreparedError = ""
		}
		return plan
	}
	if origin.PermissionProfile != nil && plan.Prepared != nil {
		prepared := *plan.Prepared
		prepared.PermissionProfile = guardianReviewerReadOnlyProfile(origin.PermissionProfile)
		plan.Prepared = &prepared
	}
	return plan
}

type modelGuardianReviewer struct {
	agent model.AgentRunner
	// sessionsMu guards sessions, the per-thread reviewer sessions. Rust
	// #44252 moves review orchestration into a ReviewerPool whose reuse key
	// keeps concurrently forked threads on separate response chains; Go keeps
	// one reusable session per thread so a review from one thread never
	// continues another thread's reviewer conversation.
	sessionsMu    sync.Mutex
	sessions      map[string]*guardianSessionRunner
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
	logAssessment     func(threadID string, event *state.Event)
	interrupt         func(threadID, turnID, reason string)
	// activeTurnID resolves the turn Guardian denial accounting applies to
	// (Rust #44866): a review dispatched by a cell from an earlier turn counts
	// against the turn that is active now. Nil keeps the review's own turn.
	activeTurnID func(threadID string) string
	transcript   func(threadID string) []string
	// transcriptSelection carries the profile-retained host-attributed entries,
	// including native encrypted agent messages that cannot be rendered as text
	// (#49038), plus the retention omission and truncation observations.
	transcriptSelection func(threadID string) guardianTranscriptSelection
	transcriptFormat    func(threadID string) state.GuardianTranscriptFormat
	// feedbackCaptureEnabled gates the bounded failed-review records exported
	// by log-enabled feedback uploads (#41936): ephemeral threads and disabled
	// feedback collection retain nothing.
	feedbackCaptureEnabled func(threadID string) bool
	// persistFeedback stores failed-review evidence in the state runtime so it
	// survives reviewer/process cleanup (Rust #51651).
	persistFeedback func(threadID string, record []byte)
	// retainedOmissions is the host-scoped record of the retained-section
	// omission notices the reviewer conversation already received (#49796).
	retainedOmissionsMu sync.Mutex
	retainedOmissions   map[string]state.GuardianRetainedOmissions
	// turnTiming resolves the reviewed turn's timing state, which owns the
	// direct-tool invocation registry used to attribute blocking review time to
	// the owning tool call (Rust #57207).
	turnTiming func(threadID, turnID string) *turn.TimingState
	// blocking mirrors Rust's thread-owned ReviewBlocking
	// (codex-rs/ext/guardian-reviewer/src/blocking.rs): the reviewer enters the
	// same tracker the turn lifecycle exports samples from, so approval waits
	// are charged to the turn that serviced them (#55591).
	blocking *guardianReviewBlocking
	// reviewModel resolves the catalog-backed reviewer selection for the
	// reviewed turn: the model, its request-level reasoning effort, and the
	// reviewer's policy instructions (Rust's resolve_review_model /
	// select_review_model plus build_guardian_review_session_config).
	reviewPlan  func(threadID, turnID string) guardianReviewPlan
	contextMode func(threadID, turnID string) state.GuardianContextMode
	// reviewPlanContext resolves the same plan with the review deadline and
	// cancellation, which Rust #57789 also applies to reviewer setup. It wins
	// over reviewPlan when set.
	reviewPlanContext          func(ctx context.Context, threadID, turnID string) guardianReviewPlan
	reviewEndpoint             func(threadID, turnID string) model.ResponsesEndpoint
	specialty                  func(threadID, turnID string) string
	nodeReplAutoReviewRequired func(threadID, turnID string) bool
	fullAccess                 func(threadID, turnID string) bool
	approvalsReviewer          func(threadID, turnID string) string
	// requiredReview reports whether the reviewed model is listed in
	// auto_review.required_on_models. Rust #41309 defers such models from the
	// cached-score fast path before they can reuse an approval, so Go's
	// remaining user-mode fast path must defer them to a full review as well.
	// Full Access stays outside this guard because Rust's routing approves it
	// before contributors run.
	requiredReview func(threadID, turnID string) bool
	// rolloverForTokenBudget reports whether the Guardian follow-up review must
	// start a fresh response chain because the reviewed turn's token budget is
	// exhausted under the review model's defaults (Rust #41221).
	rolloverForTokenBudget func(threadID, turnID string) bool
	environment            func(context.Context, string, string) ([]any, error)
	// environmentForOrigin renders the world-state items from an execution's
	// captured environment snapshot instead of the current turn's selections.
	environmentForOrigin func(context.Context, string, string, *guardianReviewOrigin) ([]any, error)
	permissionProfile    func(threadID, turnID string) *sandbox.PermissionProfile
	// turnPermissionProfile resolves the reviewed turn's own profile and cwd for
	// the permission evidence, which is distinct from the read-only profile the
	// reviewer's session uses, plus the reviewed environment's selection id when
	// one was resolved. The last argument is the tool payload's own environment
	// id when it names one (Rust's `for_tool`).
	turnPermissionProfile func(threadID, turnID, environmentID string) (*sandbox.PermissionProfile, string, *string)
	// targetEnvironmentAvailable reports whether an environment-scoped action's
	// environment belongs to the reviewed turn's captured selections (Rust
	// ReviewAction::validate). An unavailable target is denied before inference
	// instead of being reviewed against the primary environment.
	targetEnvironmentAvailable func(threadID, turnID, environmentID string) bool
	// latestResponseID resolves the reviewed turn's newest response id for the
	// review request's `parent_response_id` client metadata (Rust #45441).
	latestResponseID func(threadID, turnID string) string
	// parentResponse resolves the reviewed turn's newest response id together
	// with the routing pin captured from its handshake (Rust #57710). When set
	// it replaces latestResponseID and the pair is snapshotted once per review,
	// so retries keep the affinity captured when the review was requested.
	parentResponse func(threadID, turnID string) (string, *network.RoutingAffinity)
	// servingAssessment resolves the final serving value retained for one
	// invocation (Rust #58075). A matching low-risk value may replace the
	// synchronous review; a missing or stale value falls back to it.
	servingAssessment func(threadID, turnID, callID string) *state.ServingAssessment
	// servingAuthorization captures the current authorization and policy the
	// assessment is revalidated against.
	servingAuthorization func(threadID, turnID string) state.ServingAuthorization
	// allowsInitialCUACall reports whether the reviewed model permits its first
	// computer-use call to skip the synchronous review while the asynchronous
	// classification continues (Rust #41666).
	allowsInitialCUACall  func(threadID, turnID string) bool
	nodeReplEvidence      func(threadID string, reviewedSequence uint64) *codexctx.NodeReplReviewEvidenceFragment
	rootUserAuthorization func(threadID, turnID string) []state.RootMessage
	// syncImageContextWindow resolves the review model's usable context window.
	// Rust #57494 admits synchronous Guardian images only when the prompt stays
	// within the fixed image budget and the window minus reviewer history.
	syncImageContextWindow func(threadID, modelID string) (int64, bool)
	// retainedContext resolves the thread's retained evidence, which the review
	// prompt renders as the retained user-instruction section (Rust's
	// SectionHistory::retained_context and the RetainedUserInstructionsSection).
	retainedContext func(threadID, turnID string) *retainedctx.RetainedContext
	// conversationHistory resolves the reviewed thread's live host-owned Apps
	// history tools and their output budget. Nil keeps the single-request
	// reviewer path used when the opt-in feature is disabled.
	conversationHistory func(threadID, turnID string) *guardianConversationHistoryRuntime
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
	// reviewTimeout, when set, resolves the review's overall budget from the
	// turn's configuration (Rust #56371). The second result is false when
	// `auto_review.timeout_ms = 0` disables the deadline.
	reviewTimeout func(threadID, turnID string) (time.Duration, bool)
	// installationID supplies the Codex installation id for the review
	// request's turn metadata (Rust #44298).
	installationID func() string
	// trustedRootTurnID resolves the reviewed turn's recorded causal root for
	// classification metadata (Rust #41385's
	// turn_metadata_state.root_turn_id). The second result is false when the
	// turn has no trusted lineage, so the review omits root_turn_id instead of
	// guessing one.
	trustedRootTurnID func(threadID, turnID string) (string, bool)
	// newClassificationTurnID mints the reviewer's own turn identity. Rust
	// #41385 treats every Guardian classification as a distinct inference turn;
	// nil uses the package UUIDv7 generator.
	newClassificationTurnID func() string
	// metrics receives the guardian review metrics (Rust's
	// emit_guardian_review_metrics).
	metrics telemetry.TurnMetricSink
	// subagentThread reports whether the review's thread is a delegated
	// subagent, which selects Rust's delegated_subagent approval source.
	subagentThread func(threadID string) bool
	// warn surfaces a guardian warning notification (Rust's
	// EventMsg::GuardianWarning) for a timeout or a failed review.
	warn func(threadID string, message string)
	// tracer opens the reviewer's debug span (Rust #52274's `guardian_review`
	// span with review, thread, turn and tool-call identifiers).
	tracer func() *telemetry.Tracer
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
	wrapperStarts        []guardianWrapperStart
	// jsExecutions counts `js` executions from Node REPL-backed servers.
	// Setup and reset tools must not consume the first-execution allowance
	// (Rust #41666).
	jsExecutions int
	jsCallIDs    map[string]struct{}
}

func (r *modelGuardianReviewer) reviewTurnTiming(threadID, turnID string) *turn.TimingState {
	if r == nil || r.turnTiming == nil {
		return nil
	}
	return r.turnTiming(strings.TrimSpace(threadID), strings.TrimSpace(turnID))
}

func (r *modelGuardianReviewer) beginApprovalToolCall(threadID, toolCallID string) *guardianScoreProgress {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	toolCallID = strings.TrimSpace(toolCallID)
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
	// Tool lifecycle normally records the call before its executor requests an
	// approval. Direct review callers without lifecycle metadata retain the old
	// counting behavior instead of losing the current call from score lag.
	if toolCallID != "" && progress.hasWrapperStartLocked(toolCallID) {
		return progress
	}
	progress.latestToolCall++
	if toolCallID != "" {
		progress.appendWrapperStartLocked(guardianWrapperStart{
			callID: toolCallID,
			index:  progress.latestToolCall,
		})
	}
	return progress
}

func (r *modelGuardianReviewer) scoreLag(threadID, toolCallID string) int {
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
	lag := progress.latestToolCall - progress.latestScoredToolCall
	if lag < 0 {
		lag = 0
	}
	lag -= progress.wrapperDiscountLocked(toolCallID, progress.latestScoredToolCall)
	if lag < 0 {
		return 0
	}
	return lag
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

// observeNodeReplJSExecution mirrors Rust's `observe_js_execution`: it counts
// only `js` executions on a Node REPL-backed server, so `js_reset` and
// `js_add_node_module_dir` keep the first-execution allowance available
// (Rust #41666). The call id dedupes review retries for one invocation.
func (r *modelGuardianReviewer) observeNodeReplJSExecution(threadID, toolCallID string) int {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return 0
	}
	threadID = strings.TrimSpace(threadID)
	toolCallID = strings.TrimSpace(toolCallID)
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
	if toolCallID != "" {
		if _, seen := progress.jsCallIDs[toolCallID]; seen {
			return progress.jsExecutions
		}
		if progress.jsCallIDs == nil {
			progress.jsCallIDs = map[string]struct{}{}
		}
		progress.jsCallIDs[toolCallID] = struct{}{}
	}
	progress.jsExecutions++
	return progress.jsExecutions
}

// guardianInitialCUAExecution reports whether the action is a Node REPL `js`
// execution, the operation Rust #41666 fast-approves on its first occurrence.
// Setup and reset tools (`js_reset`, `js_add_node_module_dir`) are deliberately
// excluded so they cannot consume the allowance.
func guardianInitialCUAExecution(action state.Action) bool {
	if strings.TrimSpace(action.Type) != "mcp_tool_call" {
		return false
	}
	switch strings.TrimSpace(action.ToolName) {
	case "js", "browser_js":
	default:
		return false
	}
	return mcp.IsNodeReplBackedConnector(strings.TrimSpace(action.Server), action.ConnectorID)
}

// recordNonDenial mirrors Rust's record_guardian_non_denial: only a completed
// assessment with outcome deny counts as a policy denial, so an approved review
// and every review failure reset the denial window instead.
func (r *modelGuardianReviewer) recordNonDenial(threadID, turnID string) {
	if r == nil || r.breaker == nil {
		return
	}
	target, ok := r.accountingTurnID(threadID, turnID)
	if !ok {
		return
	}
	r.breaker.RecordNonDenial(target)
}

// accountingTurnID picks the turn a Guardian decision applies to. Rust #44866
// records against the session's active turn so a review from an earlier cell
// still accounts for (and can interrupt) the turn that is running now; with no
// active turn there is nothing to account.
func (r *modelGuardianReviewer) accountingTurnID(threadID, turnID string) (string, bool) {
	if r != nil && r.activeTurnID != nil {
		if active := strings.TrimSpace(r.activeTurnID(strings.TrimSpace(threadID))); active != "" {
			return active, true
		}
		return "", false
	}
	return strings.TrimSpace(turnID), true
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
	// previousFromPrewarm marks r.previous as the response the reviewer
	// prewarm prepared, so the first review is tagged after_prewarm
	// (Rust #48812).
	previousFromPrewarm bool
	// policyInitialized/policy mirror Rust #41919: a changed effective Node
	// REPL policy invalidates a cached reviewer session.
	policyInitialized bool
	policy            string
	// priorReview mirrors Rust's ConversationState::cursor: set once review
	// input was admitted by the guardian session, even if inference failed. The
	// next review therefore reports had_prior_review_context = true.
	priorReview bool
	// completedReviews mirrors Rust's ConversationState::completed_review_count.
	// A failed review may admit its input without counting as completed (#54264).
	completedReviews int
	// lastUsage is the reviewer's most recent total token usage. Rust #57494
	// measures reviewer history before admitting new images; the last completed
	// response is the closest host-owned equivalent in Go.
	lastUsage int64
}

type guardianReviewHistoryCheckpoint struct {
	previous string
	seeded   string
}

func (r *guardianSessionRunner) reviewHistoryCheckpoint() guardianReviewHistoryCheckpoint {
	if r == nil {
		return guardianReviewHistoryCheckpoint{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return guardianReviewHistoryCheckpoint{previous: r.previous, seeded: r.seeded}
}

// rollbackReviewHistory restores the last committed reviewer checkpoint after a
// stale allow. The invalidated attempt's response id must not become the next
// attempt's parent, while earlier valid reviewer context stays reusable.
func (r *guardianSessionRunner) rollbackReviewHistory(checkpoint guardianReviewHistoryCheckpoint) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.previous = checkpoint.previous
	r.seeded = checkpoint.seeded
	r.mu.Unlock()
}

// EnsureReviewPolicy invalidates the cached reviewer session when the
// effective Node REPL policy changes (#41919). An explicitly empty policy is
// distinct from an unset one: it suppresses policy injection and must not reuse
// a session created with the bundled policy.
func (r *guardianSessionRunner) EnsureReviewPolicy(policy string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.policyInitialized && r.policy != policy {
		r.previous = ""
		r.seeded = ""
	}
	r.policyInitialized = true
	r.policy = policy
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
		r.previousFromPrewarm = true
	}
	return err
}

// takePrewarm hands the prewarmed trunk response to the first thread-owned
// reviewer session (Rust #44252 pool prewarm). The trunk keeps the response for
// a review that uses the trunk key directly, but only one session reports it as
// the after-prewarm continuation.
func (r *guardianSessionRunner) takePrewarm() (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.previousFromPrewarm || strings.TrimSpace(r.previous) == "" {
		return "", false
	}
	r.previousFromPrewarm = false
	return r.previous, true
}

// adoptPrewarm starts a new thread-owned session from the prewarmed response so
// its first review continues the trunk instead of opening an unrelated chain.
func (r *guardianSessionRunner) adoptPrewarm(previous string) {
	if r == nil || strings.TrimSpace(previous) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.previous = strings.TrimSpace(previous)
	r.previousFromPrewarm = true
}

func (r *guardianSessionRunner) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if r == nil || r.agent == nil {
		return nil, errors.New("guardian session runner is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *request
	clone.ClientMetadata = cloneStringMap(request.ClientMetadata)
	clone.PreviousResponseFromPrewarm = false
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
		clone.PreviousResponseFromPrewarm = r.previousFromPrewarm
	}
	r.previousFromPrewarm = false
	clone.Store = false
	var response *model.AgentResponse
	var err error
	if r.previous != "" {
		if ws, ok := r.agent.(guardianWebSocketRunner); ok {
			response, err = ws.RunWebSocket(ctx, &clone)
			if err != nil {
				clone.PreviousResponseID = ""
				clone.PreviousResponseFromPrewarm = false
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
	if err == nil && response != nil {
		r.lastUsage = model.AgentUsageTotalTokens(response.Usage)
	}
	return response, err
}

func (r *guardianSessionRunner) lastUsageTokens() int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastUsage
}

// RunIsolated mirrors Rust ReviewerPool::review_isolated (#57522): one review
// in a temporary session that neither reads nor replaces the reusable reviewer.
// The request is built without a previous response or cached snapshot, so the
// prewarmed session stays available for the next regular review.
func (r *guardianSessionRunner) RunIsolated(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if r == nil || r.agent == nil {
		return nil, errors.New("guardian session runner is unavailable")
	}
	if request == nil {
		return nil, errors.New("guardian request is nil")
	}
	clone := *request
	clone.ClientMetadata = cloneStringMap(request.ClientMetadata)
	clone.PreviousResponseID = ""
	clone.Store = false
	return r.agent.Run(ctx, &clone)
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

// RolloverForContext starts a fresh reviewer response chain after the review
// model reports context exhaustion. The prompt builder replays the complete
// transcript, so clearing the previous response restores the context that the
// exhausted session can no longer carry.
func (r *guardianSessionRunner) RolloverForContext() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.previous = ""
	r.seeded = ""
	r.mu.Unlock()
}

// ResetForParentHistoryReset invalidates the reusable reviewer after the parent
// conversation history was replaced destructively without a compaction
// checkpoint (Rust #45413). Rust folds a reset version into the review-session
// reuse key, so the first review after a summary-free reset starts a new trunk
// with no carried rationale and reports had_prior_review_context = false.
func (r *guardianSessionRunner) ResetForParentHistoryReset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.previous = ""
	r.seeded = ""
	r.previousFromPrewarm = false
	r.priorReview = false
	r.completedReviews = 0
	r.lastUsage = 0
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

// guardianReviewContext applies the configured overall review budget. A
// non-positive timeout means unlimited: the review keeps the caller's
// cancellation and the independent transport timeouts (Rust #56371).
func guardianReviewContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func newModelGuardianReviewer(agent model.AgentRunner) GuardianReviewer {
	if agent == nil {
		return nil
	}
	return &modelGuardianReviewer{
		agent: agent, store: state.NewReviewStore(), sessions: map[string]*guardianSessionRunner{},
		breaker: state.NewCircuitBreaker(), scoreProgress: map[string]*guardianScoreProgress{},
		maxToolCallLag: defaultGuardianMaxToolCallLag, timeout: state.ReviewTimeout,
	}
}

func (r *modelGuardianReviewer) Prewarm(ctx context.Context) error {
	if r == nil || r.agent == nil {
		return nil
	}
	return r.sessionFor(guardianReviewerTrunkSession).Prewarm(ctx)
}

// guardianReviewerTrunkSession is the session that receives startup prewarming
// (Rust #44252's prewarmed pool trunk). The first thread-owned review adopts it
// and later threads start their own chains.
const guardianReviewerTrunkSession = ""

// sessionFor returns the reusable reviewer session for one thread. A reviewer
// built around an explicit session runner keeps sharing it; a reviewer built
// from a plain agent gives every thread its own session, so concurrently forked
// threads cannot continue each other's response chain (Rust #44252
// ReviewerPool).
func (r *modelGuardianReviewer) sessionFor(threadID string) *guardianSessionRunner {
	if r == nil || r.agent == nil {
		return nil
	}
	if runner, ok := r.agent.(*guardianSessionRunner); ok {
		return runner
	}
	key := strings.TrimSpace(threadID)
	r.sessionsMu.Lock()
	defer r.sessionsMu.Unlock()
	if r.sessions == nil {
		r.sessions = map[string]*guardianSessionRunner{}
	}
	if session := r.sessions[key]; session != nil {
		return session
	}
	session := &guardianSessionRunner{agent: r.agent}
	if key != guardianReviewerTrunkSession {
		if trunk := r.sessions[guardianReviewerTrunkSession]; trunk != nil {
			if previous, ok := trunk.takePrewarm(); ok {
				session.adoptPrewarm(previous)
			}
		}
	}
	r.sessions[key] = session
	return session
}

// existingSessionFor returns the reviewer a thread already owns. Lifecycle
// resets use it so they never create reviewer state for a thread that has not
// reviewed yet.
func (r *modelGuardianReviewer) existingSessionFor(threadID string) *guardianSessionRunner {
	if r == nil || r.agent == nil {
		return nil
	}
	if runner, ok := r.agent.(*guardianSessionRunner); ok {
		return runner
	}
	r.sessionsMu.Lock()
	defer r.sessionsMu.Unlock()
	return r.sessions[strings.TrimSpace(threadID)]
}

// DropThreadSession discards the reusable reviewer owned by one thread. Rust
// keeps the ReviewerPool and its ReviewerTasks in the thread store, so the
// thread-stop hook is what tears them down (#45672); Go's reusable reviewer is
// host-owned, so unloading the thread is the same ownership boundary and the
// thread's response chain, prior-review cursor and completed count go with it.
// An explicitly supplied session runner is shared by construction and stays.
func (r *modelGuardianReviewer) DropThreadSession(threadID string) {
	if r == nil {
		return
	}
	key := strings.TrimSpace(threadID)
	if key == "" {
		return
	}
	if _, shared := r.agent.(*guardianSessionRunner); shared {
		return
	}
	r.sessionsMu.Lock()
	delete(r.sessions, key)
	r.sessionsMu.Unlock()
}

// guardianThreadReviewerSession is implemented by reviewers that own reusable
// per-thread sessions.
type guardianThreadReviewerSession interface {
	DropThreadSession(threadID string)
}

// forgetGuardianReviewerSession drops a closed thread's reusable reviewer with
// the rest of its runtime state (Rust #45672 moves reviewer lifetime ownership
// into the thread lifecycle).
func (r *RuntimeRouter) forgetGuardianReviewerSession(threadID string) {
	if r == nil || r.services.GuardianReviewer == nil {
		return
	}
	owner, ok := r.services.GuardianReviewer.(guardianThreadReviewerSession)
	if !ok {
		return
	}
	owner.DropThreadSession(threadID)
}

// Review reports the decision and rejection message of one approval review.
// Callers that need Guardian's structured assessment use ReviewAssessment.
func (r *modelGuardianReviewer) Review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
	decision, _, reason, err := r.review(ctx, threadID, turnID, targetItemID, action)
	return decision, reason, err
}

// ReviewAssessment reports the structured assessment behind one review, so a
// direct review returns the same risk level, authorization and rationale as a
// live approval (Rust #58028/#58119 thread/guardianReview). The assessment is
// nil for decisions that never reached an assessment (fast approvals, failed
// or cancelled reviews).
func (r *modelGuardianReviewer) ReviewAssessment(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, *state.Assessment, string, error) {
	return r.review(ctx, threadID, turnID, targetItemID, action)
}

func (r *modelGuardianReviewer) review(ctx context.Context, threadID, turnID, targetItemID string, action state.Action) (state.ReviewDecision, *state.Assessment, string, error) {
	if r == nil || r.agent == nil {
		return state.DecisionAborted, nil, "", errors.New("guardian reviewer is unavailable")
	}
	session := r.sessionFor(threadID)
	// Rust #51330 (f5fa209bb0, ext/guardian-reviewer/src/routing.rs): the total
	// decision timer wraps the complete approval decision -- fast decisions,
	// preparation failures and cancellation, not just the model review that
	// emitGuardianReviewMetrics times. Rust's `decide` has no unavailable arm, so
	// the guard above stays outside the timer.
	decisionStartedAt := time.Now()
	defer r.recordGuardianDecisionDuration(decisionStartedAt)
	// Rust #57710 installs the parent response snapshot for the exclusive
	// lifetime of one review, including its inference retries.
	parentResponse := guardianParentResponseSnapshot(r, threadID, turnID)
	// Rust #55591 (ext/guardian-reviewer/src/routing.rs): the blocking guard
	// wraps the complete decision -- fast decisions, preparation and
	// cancellation -- so the thread's approval-wait union is charged to the
	// turn that serviced it.
	if release := r.blocking.enter(threadID); release != nil {
		defer release()
	}
	// Rust #45693 (guardian-reviewer/src/routing.rs ReviewRequest::decide):
	// cancellation is checked before the routing contributors run, and a
	// cancelled operation reports Abort rather than a fast approval. Go's
	// reviewer has no contributor registry or cached approvals, so the
	// equivalent guard is this single check ahead of the full-access and
	// user-approval-mode fast paths.
	if ctx != nil && ctx.Err() != nil {
		return state.DecisionAborted, nil, "", nil
	}
	// Rust #57433: a native tool call keeps review accounting and metrics but has
	// no action-specific client assessment event, so its started/completed
	// notifications are suppressed while the review still runs.
	nativeToolCall := strings.TrimSpace(action.Type) == state.ActionNativeToolCall
	emit := r.emit
	if nativeToolCall {
		emit = func(string, *state.Event) {}
	}
	// Rust #57207: bind the review to its owning tool invocation before any
	// blocking work starts. When the owner is unknown, invalidate the turn's
	// capture instead of charging an unrelated call.
	if timing := r.reviewTurnTiming(threadID, turnID); timing.CriticalPathCaptureEnabled() {
		timingCallID := targetItemID
		if override, ok := guardianTimingCallIDFromContext(ctx); ok {
			timingCallID = override
		}
		if guard := timing.DirectToolTimings().StartReviewForTurn(timingCallID, turnID); guard != nil {
			defer guard.End()
		} else {
			timing.InvalidateCriticalPath()
		}
	}
	// Rust #39001: skip approval review when the latest risk score lags by more
	// than max_tool_call_lag tool calls. The Go simplified reviewer fails closed
	// (no stale-score auto-approval), mirroring the stale-data guard while
	// keeping the established refuse-approval convention.
	if lag := r.maxToolCallLagValue(threadID, turnID); lag > 0 && r.scoreLag(threadID, targetItemID) > lag {
		return state.DecisionDenied, nil, "guardian review skipped: risk score lag exceeds max_tool_call_lag", nil
	}
	if r.fullAccess != nil && r.fullAccess(threadID, turnID) {
		if r.fastDecision != nil {
			r.fastDecision(ctx, threadID, turnID, targetItemID)
		}
		return state.DecisionApproved, nil, "full access", nil
	}
	// Rust #41666: the first `js` execution from a Node REPL-backed server never
	// waits for synchronous review. The asynchronous classifier still runs, and
	// setup/reset tools do not consume the allowance, so every later execution
	// follows the normal policy.
	if guardianInitialCUAExecution(action) {
		if executions := r.observeNodeReplJSExecution(threadID, targetItemID); executions == 1 &&
			r.allowsInitialCUACall != nil && r.allowsInitialCUACall(threadID, turnID) {
			if r.fastDecision != nil {
				r.fastDecision(ctx, threadID, turnID, targetItemID)
			}
			if r.metrics != nil {
				r.metrics.Counter(telemetry.GuardianV2FastDecisionMetric, 1, map[string]string{
					"decision": "approved", "reason": "initial_cua_call",
				})
			}
			return state.DecisionApproved, nil, "initial computer use call", nil
		}
	}
	requiredReview := r.requiredReview != nil && r.requiredReview(threadID, turnID)
	// Rust #44569: a cached low-risk assessment must not cover an action the
	// reviewer could never admit, including expanded approval arguments. An
	// oversized action always runs the full review (which defers to the user or
	// fails closed), while later small actions keep reusing their score.
	oversizedAction := r.guardianActionExceedsReviewBudget(threadID, turnID, action, session)
	// Rust #58075: a final serving assessment may replace the synchronous
	// review for a low-risk action. An explicit fresh-review requirement, an
	// elevated-risk value, or a stale/incomplete authorization still runs the
	// full review below.
	if r.servingAssessment != nil && !requiredReview && !oversizedAction {
		if assessment := r.servingAssessment(threadID, turnID, targetItemID); assessment != nil {
			current := state.ServingAuthorization{}
			if r.servingAuthorization != nil {
				current = r.servingAuthorization(threadID, turnID)
			}
			if assessment.AllowsSynchronousReviewSkip(current) {
				r.beginApprovalToolCall(threadID, targetItemID)
				r.markScored(threadID)
				if r.fastDecision != nil {
					r.fastDecision(ctx, threadID, turnID, targetItemID)
				}
				if r.metrics != nil {
					r.metrics.Counter(telemetry.GuardianReviewRouteMetric, 1, map[string]string{
						"route": "serving_assessment", "reason": "low_risk",
					})
				}
				return state.DecisionApproved, nil, "serving assessment", nil
			}
		}
	}
	if !requiredReview && r.approvalsReviewer != nil && strings.EqualFold(r.approvalsReviewer(threadID, turnID), string(config.ApprovalsReviewerUser)) &&
		action.Type == "mcp_tool_call" &&
		mcp.IsNodeReplBackedConnector(strings.TrimSpace(action.Server), action.ConnectorID) &&
		strings.EqualFold(strings.TrimSpace(action.ToolName), "js") {
		if r.fastDecision != nil {
			r.fastDecision(ctx, threadID, turnID, targetItemID)
		}
		return state.DecisionApproved, nil, "user approval mode", nil
	}
	r.beginApprovalToolCall(threadID, targetItemID)
	// The review attempt starts here: Rust builds ReviewReport/tracking before
	// the prompt, and the metrics duration is measured from that point.
	if r.metrics != nil {
		// Rust #55347: a synchronous model review that did not race an async
		// approval is the `required_sync` route. Go's reviewer is synchronous,
		// so the distinction is the configured fresh-review requirement.
		routeReason := "policy"
		if requiredReview {
			routeReason = "fresh_required"
		}
		r.metrics.Counter(telemetry.GuardianReviewRouteMetric, 1, map[string]string{
			"route": "required_sync", "reason": routeReason,
		})
	}
	reviewStartedAt := time.Now()
	attribution := r.newGuardianReviewAttribution(threadID, turnID, action)
	feedbackCapture := &guardianReviewFeedbackCapture{}
	defer func() {
		if r.feedbackCaptureEnabled != nil && !r.feedbackCaptureEnabled(threadID) {
			return
		}
		if record := feedbackCapture.record(threadID, turnID, attribution, action); len(record) > 0 && r.persistFeedback != nil {
			r.persistFeedback(threadID, record)
		}
	}()
	contextChanged := false
	emitReviewMetrics := func() {
		attribution.ContextChanged = contextChanged
		r.emitGuardianReviewMetrics(attribution, guardianReviewElapsedMS(reviewStartedAt))
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
		return state.DecisionAborted, nil, "", err
	}
	event.GuardianContextMode = attribution.ContextMode.String()
	store.SetGuardianContextMode(event.ID, event.GuardianContextMode)
	emit(threadID, event)
	// Rust #47630 ReviewAction::validate: an action may only be reviewed against
	// the environment that owns it. Manual approval routing still sees the
	// action JSON; Guardian inference never runs for a missing environment.
	if environmentID := strings.TrimSpace(action.EnvironmentID); environmentID != "" && r.targetEnvironmentAvailable != nil &&
		!r.targetEnvironmentAvailable(threadID, turnID, environmentID) {
		message := "automatic approval review cannot access the request's environment"
		if strings.TrimSpace(action.Type) == "write_stdin" {
			message = "automatic approval review cannot access the terminal's environment; select it before retrying"
		}
		completed, _ := store.FailClosed(event.ID, message)
		emit(threadID, completed)
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "environment_unavailable"
		emitReviewMetrics()
		return state.DecisionDenied, nil, message, nil
	}
	if r.tracer != nil {
		if tracer := r.tracer(); tracer != nil {
			span := tracer.StartSpan("guardian_review", map[string]string{
				"review_id":    strings.TrimSpace(event.ID),
				"thread_id":    strings.TrimSpace(threadID),
				"turn_id":      strings.TrimSpace(turnID),
				"tool_call_id": strings.TrimSpace(targetItemID),
			})
			if span != nil {
				defer span.End()
			}
		}
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = state.ReviewTimeout
	}
	if r.reviewTimeout != nil {
		var configured bool
		timeout, configured = r.reviewTimeout(threadID, turnID)
		if !configured {
			timeout = 0
		}
	}
	reviewCtx, cancel := guardianReviewContext(ctx, timeout)
	defer cancel()
	plan := r.planForTurn(reviewCtx, threadID, turnID)
	reviewOrigin := guardianReviewOriginFromContext(reviewCtx)
	plan = guardianReviewPlanWithOrigin(plan, reviewOrigin)
	// Rust #40742 fails the decision closed when the reviewer session cannot be
	// prepared (for example an environment whose permissions are still pending).
	if plan.PreparedError != "" {
		rationale := guardianReviewFailureRationale(plan.PreparedError)
		message := guardianReviewFailureMessage(plan.PreparedError)
		completed, _ := store.FailClosed(event.ID, rationale)
		emit(threadID, completed)
		r.recordNonDenial(threadID, turnID)
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "session_error"
		emitReviewMetrics()
		r.emitWarning(threadID, rationale)
		return state.DecisionDenied, nil, message, nil
	}
	if session != nil {
		session.EnsureReviewPolicy(plan.NodeReplPolicy)
		// Rust #43912 checks the reviewer session's own feature gate, which
		// disables token-budget mode even when the parent turn enabled it.
		if guardianReviewerTokenBudgetEnabled(plan.Prepared) && r.rolloverForTokenBudget != nil && r.rolloverForTokenBudget(threadID, turnID) {
			session.RolloverForContext()
			contextChanged = true
		}
	}
	feedbackCapture.model = plan.Selection.Model
	feedbackCapture.instructions = plan.Instructions
	historyCheckpoint := guardianReviewHistoryCheckpoint{}
	historyCheckpointReady := false
	// Rust #53987/#53797: retries share one attempt budget and deadline. A
	// changed local review context is stale authorization, refreshed without
	// the service backoff; parse failures keep Rust's exponential backoff.
	finishTimedOut := func() (state.ReviewDecision, *state.Assessment, string, error) {
		if historyCheckpointReady {
			if session != nil {
				session.rollbackReviewHistory(historyCheckpoint)
			}
		}
		completed, finishErr := store.Timeout(event.ID)
		if finishErr == nil {
			emit(threadID, completed)
		}
		// Rust records a non-denial for a failed review: only a completed
		// assessment with outcome deny counts as a policy denial.
		r.recordNonDenial(threadID, turnID)
		attribution.Decision = state.DecisionTimedOut
		attribution.TerminalStatus = "timed_out"
		attribution.FailureReason = "timeout"
		emitReviewMetrics()
		timeoutMessage := guardianTimeoutMessage(plan.AutoReview)
		// Rust's timed-out completion records the review rationale on the
		// assessment event and publishes it as the GuardianWarning, while the
		// tool rejection carries the resolved timeout instructions.
		r.emitWarning(threadID, state.GuardianTimeoutRationale())
		return state.DecisionTimedOut, nil, timeoutMessage, finishErr
	}
	finishAborted := func() (state.ReviewDecision, *state.Assessment, string, error) {
		if historyCheckpointReady {
			if session != nil {
				session.rollbackReviewHistory(historyCheckpoint)
			}
		}
		completed, finishErr := store.Abort(event.ID, "Guardian review was aborted.")
		if finishErr == nil {
			emit(threadID, completed)
		}
		r.recordNonDenial(threadID, turnID)
		attribution.Decision = state.DecisionAborted
		attribution.TerminalStatus = "aborted"
		attribution.FailureReason = "cancelled"
		emitReviewMetrics()
		return state.DecisionAborted, nil, "", finishErr
	}
	// Rust #41385: a classification is its own inference turn. Mint one UUIDv7
	// identity before the attempt loop so a retry keeps the same classification
	// turn, while parent_turn_id stays the turn that requested the review.
	classificationTurnID := r.classificationTurnID()
	attempts := 0
	var assessment *state.Assessment
	var response *model.AgentResponse
reviewAttempt:
	attempts++
	attribution.AttemptCount = attempts
	var transcript []string
	var transcriptEntries []state.GuardianTranscriptEntry
	var transcriptOmissionNote string
	transcriptFormat := state.GuardianTranscriptFormatLine
	if r.transcriptFormat != nil {
		transcriptFormat = r.transcriptFormat(threadID)
	}
	if r.transcriptSelection != nil {
		selection := r.transcriptSelection(threadID)
		transcriptEntries = selection.Entries
		transcriptOmissionNote = selection.OmissionNote
		transcript = state.RenderGuardianTranscript(transcriptEntries, transcriptFormat)
	} else if r.transcript != nil {
		transcript = r.transcript(threadID)
	}
	omittedCallID := ""
	if nativeToolCall {
		if callID, _, ok := state.GuardianNativeFunctionCall(action.NativeCall); ok {
			omittedCallID = callID
		}
	}
	if omittedCallID == "" {
		// A direct review of a recorded exec_command delivers the call as the
		// planned action too, so its history copy must be omitted for the prompt
		// to match the live review (Rust #58028).
		omittedCallID = guardianOmittedCallIDFromContext(ctx)
	}
	if omittedCallID != "" {
		// The proposed call is delivered as the planned action, so its history
		// copy becomes an omission placeholder at the same position.
		transcriptEntries = state.GuardianTranscriptWithoutProposedCall(transcriptEntries, omittedCallID)
		transcript = state.RenderGuardianTranscript(transcriptEntries, transcriptFormat)
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
	if reviewOrigin != nil && reviewOrigin.PermissionProfile != nil {
		deniedPaths := sandbox.UnreadableRootsWithCWD(reviewOrigin.PermissionProfile, reviewOrigin.CWD)
		deniedGlobs := sandbox.UnreadableGlobsWithCWD(reviewOrigin.PermissionProfile, reviewOrigin.CWD)
		var environmentID *string
		if id := strings.TrimSpace(reviewOrigin.EnvironmentID); id != "" {
			environmentID = &id
		}
		permissionContext = &state.PermissionContext{
			EnvironmentID: environmentID,
			DeniedPaths:   deniedPaths,
			DeniedGlobs:   deniedGlobs,
		}
	} else if r.turnPermissionProfile != nil {
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
	previousOmissions := r.retainedOmissionDelivery(threadID)
	pendingOmissions := state.CurrentRetainedOmissions(retainedContext, state.ActionPresentationSyncFull)
	// The approval reviewer is the synchronous, stateful consumer, so it renders
	// the retained snapshot in Rust's synchronous layout (#51627): one retained
	// section ahead of the transcript, with the assistant context inside it. The
	// asynchronous independent snapshot's split belongs to ActionPresentationAsync.
	buildOptions := state.BuildPromptOptions{
		Presentation:              state.ActionPresentationSyncFull,
		TranscriptFormat:          transcriptFormat,
		TranscriptOmissionNote:    transcriptOmissionNote,
		NodeReplEvidence:          promptNodeReplEvidence,
		PermissionContext:         permissionContext,
		RetainedContext:           retainedContext,
		PreviousRetainedOmissions: previousOmissions,
	}
	var prompt string
	var promptParts []state.GuardianPromptPart
	var promptCosts []state.GuardianPromptSectionCost
	buildPrompt := func() error {
		if transcriptEntries != nil || nativeToolCall {
			var buildErr error
			promptParts, promptCosts, buildErr = state.BuildPromptPartsWithSectionCosts(action, transcriptEntries, buildOptions)
			return buildErr
		}
		var buildErr error
		prompt, promptCosts, buildErr = state.BuildPromptWithSectionCosts(action, transcript, buildOptions)
		return buildErr
	}
	var rootAuthorization []state.RootMessage
	if r.rootUserAuthorization != nil {
		rootAuthorization = r.rootUserAuthorization(threadID, turnID)
		if len(rootAuthorization) > 0 {
			buildOptions.RootUserAuthorization = rootAuthorization
		}
	}
	var environmentItems []any
	environmentSource := r.environment
	if reviewOrigin != nil && r.environmentForOrigin != nil {
		environmentSource = func(ctx context.Context, threadID string, turnID string) ([]any, error) {
			return r.environmentForOrigin(ctx, threadID, turnID, reviewOrigin)
		}
	}
	if environmentSource != nil {
		var environmentErr error
		environmentItems, environmentErr = environmentSource(reviewCtx, threadID, turnID)
		err = environmentErr
		if err != nil {
			rationale := guardianReviewFailureRationale(err.Error())
			message := guardianReviewFailureMessage(err.Error())
			completed, _ := store.FailClosed(event.ID, rationale)
			emit(threadID, completed)
			r.recordNonDenial(threadID, turnID)
			attribution.Decision = state.DecisionDenied
			attribution.TerminalStatus = "failed_closed"
			attribution.FailureReason = "session_error"
			emitReviewMetrics()
			r.emitWarning(threadID, rationale)
			return state.DecisionDenied, nil, message, nil
		}
	}
	var (
		admittedLedger      *codexctx.GuardianAdmittedImages
		admittedImages      []codexctx.GuardianImageRef
		reviewedEvidence    uint64
		hasReviewedEvidence bool
	)
	contextWindow, windowKnown := int64(0), false
	if r.syncImageContextWindow != nil {
		contextWindow, windowKnown = r.syncImageContextWindow(threadID, plan.Selection.Model)
	}
	// Rust #44281 (core/src/guardian/input_budget.rs::finalize): evidence is
	// selected only after the rest of the request is resolved, against the
	// reviewer's complete-request budget. Optional evidence is omitted
	// oldest-first with the shared omission notice, and required evidence that
	// cannot fit rejects the review instead of sending an oversized request.
	budgetLimit := effectiveGuardianInputTokenLimit(contextWindow, windowKnown)
	outputSchema := guardianAssessmentOutputSchema()
	historyTokens := int64(0)
	if session != nil {
		historyTokens = session.lastUsageTokens()
	}
	composeRequest := func() (string, []any, error) {
		if buildErr := buildPrompt(); buildErr != nil {
			return "", nil, buildErr
		}
		composedPrompt := prompt
		var promptInputItems []any
		if promptParts != nil {
			composedPrompt, promptInputItems = guardianPromptDelivery(promptParts)
		}
		inputItems := append([]any(nil), promptInputItems...)
		inputItems = append(inputItems, environmentItems...)
		admittedLedger = nil
		admittedImages = nil
		imageItemStart := len(inputItems)
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
		imageItemEnd := len(inputItems)
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
		// Rust #40420 keeps the reviewer policy in a separate developer item with
		// the guardian.policy content kind, making it auditable and preserving it
		// independently from ordinary prompt instructions.
		if fragment := codexctx.NewGuardianPolicy(plan.Instructions); fragment != nil {
			if item := buildRenderedMessage(codexctx.Render(fragment)); item != nil {
				inputItems = append(inputItems, item)
			}
		}
		// Rust prepare_sync_images (#57494): the complete prompt must stay within the
		// fixed image-item budget and the review model's remaining context window.
		// A missing window is treated as unknown and rejects new images.
		if imageItemEnd > imageItemStart {
			promptTokens := guardianInputItemsTokenCount(inputItems)
			if !guardianAdmitImages(promptTokens, historyTokens, contextWindow, windowKnown) {
				inputItems = append(inputItems[:imageItemStart:imageItemStart], inputItems[imageItemEnd:]...)
				admittedLedger = nil
				admittedImages = nil
			}
		}
		return composedPrompt, inputItems, nil
	}
	if nodeReplEvidence != nil {
		reviewedEvidence = nodeReplEvidence.Sequence()
		hasReviewedEvidence = true
	}
	// Budget omission is a local delivery decision, not new evidence, so the
	// staleness comparison keeps the selection the review started from.
	budgetCompareEntries := append([]state.GuardianTranscriptEntry(nil), transcriptEntries...)
	budgetNoteApplied := false
	var inputItems []any
	for {
		var composeErr error
		prompt, inputItems, composeErr = composeRequest()
		if composeErr != nil {
			rationale := guardianReviewFailureRationale(composeErr.Error())
			message := guardianReviewFailureMessage(composeErr.Error())
			attribution.Decision = state.DecisionDenied
			attribution.TerminalStatus = "failed_closed"
			attribution.FailureReason = "prompt_build_error"
			emitReviewMetrics()
			r.emitWarning(threadID, rationale)
			return state.DecisionDenied, nil, message, nil
		}
		requestTokens := guardianCompleteRequestTokens(prompt, inputItems, outputSchema, historyTokens)
		if requestTokens <= budgetLimit {
			break
		}
		// Rust #44570 reserves the omission notice before reducing evidence, so
		// the reviewer knows that instructions or approvals may be incomplete.
		if !budgetNoteApplied {
			buildOptions.TranscriptOmissionNote = guardianBudgetOmissionNote(transcriptOmissionNote)
			budgetNoteApplied = true
			continue
		}
		reduced, dropped := guardianDropOldestOptionalEvidence(transcriptEntries)
		if dropped {
			transcriptEntries = reduced
			if transcriptEntries != nil {
				transcript = state.RenderGuardianTranscript(transcriptEntries, transcriptFormat)
			}
			continue
		}
		// Optional evidence is exhausted. Rust may now shorten older user
		// instructions and manual approvals, oldest first, while later
		// restrictions stay complete whenever older entries can supply room.
		if guardianShortenOldestHistoricalEvidence(transcriptEntries, transcriptFormat, requestTokens-budgetLimit) {
			transcript = state.RenderGuardianTranscript(transcriptEntries, transcriptFormat)
			continue
		}
		// Required evidence remains and cannot fit the reviewer's
		// complete-request budget. Rust #44569 defers an optional review to the
		// user so the approval can still be answered, while a review the policy
		// requires fails closed because the model must see the complete action.
		if !requiredReview {
			rationale := guardianReviewFailureRationale(guardianInputBudgetMessage)
			// Rust #44569 completes an exhausted optional review as aborted with
			// no ReviewDecision, which is what makes the approval fall back to the
			// user instead of being denied.
			completed, abortErr := store.Abort(event.ID, rationale)
			if abortErr == nil {
				emit(threadID, completed)
			}
			r.recordNonDenial(threadID, turnID)
			attribution.Decision = state.DecisionAborted
			attribution.TerminalStatus = "aborted"
			attribution.FailureReason = "input_budget"
			emitReviewMetrics()
			r.emitWarning(threadID, rationale)
			return state.DecisionAskUser, nil, guardianInputBudgetMessage, nil
		}
		// A required review fails closed without sampling. Rust reports the same
		// rejection to the parent.
		rationale := guardianReviewFailureRationale(guardianInputBudgetMessage)
		completed, _ := store.FailClosed(event.ID, rationale)
		emit(threadID, completed)
		r.recordNonDenial(threadID, turnID)
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "input_budget"
		emitReviewMetrics()
		r.emitWarning(threadID, rationale)
		return state.DecisionDenied, nil, guardianReviewFailureMessage(guardianInputBudgetMessage), nil
	}
	r.emitGuardianContextMetrics(promptCosts, prompt, plan.Instructions, inputItems)
	reviewRequest := &model.AgentRequest{
		Prompt:          prompt,
		Instructions:    "",
		InputItems:      inputItems,
		Model:           plan.Selection.Model,
		ProviderID:      plan.ProviderID,
		Endpoint:        r.reviewEndpointValue(threadID, turnID),
		ReasoningEffort: plan.Selection.ReasoningEffort,
		// Rust #44574 configures the review session from the reviewed action's
		// captured reasoning summary, not the current catalog default.
		ReasoningSummary: plan.ReasoningSummary,
		// Rust #52689 forwards the parent turn's Cyber program to the reviewer
		// (turn_start_options.cyber_access_program), normalized to Blue for
		// either Daybreak selection.
		CyberAccessProgram: plan.CyberAccessProgram,
		TaskKind:           model.AgentTaskReview,
		ThreadID:           threadID,
		SessionID:          plan.SessionID,
		TurnID:             classificationTurnID,
		Originator:         "guardian",
		// Rust #40742 preserves the parent's persistence setting for the
		// reviewer session; Go carries it as the request's ephemeral intent.
		Ephemeral: plan.Ephemeral,
		// Rust scopes a Guardian review session's Responses cache to the
		// reviewed parent thread (guardian_prompt_cache_key override).
		PromptCacheKey: guardianPromptCacheKeyOverride(guardianReviewerName, threadID),
		OutputSchema:   guardianAssessmentOutputSchema(),
		// Rust #57710: the reviewer request reuses the parent response's edge
		// route. The pin is transport-only and never enters the prompt,
		// history, or client metadata.
		RoutingAffinity: parentResponse.affinity,
		ClientMetadata:  r.reviewClientMetadata(threadID, turnID, classificationTurnID, targetItemID, parentResponse.id),
	}
	if plan.Prepared != nil && plan.Prepared.PermissionProfile != nil {
		reviewRequest.PermissionProfile = plan.Prepared.PermissionProfile
	}
	if reviewRequest.PermissionProfile == nil && r.permissionProfile != nil {
		reviewRequest.PermissionProfile = r.permissionProfile(threadID, turnID)
	}
	feedbackCapture.history = guardianFeedbackHistoryInput(prompt, inputItems)
	if session != nil {
		session.SeedForReview(reviewRequest)
	}
	if session != nil {
		historyCheckpoint = session.reviewHistoryCheckpoint()
		historyCheckpointReady = true
	}
	if history := r.guardianConversationHistoryValue(threadID, turnID); history != nil && len(history.Tools) > 0 {
		response, err = runGuardianReviewWithHistory(reviewCtx, session, reviewRequest, history)
	} else {
		response, err = session.Run(reviewCtx, reviewRequest)
	}
	if err == nil {
		// The review request reached the session, so the retained-section
		// omission state is delivered; a later composition may suppress it.
		r.markRetainedOmissionsDelivered(threadID, pendingOmissions)
	}
	if session != nil {
		// Rust records the reviewed-transcript cursor once a review ran in the
		// guardian session.
		session.markPriorReview()
	}
	var staleAuthorization bool
	if err == nil && r.guardianReviewContextChanged(threadID, turnID, transcript, budgetCompareEntries, transcriptOmissionNote, rootAuthorization, omittedCallID) {
		// Rust #47819 only invalidates a completed allow: a deny stands on the
		// evidence it saw, while an approval must match the latest authorization.
		if parsed, parseErr := state.ParseAssessment([]byte(guardianAssessmentText(response))); parseErr == nil && parsed.Outcome == state.OutcomeAllow {
			staleAuthorization = true
			contextChanged = true
			// Rust #53987 treats stale authorization as a local evidence refresh,
			// not a failed service request: yield, check cancellation and the
			// shared deadline, then retry without backoff.
			if attempts < guardianMaxReviewAttempts {
				runtime.Gosched()
				if waitErr := reviewCtx.Err(); waitErr != nil {
					err = waitErr
				} else {
					r.emitGuardianReviewRetryMetrics("stale_authorization", time.Now(), nil)
					if session != nil {
						session.rollbackReviewHistory(historyCheckpoint)
					}
					goto reviewAttempt
				}
			}
		}
	}
	if err == nil && staleAuthorization && attempts >= guardianMaxReviewAttempts {
		if session != nil {
			session.rollbackReviewHistory(historyCheckpoint)
		}
		// Rust #47819: exhausting the retry budget on changed authorization is a
		// failed-closed denial, never a stale approval.
		message := "authorization changed before approval could be applied"
		rationale := guardianReviewFailureRationale(message)
		failedClosed, _ := store.FailClosed(event.ID, rationale)
		emit(threadID, failedClosed)
		r.recordNonDenial(threadID, turnID)
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "stale_authorization"
		emitReviewMetrics()
		r.emitWarning(threadID, rationale)
		return state.DecisionDenied, nil, guardianReviewFailureMessage(message), nil
	}
	if err != nil && attempts < guardianMaxReviewAttempts && isContextWindowExceededError(err) {
		// Rust #41215 rolls the reviewer over before the next review. Go has no
		// reviewer-session compaction carrier, so start a fresh response chain;
		// the next attempt replays the complete transcript and evidence.
		if session != nil {
			session.RolloverForContext()
			contextChanged = true
			runtime.Gosched()
			waitStarted := time.Now()
			waitErr := reviewCtx.Err()
			r.emitGuardianReviewRetryMetrics("session_error", waitStarted, waitErr)
			if waitErr != nil {
				if errors.Is(waitErr, context.DeadlineExceeded) {
					return finishTimedOut()
				}
				return finishAborted()
			}
			goto reviewAttempt
		}
	}
	if err != nil && attempts < guardianMaxReviewAttempts && guardianReviewRetryableError(err) {
		// Rust should_retry_guardian_review: recoverable session failures keep
		// the same attempt budget and pay the service backoff, honouring the
		// server's Retry-After when it asks for a longer wait.
		delay := guardianReviewRetryDelay(attempts)
		retryAfter := codexapi.RetryDelay(err)
		if retryAfter > delay {
			delay = retryAfter
		}
		if deadline, ok := reviewCtx.Deadline(); ok {
			now := time.Now()
			slog.Debug("Guardian review retry scheduled",
				"attempt_count", attempts,
				"next_attempt", attempts+1,
				"max_attempts", guardianMaxReviewAttempts,
				"failure_reason", "session",
				"backoff_ms", delay.Milliseconds(),
				"retry_not_before_ms", retryAfter.Milliseconds(),
				"remaining_budget_ms", deadline.Sub(now).Milliseconds(),
				"deadline_limited", now.Add(delay).After(deadline),
			)
		}
		waitStarted := time.Now()
		waitErr := waitGuardianReviewRetry(reviewCtx, delay)
		r.emitGuardianReviewRetryMetrics("session_error", waitStarted, waitErr)
		if waitErr != nil {
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
		emit(threadID, failedClosed)
		r.recordNonDenial(threadID, turnID)
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "session_error"
		emitReviewMetrics()
		r.emitWarning(threadID, rationale)
		return state.DecisionDenied, nil, message, nil
	}
	assessment, err = state.ParseAssessment([]byte(guardianAssessmentText(response)))
	if err != nil && attempts < guardianMaxReviewAttempts {
		// Rust retries Parse failures inside the same attempt budget.
		waitStarted := time.Now()
		waitErr := waitGuardianReviewRetry(reviewCtx, guardianReviewRetryDelay(attempts))
		r.emitGuardianReviewRetryMetrics("parse_error", waitStarted, waitErr)
		if waitErr != nil {
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
		emit(threadID, failedClosed)
		r.recordNonDenial(threadID, turnID)
		attribution.Decision = state.DecisionDenied
		attribution.TerminalStatus = "failed_closed"
		attribution.FailureReason = "parse_error"
		emitReviewMetrics()
		r.emitWarning(threadID, rationale)
		return state.DecisionDenied, nil, message, nil
	}
	completed, err := store.Complete(event.ID, *assessment)
	if err != nil {
		attribution.Decision = state.DecisionAborted
		attribution.TerminalStatus = "aborted"
		attribution.FailureReason = "session_error"
		emitReviewMetrics()
		return state.DecisionAborted, nil, "", err
	}
	if r.logAssessment != nil && !nativeToolCall {
		r.logAssessment(threadID, completed)
	}
	emit(threadID, completed)
	if session != nil {
		// Rust complete_review: a completed assessment advances the transcript
		// cursor and the completed-review count, unlike a failed-but-admitted
		// attempt which only admits its input (#54264).
		session.completeReview()
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
	return decision, assessment, rationale, nil
}

// retainedOmissionDelivery returns the omission state the thread's reviewer
// conversation already received. A missing entry is unknown, so the first
// review always delivers the notices.
func (r *modelGuardianReviewer) retainedOmissionDelivery(threadID string) *state.GuardianRetainedOmissions {
	if r == nil {
		return nil
	}
	r.retainedOmissionsMu.Lock()
	defer r.retainedOmissionsMu.Unlock()
	if value, ok := r.retainedOmissions[threadID]; ok {
		delivered := value
		return &delivered
	}
	return nil
}

func (r *modelGuardianReviewer) markRetainedOmissionsDelivered(threadID string, value state.GuardianRetainedOmissions) {
	if r == nil {
		return
	}
	r.retainedOmissionsMu.Lock()
	defer r.retainedOmissionsMu.Unlock()
	if r.retainedOmissions == nil {
		r.retainedOmissions = map[string]state.GuardianRetainedOmissions{}
	}
	r.retainedOmissions[threadID] = value
}

// guardianParentResponse is the reviewed parent response and the routing pin
// captured with it, snapshotted for one review and its retries (Rust #57710).
type guardianParentResponse struct {
	id       string
	affinity *network.RoutingAffinity
}

// guardianParentResponseSnapshot resolves the reviewed turn's newest response
// once per review. A pin without an id is meaningless: the reviewer only
// applies an affinity to a request whose parent response matches.
func guardianParentResponseSnapshot(r *modelGuardianReviewer, threadID, turnID string) guardianParentResponse {
	if r == nil {
		return guardianParentResponse{}
	}
	if r.parentResponse != nil {
		id, affinity := r.parentResponse(threadID, turnID)
		id = strings.TrimSpace(id)
		if id == "" {
			return guardianParentResponse{}
		}
		return guardianParentResponse{id: id, affinity: affinity}
	}
	if r.latestResponseID != nil {
		return guardianParentResponse{id: strings.TrimSpace(r.latestResponseID(threadID, turnID))}
	}
	return guardianParentResponse{}
}

// guardianClassificationTurnID mints a UUIDv7 identity for one Guardian
// classification. Rust #41385 gives every classification its own turn id, so
// reusing a Guardian session or WebSocket never merges two reviews into one
// turn.
func guardianClassificationTurnID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

// classificationTurnID returns the identity for the review about to run. Each
// call mints a new turn; retries reuse the value captured before the attempt
// loop (Rust #41385).
func (r *modelGuardianReviewer) classificationTurnID() string {
	if r != nil && r.newClassificationTurnID != nil {
		if id := strings.TrimSpace(r.newClassificationTurnID()); id != "" {
			return id
		}
	}
	return guardianClassificationTurnID()
}

// reviewClientMetadata builds the guardian review request's client metadata.
// Rust #44298 attributes guardian reviews to the `guardian_review` turn trigger
// and emits it in x-codex-turn-metadata. Rust #41385 separates the identities:
// classificationTurnID is the reviewer's own turn, parentTurnID is the turn
// that requested the review, and root_turn_id is the parent's trusted causal
// root (omitted when the parent has no recorded lineage).
func (r *modelGuardianReviewer) reviewClientMetadata(threadID, parentTurnID, classificationTurnID, targetItemID, parentResponseID string) map[string]string {
	installationID := ""
	if r != nil && r.installationID != nil {
		installationID = strings.TrimSpace(r.installationID())
	}
	metadata := codexapi.NewClientMetadata(installationID, threadID, threadID, "")
	metadata.RequestKind = codexapi.ClientRequestTurn
	metadata.TurnTrigger = "guardian_review"
	metadata.TurnID = strings.TrimSpace(classificationTurnID)
	metadata.ParentTurnID = strings.TrimSpace(parentTurnID)
	if r != nil && r.trustedRootTurnID != nil {
		if root, ok := r.trustedRootTurnID(threadID, parentTurnID); ok {
			metadata.RootTurnID = root
		}
	}
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
	if parent := strings.TrimSpace(parentResponseID); parent != "" {
		client["parent_response_id"] = parent
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
		mcp.IsNodeReplBackedConnector(strings.TrimSpace(action.Server), action.ConnectorID) &&
		strings.TrimSpace(action.ToolName) == "js"
}

// planForTurn resolves the reviewer plan the review request uses. A reviewer
// without a resolver keeps the pre-selection shape: no explicit model, effort,
// or instructions, so the agent's own defaults apply.
func (r *modelGuardianReviewer) planForTurn(ctx context.Context, threadID, turnID string) guardianReviewPlan {
	if r == nil {
		return guardianReviewPlan{}
	}
	if r.reviewPlanContext != nil {
		return r.reviewPlanContext(ctx, threadID, turnID)
	}
	if r.reviewPlan == nil {
		return guardianReviewPlan{}
	}
	return r.reviewPlan(threadID, turnID)
}

// modelForTurn keeps the model-only view used by review metrics and telemetry.
func (r *modelGuardianReviewer) modelForTurn(threadID, turnID string) string {
	return strings.TrimSpace(r.planForTurn(context.Background(), threadID, turnID).Selection.Model)
}

// guardianActionExceedsReviewBudget reports whether one action alone cannot fit
// the reviewer's complete-request budget. Rust #44569 stops cached asynchronous
// scores from covering oversized actions (including expanded approval
// arguments) and routes them to synchronous review; Go has no async scorer, so
// the same guard keeps the serving-assessment fast path from approving an
// action the synchronous review could never admit.
func (r *modelGuardianReviewer) guardianActionExceedsReviewBudget(threadID, turnID string, action state.Action, session *guardianSessionRunner) bool {
	if r == nil {
		return false
	}
	contextWindow, windowKnown := int64(0), false
	if r.syncImageContextWindow != nil {
		contextWindow, windowKnown = r.syncImageContextWindow(threadID, r.modelForTurn(threadID, turnID))
	}
	budget := effectiveGuardianInputTokenLimit(contextWindow, windowKnown)
	if budget <= 0 {
		return true
	}
	text, _, err := state.FormatGuardianActionPretty(action)
	if err != nil {
		return true
	}
	tokens := int64(utils.ApproxTokenCount(text))
	if session != nil {
		tokens += session.lastUsageTokens()
	}
	return tokens > budget
}

func (r *modelGuardianReviewer) emit(threadID string, event *state.Event) {
	if r != nil && r.notify != nil && event != nil {
		r.notify(threadID, event)
	}
}

func (r *modelGuardianReviewer) reviewEndpointValue(threadID, turnID string) model.ResponsesEndpoint {
	if r == nil || r.reviewEndpoint == nil {
		return model.ResponsesEndpointResponses
	}
	return r.reviewEndpoint(threadID, turnID)
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
		r.recordNonDenial(threadID, turnID)
		return
	}
	policy := state.CircuitBreakerPolicyStandard
	if r.specialty != nil && strings.TrimSpace(r.specialty(threadID, turnID)) == model.ModelSpecialtyCyber {
		policy = state.CircuitBreakerPolicyCyber
	}
	target, ok := r.accountingTurnID(threadID, turnID)
	if !ok {
		return
	}
	action := r.breaker.RecordDenialWithPolicy(target, policy)
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
		r.interrupt(threadID, target, reason)
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

const guardianTranscriptOmissionNotice = state.GuardianTranscriptOmissionNotice

// guardianNativeToolCallTokenLimit is Rust's hard ceiling for a preserved
// native MAv2 call: `entry_limits.tool_tokens.min(9_000)` (#57448).
const guardianNativeToolCallTokenLimit = 9_000

// guardianNativeMultiAgentNamespace resolves Rust's
// `profile.transcript.options.native_multi_agent_namespace`: native calls are
// preserved only when `guardian_native_multi_agent_history` is enabled on an
// MAv2 turn, and the configured tool namespace falls back to `functions`.
func (r *RuntimeRouter) guardianNativeMultiAgentNamespace(threadID string) string {
	if r == nil {
		return ""
	}
	threadID = strings.TrimSpace(threadID)
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: threadID})
	if err != nil || cfg == nil {
		return ""
	}
	if !features.Enabled(cfg.FeatureSettings(), "guardian_native_multi_agent_history") {
		return ""
	}
	agentsConfig, agentsErr := cfg.AgentsConfig(r.configBaseDirForAgents())
	if agentsErr != nil {
		agentsConfig = nil
	}
	if r.runtimeMultiAgentVersionForTurn(threadID, cfg, agentsConfig) != agent.VersionV2 {
		return ""
	}
	if v2Config, v2Err := cfg.MultiAgentV2Config(0); v2Err == nil && v2Config != nil {
		if namespace := strings.TrimSpace(v2Config.ToolNamespace); namespace != "" {
			return namespace
		}
	}
	return "functions"
}

// guardianTranscriptSelection is the host-selected, profile-retained transcript
// plus the retention notices that must accompany the prompt (#43806).
type guardianTranscriptSelection struct {
	Entries      []state.GuardianTranscriptEntry
	OmissionNote string
	Truncations  []state.TranscriptTruncationObservation
}

// guardianReviewTranscriptEntries returns the retained transcript entries.
func (r *RuntimeRouter) guardianReviewTranscriptEntries(threadID string) []state.GuardianTranscriptEntry {
	return r.guardianReviewTranscriptSelection(threadID).Entries
}

// guardianTranscriptRetainedSource resolves the retained original one
// transcript user entry proves delivered (#48110). Rust requires a complete
// user source whose message id and turn match the item, and an asynchronous
// copy proves delivery only when its retained-source-order rendering fits the
// instruction budget; otherwise the original must be redelivered.
func guardianTranscriptRetainedSource(
	context *retainedctx.RetainedContext,
	item *session.Item,
	index int,
	truncated bool,
	target state.ContextTarget,
) (*retainedctx.RetainedSource, string) {
	// Legacy checkpoints have no comparable acceptance order, so their
	// positional labels cannot prove reusable delivery through a transcript
	// copy (Rust filters the whole retained context in that case).
	if context == nil || item == nil || truncated || state.HasLegacyRetainedOrder(context) {
		return nil, ""
	}
	turnID := runtimeSessionItemTurnID(item, index)
	itemID := strings.TrimSpace(item.ID)
	responseItemID := strings.TrimSpace(item.ResponseItemID)
	budget := state.RetainedInstructionByteBudget()
	for _, labeled := range state.RetainedSourceOrderLabels(context) {
		message := labeled.Entry.UserMessage
		if message == nil {
			continue
		}
		source := context.Source(labeled.Entry)
		if source == nil || !source.Complete || source.ID.Role != retainedctx.RetainedSourceRoleUser {
			continue
		}
		sourceID := strings.TrimSpace(source.ID.MessageID)
		if source.ID.TurnID != turnID ||
			sourceID == "" ||
			(sourceID != itemID && sourceID != responseItemID) {
			continue
		}
		rendered := "Retained source order: " + labeled.Label + "\n" +
			state.RootMessage{Kind: state.RootMessageUser, Text: message.Text}.Render()
		if target == state.ContextTargetAsync && len(rendered) > budget {
			return nil, ""
		}
		captured := *source
		return &captured, labeled.Label
	}
	return nil, ""
}

// guardianReviewTranscriptSelection collects the full host snapshot, applies
// the shared synchronous profile's per-entry caps and aggregate retention, and
// preserves original entry numbering for JSON records (#43806, #51465).
func (r *RuntimeRouter) guardianReviewTranscriptSelection(threadID string) guardianTranscriptSelection {
	profile := state.SynchronousContextProfile()
	// Rust #57176 resolves the node_repl output cap from the turn's evidence
	// mode before transcript preparation; a disabled evidence mode keeps the
	// ordinary tool entry cap.
	profile.Transcript.EntryLimits.NodeReplOutputTokens = r.guardianNodeReplResultTokenLimit(threadID)
	// Rust #57448 resolves the native MAv2 namespace from the reviewed turn, so
	// only an enabled feature on a V2 turn preserves native calls.
	profile.Transcript.Options.NativeMultiAgentNamespace = r.guardianNativeMultiAgentNamespace(threadID)
	return r.guardianReviewTranscriptSelectionWithProfile(threadID, profile)
}

// guardianReviewTranscriptSelectionWithProfile applies a caller-resolved
// profile to one host snapshot, so synchronous and asynchronous consumers share
// the same collection and retention path (#41422, #43806).
func (r *RuntimeRouter) guardianReviewTranscriptSelectionWithProfile(threadID string, profile state.ContextProfile) guardianTranscriptSelection {
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, true)
	if err != nil || record == nil {
		return guardianTranscriptSelection{}
	}
	items := r.guardianReviewHistoryItems(threadID, record)
	retainedContext := r.guardianRetainedContextForTurn(threadID, "")
	toolCalls := map[string]session.Item{}
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "function_call", "custom_tool_call", "tool_search_call", "local_shell_call", "web_search_call":
			if callID := strings.TrimSpace(item.CallID); callID != "" {
				toolCalls[callID] = item
			}
		}
	}
	entries := make([]state.GuardianTranscriptEntry, 0, len(items))
	evidence := make([]state.TranscriptEvidenceEntry, 0, len(items))
	seenNativeAgentMessageIDs := map[string][]byte{}
	for i := range items {
		item := items[i]
		// Rust #54316 (guardian-context/src/history.rs): compaction summaries are
		// excluded from the transcript while the surrounding user messages
		// survive, including a message whose text matches the summary.
		if guardianTranscriptExcludedItem(&item, profile.Transcript.Options.IncludeReasoning) ||
			!guardianTranscriptOptionsIncludeItem(&item, profile.Transcript.Options) {
			continue
		}
		// Rust #49038: encrypted agent messages carry evidence such as a
		// parent reply. They cannot be truncated, so the complete native item
		// is delivered in order, or replaced whole by an omission notice.
		if native, ok := guardianNativeAgentMessage(&item); ok {
			if id := strings.TrimSpace(item.ID); id != "" {
				if previous, duplicate := seenNativeAgentMessageIDs[id]; duplicate {
					if bytes.Equal(previous, native) {
						continue
					}
					// A conflicting payload under one ID is invalid evidence.
					// Skip it rather than admitting ambiguous reviewer context.
					continue
				}
				seenNativeAgentMessageIDs[id] = append([]byte(nil), native...)
			}
			// Rust #57241: encrypted agent messages use the configured message
			// entry cap directly instead of a hard-coded 9,000-token ceiling, so a
			// larger configured limit admits larger native items. The whole item
			// still becomes an omission notice at the same position when it exceeds
			// the budget.
			if len(native) > utils.ApproxBytesForTokens(profile.Transcript.EntryLimits.MessageTokens) {
				entries = append(entries, state.GuardianTranscriptEntry{
					Author:   "assistant",
					LineRole: "assistant",
					Text:     guardianTranscriptOmissionNotice,
				})
				evidence = append(evidence, state.TranscriptEvidenceEntry{
					Kind:          state.GuardianTranscriptProtectedMessage,
					Role:          "assistant",
					Text:          guardianTranscriptOmissionNotice,
					OriginalBytes: 0,
				})
			} else {
				entries = append(entries, state.GuardianTranscriptEntry{Author: "assistant", Native: native})
				// Native ciphertext cannot be text-truncated or rendered. Charge
				// its serialized cost to the protected-message pool so retention
				// still bounds the total evidence, while keeping it out of the
				// async text-truncation observations.
				evidence = append(evidence, state.TranscriptEvidenceEntry{
					Kind:   state.GuardianTranscriptProtectedMessage,
					Role:   "assistant",
					Tokens: utils.ApproxTokenCount(string(native)),
				})
			}
			continue
		}
		// Rust #57448: an enabled native MAv2 history keeps the complete
		// function call so the backend can decrypt it, or replaces the whole
		// item with an omission notice when it exceeds the tool-entry cap.
		if native, ok := guardianNativeMultiAgentCall(&item, profile.Transcript.Options.NativeMultiAgentNamespace); ok {
			name := guardianNativeFunctionCallName(&item)
			lineRole := "tool " + name + " call"
			if state.GuardianNativeFunctionCallTokens(native) <= min(profile.Transcript.EntryLimits.ToolTokens, guardianNativeToolCallTokenLimit) {
				entries = append(entries, state.GuardianTranscriptEntry{
					Author:   "tool",
					Label:    name,
					LineRole: lineRole,
					Native:   native,
				})
				evidence = append(evidence, state.TranscriptEvidenceEntry{
					Kind:          state.GuardianTranscriptTool,
					Role:          lineRole,
					Tokens:        state.GuardianNativeFunctionCallTokens(native),
					OriginalBytes: len(native),
				})
			} else {
				entries = append(entries, state.GuardianTranscriptEntry{
					Author:   "tool",
					Label:    name,
					LineRole: lineRole,
					Text:     guardianTranscriptOmissionNotice,
				})
				evidence = append(evidence, state.TranscriptEvidenceEntry{
					Kind:          state.GuardianTranscriptTool,
					Role:          lineRole,
					Text:          guardianTranscriptOmissionNotice,
					OriginalBytes: len(native),
				})
			}
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
			itemType := strings.ToLower(strings.TrimSpace(item.Type))
			if strings.HasSuffix(itemType, "_output") {
				text = "[non-text output]"
			} else if itemType == "local_shell_call" || itemType == "web_search_call" || itemType == "tool_search_call" {
				if encoded, err := json.Marshal(item.Data); err == nil {
					text = string(encoded)
				}
			}
		}
		if text == "" {
			continue
		}
		if lineRole, label, include, isTool := guardianToolTranscriptRole(&item, toolCalls); isTool {
			if !include {
				continue
			}
			tokenLimit := profile.Transcript.EntryLimits.ToolTokens
			if mcp.IsNodeReplBackedServer(strings.TrimSpace(item.Namespace)) ||
				mcp.IsNodeReplBackedServer(strings.TrimSpace(item.Name)) {
				tokenLimit = profile.Transcript.EntryLimits.NodeReplOutputTokens
			}
			limited, _ := truncateGuardianTranscriptEntry(text, tokenLimit)
			entries = append(entries, state.GuardianTranscriptEntry{
				Author:   "tool",
				Label:    label,
				LineRole: lineRole,
				Text:     limited,
			})
			evidence = append(evidence, state.TranscriptEvidenceEntry{
				Kind:          state.GuardianTranscriptTool,
				Role:          lineRole,
				Text:          limited,
				OriginalBytes: len(text),
			})
			continue
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
		kind := guardianTranscriptKindForItem(&item, role)
		tokenLimit := profile.Transcript.EntryLimits.MessageTokens
		if kind == state.GuardianTranscriptTool {
			tokenLimit = profile.Transcript.EntryLimits.ToolTokens
			nodeRepl := mcp.IsNodeReplBackedServer(strings.TrimSpace(item.Namespace)) ||
				mcp.IsNodeReplBackedServer(strings.TrimSpace(item.Name))
			if !nodeRepl {
				if call, ok := toolCalls[strings.TrimSpace(item.CallID)]; ok {
					nodeRepl = mcp.IsNodeReplBackedServer(strings.TrimSpace(call.Namespace)) ||
						mcp.IsNodeReplBackedServer(strings.TrimSpace(call.Name))
				}
			}
			if nodeRepl {
				tokenLimit = profile.Transcript.EntryLimits.NodeReplOutputTokens
			}
		}
		// Rust #44570 keeps user messages and manual approvals complete through
		// transcript collection. They may be shortened only by the final
		// complete-request budget so a later instruction is not lost first.
		limited, truncated := text, false
		if kind != state.GuardianTranscriptUser && kind != state.GuardianTranscriptManualApproval {
			limited, truncated = truncateGuardianTranscriptEntry(text, tokenLimit)
		}
		var retainedSource *retainedctx.RetainedSource
		var retainedOrder string
		if kind == state.GuardianTranscriptUser {
			retainedSource, retainedOrder = guardianTranscriptRetainedSource(retainedContext, &item, i, truncated, profile.Target)
		}
		if role != "" {
			entries = append(entries, state.GuardianTranscriptEntry{
				Author:              guardianTranscriptAuthor(role),
				Label:               guardianTranscriptLabel(role),
				LineRole:            role,
				RetainedSourceOrder: retainedOrder,
				RetainedSource:      retainedSource,
				Text:                limited,
			})
		} else {
			entries = append(entries, state.GuardianTranscriptEntry{
				Author:              guardianTranscriptAuthor(role),
				RetainedSourceOrder: retainedOrder,
				RetainedSource:      retainedSource,
				Text:                limited,
			})
		}
		label := guardianTranscriptLabel(role)
		if label == "" {
			label = role
		}
		// Rust #57519: keep scheduler provenance on the collected entry. Sync
		// compression runs over the complete transcript before suffix selection;
		// async consumers compress during this collection pass.
		scheduled := false
		var heartbeat *state.TranscriptHeartbeat
		if kind == state.GuardianTranscriptUser {
			origin := retainedctx.UserInputOriginFromMessage(role, sessionItemContentItemKinds(&item))
			if origin == retainedctx.UserInputOriginHeartbeat {
				scheduled = true
				if parsed, ok := retainedctx.HeartbeatFromMessage(origin, []string{text}); ok {
					heartbeat = &state.TranscriptHeartbeat{
						AutomationID: parsed.AutomationID,
						Timestamp:    parsed.Timestamp,
						Instructions: parsed.Instructions,
					}
				}
			}
		}
		evidence = append(evidence, state.TranscriptEvidenceEntry{
			Kind:          kind,
			Role:          label,
			Text:          limited,
			OriginalBytes: len(text),
			Scheduled:     scheduled,
			Heartbeat:     heartbeat,
		})
	}
	state.CompressGuardianTranscriptHeartbeats(evidence, 0)
	// Collection keeps entries and evidence parallel; mirror compressed scheduler
	// text onto the host-attributed entries the prompt renders. Native entries
	// carry ciphertext instead of text and are never scheduler messages.
	for index := range entries {
		if len(entries[index].Native) == 0 {
			// Rust clears an asynchronous scheduled copy's source when heartbeat
			// compression rewrites it, so the rewritten entry cannot claim to
			// carry the original revision (#57519, #48110).
			if profile.Target == state.ContextTargetAsync && evidence[index].Scheduled && evidence[index].Text != entries[index].Text {
				entries[index].RetainedSource = nil
				entries[index].RetainedSourceOrder = ""
			}
			entries[index].Text = evidence[index].Text
		}
		entries[index].Async = profile.Target == state.ContextTargetAsync
	}
	rendered := profile.RenderTranscript(evidence, 0)
	retained := make([]state.GuardianTranscriptEntry, 0, len(rendered.Indices))
	for _, index := range rendered.Indices {
		entry := entries[index]
		entry.Index = index
		entry.Optional = guardianTranscriptEvidenceOptional(evidence[index])
		entry.Historical = evidence[index].Kind == state.GuardianTranscriptUser ||
			evidence[index].Kind == state.GuardianTranscriptManualApproval
		retained = append(retained, entry)
	}
	return guardianTranscriptSelection{
		Entries:      retained,
		OmissionNote: rendered.OmissionNote,
		Truncations:  rendered.Truncations,
	}
}

// guardianTranscriptEvidenceOptional reports whether Rust's composed budget may
// omit this evidence (#44281): ordinary assistant commentary and tool
// calls/results are optional, while user messages, developer context, final
// assistant answers and encrypted native evidence are required.
func guardianTranscriptEvidenceOptional(entry state.TranscriptEvidenceEntry) bool {
	switch entry.Kind {
	case state.GuardianTranscriptMessage, state.GuardianTranscriptTool:
		return true
	default:
		return false
	}
}

// guardianTranscriptKindForItem maps one stored item to the profile's retention
// pools. Developer messages and final assistant answers are protected context;
// commentary and compaction summaries use the ordinary message pool.
func guardianTranscriptKindForItem(item *session.Item, role string) state.GuardianTranscriptKind {
	if item != nil {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "function_call", "custom_tool_call", "local_shell_call", "web_search_call",
			"function_call_output", "custom_tool_call_output", "tool_output":
			return state.GuardianTranscriptTool
		}
	}
	switch strings.TrimSpace(role) {
	case "user":
		return state.GuardianTranscriptUser
	case "developer":
		return state.GuardianTranscriptManualApproval
	case "tool":
		return state.GuardianTranscriptTool
	case "assistant":
		if item == nil || guardianTranscriptIsCompactionSummary(item) {
			return state.GuardianTranscriptMessage
		}
		if phase, _ := item.Data["phase"].(string); strings.TrimSpace(phase) == "commentary" {
			return state.GuardianTranscriptMessage
		}
		return state.GuardianTranscriptProtectedMessage
	default:
		return state.GuardianTranscriptMessage
	}
}

// guardianToolTranscriptRole mirrors the shared Guardian transcript
// collector's tool attribution (#41879): calls use "tool <name> call", paired
// outputs inherit the source call's name, and an output whose call is no
// longer present remains a generic "tool result" instead of disappearing.
func guardianToolTranscriptRole(item *session.Item, toolCalls map[string]session.Item) (lineRole, label string, include, isTool bool) {
	if item == nil {
		return "", "", false, false
	}
	itemType := strings.ToLower(strings.TrimSpace(item.Type))
	switch itemType {
	case "function_call", "custom_tool_call", "tool_search_call", "local_shell_call", "web_search_call":
		name := guardianToolTranscriptName(item)
		if name == "" {
			return "", "", false, true
		}
		return "tool " + name + " call", name, true, true
	case "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_output":
		name := ""
		if callID := strings.TrimSpace(item.CallID); callID != "" {
			if call, ok := toolCalls[callID]; ok {
				name = guardianToolTranscriptName(&call)
			}
			if name == "" {
				return "tool result", "tool", true, true
			}
		} else {
			name = guardianToolTranscriptName(item)
			if name == "" {
				return "", "", false, true
			}
		}
		return "tool " + name + " result", name, true, true
	default:
		return "", "", false, false
	}
}

func guardianToolTranscriptName(item *session.Item) string {
	if item == nil {
		return ""
	}
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = firstNonEmpty(
			stringFromAny(item.Data["name"]),
			stringFromAny(item.Data["tool"]),
		)
	}
	namespace := strings.TrimSpace(item.Namespace)
	if namespace == "" {
		namespace = stringFromAny(item.Data["namespace"])
	}
	if name == "" {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "local_shell_call":
			name = "shell"
		case "web_search_call":
			name = "web_search"
		case "tool_search_call", "tool_search_output":
			name = "tool_search"
		}
	}
	if name == "" {
		return ""
	}
	if namespace != "" && !strings.HasPrefix(name, namespace+".") {
		name = namespace + "." + name
	}
	return name
}

// truncateGuardianTranscriptEntry applies the profile's per-entry token cap
// before aggregate retention. Rust's collect_transcript bounds every non-user
// entry with the shared guardian_context::truncate_text primitive, so both
// transcript evidence and request budgeting use the same UTF-8-safe prefix,
// suffix, and `<truncated omitted_approx_tokens="N" />` marker.
func truncateGuardianTranscriptEntry(text string, tokenLimit int) (string, bool) {
	return codexctx.GuardianTruncateText(text, tokenLimit)
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
// the Guardian review transcript. Reasoning is omitted unless the request
// enables it, while context-compaction and compaction-trigger items never carry
// guardrail evidence. A copied manual approval is model context rather than the
// child's own trusted approval evidence (Rust
// guardian-context/src/transcript.rs::is_inherited_manual_approval, #52535), so
// it is dropped as well.
func guardianTranscriptExcludedItem(item *session.Item, includeReasoning bool) bool {
	if item == nil {
		return false
	}
	switch strings.TrimSpace(item.Type) {
	case "reasoning":
		return !includeReasoning
	case "context_compaction", "compaction_trigger":
		return true
	}
	// Rust #46064 (guardian-context/src/transcript.rs): a developer message is
	// the only role the transcript filters by text. The manual-approval marker
	// that authorizes a previously rejected action survives as `developer`
	// evidence; ordinary developer context never reaches the reviewer.
	if guardianTranscriptDropsDeveloperMessage(item) {
		return true
	}
	return guardianTranscriptIsInheritedManualApproval(item)
}

// guardianTranscriptDropsDeveloperMessage reports whether a developer message is
// ordinary context rather than the manual-approval marker the transcript keeps.
func guardianTranscriptDropsDeveloperMessage(item *session.Item) bool {
	if item == nil || !strings.EqualFold(strings.TrimSpace(item.Role), "developer") {
		return false
	}
	return !sessionItemIsManualApprovalMessage(item)
}

// guardianTranscriptOptionsIncludeItem applies the profile's independent source
// flags for tool calls and tool outputs (#41422).
func guardianTranscriptOptionsIncludeItem(item *session.Item, options state.ConversationTranscriptOptions) bool {
	if item == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "function_call", "custom_tool_call", "local_shell_call", "web_search_call":
		return options.IncludeToolCalls
	case "function_call_output", "custom_tool_call_output", "tool_output":
		return options.IncludeToolOutputs
	default:
		return true
	}
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
		ApprovalID:    event.Action.ApprovalID,
		ProcessID:     event.Action.ProcessID,
		Stdin:         event.Action.Chars,
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
		guardianContextMode: event.GuardianContextMode,
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

// guardianActiveTurnID returns the thread's active turn for Guardian denial
// accounting (Rust #44866). An empty result means the review belongs to a turn
// that is no longer running, so nothing is counted against a newer turn.
func (r *RuntimeRouter) guardianActiveTurnID(threadID string) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.threads.ActiveTurnID(strings.TrimSpace(threadID)))
}

func (r *RuntimeRouter) interruptTurnForGuardianCircuitBreaker(threadID, turnID, reason string) {
	if r == nil {
		return
	}
	appErr := r.guardianCircuitBreakError(threadID, reason)
	if r.activeRuntimeTurnSnapshot(threadID) != nil {
		active, ok := r.interruptRuntimeTurnIfNoPendingInput(threadID, turnID)
		if !ok {
			return
		}
		// Rust #49475: abort callbacks complete before the terminal event.
		r.runInterruptHook(active)
		r.finishTurnInterruptedAnalyticsWithError(threadID, turnID, active.StartedAtMS, analyticsContextFromActiveRuntimeTurn(active), turnAbortReasonInterrupted, appErr)
		r.deliverGuardianInterruptionToParent(threadID, reason)
		return
	}
	_, _ = r.requireTurns().Interrupt(&turn.TurnInterruptParams{ThreadID: threadID, TurnID: turnID})
	r.deliverGuardianInterruptionToParent(threadID, reason)
}

// guardianCircuitBreakError returns the opt-in structured error attached to a
// Guardian denial-limit interruption (Rust #48796). Without the strict action,
// older clients continue to receive only the interrupted turn and warning.
func (r *RuntimeRouter) guardianCircuitBreakError(threadID, reason string) *TurnError {
	if r == nil {
		return nil
	}
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{ThreadID: threadID})
	if err != nil || cfg == nil || cfg.GuardianCircuitBreakAction() != "strict" {
		return nil
	}
	return &TurnError{
		Message:        firstNonEmpty(strings.TrimSpace(reason), "Guardian denied the action repeatedly."),
		CodexErrorInfo: CodexErrorInfo("tooManyDenials"),
	}
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
	item := guardianApprovedActionSessionItem(event, text, runtimeRouterNow(r).UTC())
	if _, err := r.runtimeAppendItem(session.ThreadID(params.ThreadID), item); err != nil {
		return nil, err
	}
	_ = r.appendRuntimeRollout(params.ThreadID, []session.Item{item}, item.CreatedAt)
	if active := r.activeRuntimeTurnStateSnapshot(params.ThreadID, event.TurnID); active != nil {
		input := map[string]any{
			"type": "message", "role": "developer",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
			"internal_chat_message_metadata_passthrough": map[string]any{
				"content_item_kinds": []any{codexctx.GuardianApprovedActionKind},
			},
		}
		if err := r.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: params.ThreadID, TurnID: event.TurnID, InputItems: []any{input}}); err != nil {
			return nil, err
		}
	}
	return &ThreadApproveGuardianDeniedActionResponse{}, nil
}

func guardianApprovedActionSessionItem(event state.Event, text string, now time.Time) session.Item {
	return session.Item{
		ID: "guardian-denied-action-approval-" + safeIdentifier(event.ID), Type: "message", Role: "developer", Text: text,
		Content: []session.ContentPart{{Type: "input_text", Text: text}}, CreatedAt: now,
		Metadata: map[string]any{"turnId": event.TurnID, "turn_id": event.TurnID, "guardianReviewId": event.ID, "guardian_review_id": event.ID},
		Data: map[string]any{
			"internal_chat_message_metadata_passthrough": map[string]any{
				"content_item_kinds": []any{codexctx.GuardianApprovedActionKind},
			},
		},
	}
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
	if r.threads != nil && r.threads.ActiveTurn(strings.TrimSpace(params.ThreadID)) != nil {
		// Rust #41562: external context reaching an active turn makes the goal
		// continuation's root attribution ambiguous.
		r.invalidateGoalTurnLineage(params.ThreadID)
	}
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
// #41931-#42085, #42290, #42579-#42852, #47272): review continuity is
// preserved through the bounded parent-compaction response ID, root-conversation
// authorization and node_repl evidence stay in the review prompt, and
// missing/oversized checkpoints fail closed. Rust #47272 drops the synchronous
// reviewer's comp_hash compatibility gate: the reviewer can consume checkpoints
// across advertised hashes and lets the backend validate the payload, so Go
// reuses any usable checkpoint and only refuses to seed when none exists or
// reuse is disabled. Rust's thread-context/structured-subagent history
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
	sessionRunner := runner.existingSessionFor(threadID)
	if sessionRunner == nil {
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
	if threadContext && !reuseParentCompaction {
		// Thread-owned review is the only mode Go enables checkpoint reuse for
		// (guardian_reuse_parent_compaction), so an independent review still
		// clears the seed instead of inheriting the parent checkpoint.
		sessionRunner.DropStaleParentCompaction()
		return
	}
	sessionRunner.ResetAfterParentCompaction(responseID)
}

// resetGuardianAfterParentHistoryReset clears the reusable Guardian reviewer
// when the parent history is replaced destructively without a compaction
// checkpoint (Rust #45413). Cold resumes and forks replay a thread's history
// into a fresh runtime, which Rust models as HistoryReplacement::Reset and
// bumps the reset version that participates in the review-session reuse key.
// Without the reset Go would keep appending to the pre-reset reviewer chain and
// report trunk_reused/had_prior_review_context = true after the history reset.
func (r *RuntimeRouter) resetGuardianAfterParentHistoryReset(threadID string) {
	if r == nil {
		return
	}
	// Rust #41660: a destructive history replacement advances the host-owned
	// user-message revision, so a retained Guardian authorization from before
	// the reset can no longer approve anything.
	r.bumpGuardianUserMessageRevision(threadID)
	if r.services.GuardianReviewer == nil {
		return
	}
	runner, ok := r.services.GuardianReviewer.(*modelGuardianReviewer)
	if !ok {
		return
	}
	sessionRunner := runner.existingSessionFor(threadID)
	if sessionRunner == nil {
		return
	}
	sessionRunner.ResetForParentHistoryReset()
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

// guardianMaxImagePromptTokens is Rust's GUARDIAN_MAX_IMAGE_ITEM_TOKENS: a
// synchronous Guardian prompt above this estimate admits no new images.
const guardianMaxImagePromptTokens = 10_000

// guardianInputItemsTokenCount estimates the review prompt's token cost from
// the serialized user-input items, matching the context-window accounting the
// image admission check needs.
func guardianInputItemsTokenCount(items []any) int64 {
	var total int64
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return math.MaxInt64
		}
		total += int64(utils.ApproxTokenCount(string(encoded)))
	}
	return total
}

// guardianAdmitImages applies Rust prepare_sync_images' admission rule. A known
// context window must leave room for both the prompt and reviewer history; when
// no resolver is wired (unit-level reviewers), only the item budget applies.
func guardianAdmitImages(promptTokens, historyTokens, contextWindow int64, windowKnown bool) bool {
	if promptTokens > guardianMaxImagePromptTokens {
		return false
	}
	if !windowKnown {
		return true
	}
	return promptTokens+historyTokens <= contextWindow
}

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
	case codexapi.ErrorRateLimitExceeded, codexapi.ErrorFlexUnavailable, codexapi.ErrorServerOverloaded,
		codexapi.ErrorPolicyCapacityUnavailable:
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
func (r *modelGuardianReviewer) guardianReviewContextChanged(threadID, turnID string, transcript []string, entries []state.GuardianTranscriptEntry, omissionNote string, rootAuthorization []state.RootMessage, omittedCallID string) bool {
	if r == nil {
		return false
	}
	if r.transcriptSelection != nil {
		selection := r.transcriptSelection(threadID)
		// The proposed call is part of the plan, not a context change, so both
		// sides of the comparison drop it.
		fresh := state.GuardianTranscriptWithoutProposedCall(selection.Entries, omittedCallID)
		if !reflect.DeepEqual(entries, fresh) || omissionNote != selection.OmissionNote {
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
