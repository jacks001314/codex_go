package appserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/execpolicy"
	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/tooltiming"
	"codex_go/turn"
)

type networkApprovalKey struct {
	threadID      string
	environmentID string
	protocol      network.ProxyProtocol
	host          string
	port          uint16
}

type pendingNetworkApproval struct {
	done         chan struct{}
	decision     network.ProxyDecision
	turnID       string
	connectionID string
	cancel       context.CancelFunc
	startedAt    time.Time
	// persistent records that the approval belongs to a process that outlives
	// the tool call, so turn cleanup must not resolve it (Rust #44575).
	persistent bool
}

type pendingNetworkApprovalKey struct {
	networkApprovalKey
	turnID string
	callID string
}

type networkApprovalService struct {
	router  *RuntimeRouter
	mu      sync.Mutex
	pending map[pendingNetworkApprovalKey]*pendingNetworkApproval
	// promptQueues serializes the visible manual prompt for one endpoint so
	// concurrent executions cannot stack duplicate approvals (Rust #58351).
	promptQueues map[networkApprovalKey]*networkApprovalPromptQueue
	allowed      map[networkApprovalKey]struct{}
	denied       map[networkApprovalKey]struct{}
	saved        map[string][]networkRuleSavedFragment
	callsMu      sync.Mutex
	calls        map[string]*activeNetworkApprovalCall
	// executions keeps the originating execution snapshot and cancellation
	// token for processes that outlive their launching tool call (Rust #44575).
	executions map[string]*activeNetworkApprovalCall
}

// networkApprovalPromptQueue is a one-permit endpoint queue. refs counts the
// executions holding or waiting for the permit, so an idle endpoint leaves no
// entry behind.
type networkApprovalPromptQueue struct {
	permits chan struct{}
	refs    int
}

type activeNetworkApprovalCall struct {
	threadID      string
	turnID        string
	callID        string
	environmentID string
	trigger       *guardianNetworkAccessTrigger
	cancel        context.CancelCauseFunc
	outcome       string
	persistent    bool
	// executionCtx is canceled when this execution ends. It is independent of
	// the tool-call context so a background process keeps reviewing after its
	// launching call and turn complete.
	executionCtx    context.Context
	executionCancel context.CancelCauseFunc
	origin          *guardianReviewOrigin
}

func (c *activeNetworkApprovalCall) isCancelled() bool {
	return c != nil && c.executionCtx != nil && c.executionCtx.Err() != nil
}

type guardianNetworkAccessTrigger struct {
	CallID                string                               `json:"callId"`
	Command               []string                             `json:"command"`
	CWD                   string                               `json:"cwd"`
	SandboxPermissions    string                               `json:"sandboxPermissions"`
	AdditionalPermissions *sandbox.AdditionalPermissionProfile `json:"additionalPermissions,omitempty"`
	Justification         *string                              `json:"justification,omitempty"`
	ToolName              string                               `json:"toolName"`
	TTY                   bool                                 `json:"tty"`
}

type networkRuleSavedFragment struct {
	host   string
	action NetworkPolicyRuleAction
}

type networkApprovalTurn struct {
	threadID     string
	turnID       string
	connectionID string
	params       *turn.TurnStartParams
	runConfig    *appTurnRunConfig
}

func newNetworkApprovalService(router *RuntimeRouter) *networkApprovalService {
	return &networkApprovalService{
		router:       router,
		pending:      map[pendingNetworkApprovalKey]*pendingNetworkApproval{},
		promptQueues: map[networkApprovalKey]*networkApprovalPromptQueue{},
		allowed:      map[networkApprovalKey]struct{}{},
		denied:       map[networkApprovalKey]struct{}{},
		saved:        map[string][]networkRuleSavedFragment{},
		calls:        map[string]*activeNetworkApprovalCall{},
		executions:   map[string]*activeNetworkApprovalCall{},
	}
}

func (s *networkApprovalService) Decide(ctx context.Context, request network.ProxyPolicyRequest) network.ProxyDecision {
	return s.decide(ctx, "", request)
}

func (s *networkApprovalService) decideForThread(ctx context.Context, threadID string, request network.ProxyPolicyRequest) network.ProxyDecision {
	return s.decide(ctx, strings.TrimSpace(threadID), request)
}

func (s *networkApprovalService) decide(ctx context.Context, threadID string, request network.ProxyPolicyRequest) network.ProxyDecision {
	if s == nil || s.router == nil {
		return network.AskProxyDecision(network.ProxyReasonNotAllowed)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	active := s.router.activeTurnForNetworkApprovalThread(threadID, request.EnvironmentID)
	if active == nil && strings.TrimSpace(request.ExecutionID) != "" {
		// An owned process keeps its environment even when the active turn
		// selects a different one; review settings still come from that turn
		// (Rust #44575).
		active = s.router.activeTurnForNetworkApprovalThread(threadID, "")
	}
	if active == nil {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	if !s.router.networkApprovalAllowedForTurn(active) {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	key := networkApprovalKey{
		threadID:      active.threadID,
		environmentID: firstNonEmpty(strings.TrimSpace(request.EnvironmentID), "local"),
		protocol:      request.Protocol,
		host:          network.NormalizeProxyHost(request.Host),
		port:          request.Port,
	}
	ownerCall := s.activeCallForApproval(active, key.environmentID, request.ExecutionID)
	if strings.TrimSpace(request.ExecutionID) != "" && ownerCall == nil {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	// Rust #44575 denies a request from an execution that has already ended
	// before consulting the session host grants: a late proxy callback must not
	// resurrect a cancelled process from an earlier approval.
	if ownerCall != nil && ownerCall.isCancelled() {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	ownerTurnID := active.turnID
	if ownerCall != nil && strings.TrimSpace(ownerCall.turnID) != "" {
		ownerTurnID = ownerCall.turnID
	}
	pendingKey := pendingNetworkApprovalKey{networkApprovalKey: key, turnID: ownerTurnID}
	if ownerCall != nil {
		pendingKey.callID = ownerCall.callID
	}

	s.mu.Lock()
	if _, ok := s.denied[key]; ok {
		s.mu.Unlock()
		s.recordUserDenialForThread(active.threadID)
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	if _, ok := s.allowed[key]; ok {
		s.mu.Unlock()
		return network.AllowProxyDecision()
	}
	if pending := s.pending[pendingKey]; pending != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
		case <-pending.done:
			return pending.decision
		}
	}
	approvalCtx, approvalCancel := context.WithCancel(ctx)
	pending := &pendingNetworkApproval{
		done:         make(chan struct{}),
		turnID:       ownerTurnID,
		connectionID: active.connectionID,
		cancel:       approvalCancel,
		startedAt:    time.Now(),
		persistent:   ownerCall != nil && ownerCall.persistent,
	}
	s.pending[pendingKey] = pending
	s.mu.Unlock()

	// Rust #58351: only the visible manual prompt is serialized per endpoint;
	// hook and Guardian decisions proceed independently. The gate keeps this
	// execution's tool result paused while it waits.
	promptGate := &networkApprovalPromptGate{
		service: s, router: s.router, ctx: approvalCtx,
		key: key, pendingKey: pendingKey, pending: pending,
		threadID: active.threadID, turnID: active.turnID, ownerCall: ownerCall,
	}
	defer promptGate.release()

	// Cancelling the owning execution resolves a pending review as denied. A
	// review result that is already accepted wins this race, which is why the
	// execution watcher only cancels the approval context instead of publishing
	// a denial itself (Rust #44575 run_until_cancelled).
	var stopExecutionWatch func() bool
	if ownerCall != nil && ownerCall.executionCtx != nil {
		stopExecutionWatch = context.AfterFunc(ownerCall.executionCtx, approvalCancel)
	}
	decision, cache := s.requestApproval(approvalCtx, active, key, request, ownerCall, promptGate)
	if stopExecutionWatch != nil {
		stopExecutionWatch()
	}
	approvalCancel()
	s.mu.Lock()
	if s.pending[pendingKey] != pending {
		decision = pending.decision
		s.mu.Unlock()
		return decision
	}
	switch cache {
	case NetworkPolicyRuleAllow:
		delete(s.denied, key)
		s.allowed[key] = struct{}{}
	case NetworkPolicyRuleDeny:
		delete(s.allowed, key)
		s.denied[key] = struct{}{}
	}
	pending.decision = decision
	delete(s.pending, pendingKey)
	close(pending.done)
	s.mu.Unlock()
	return decision
}

// acquirePromptQueue takes the endpoint's manual-prompt permit, calling
// onQueued when another execution already holds it. The returned release must
// run after this execution commits or abandons its own decision so the next
// waiter observes the committed session policy.
func (s *networkApprovalService) acquirePromptQueue(ctx context.Context, key networkApprovalKey, onQueued func()) (func(), error) {
	s.mu.Lock()
	queue := s.promptQueues[key]
	if queue == nil {
		queue = &networkApprovalPromptQueue{permits: make(chan struct{}, 1)}
		queue.permits <- struct{}{}
		s.promptQueues[key] = queue
	}
	queue.refs++
	select {
	case <-queue.permits:
		s.mu.Unlock()
		return func() { s.releasePromptQueue(key, queue) }, nil
	default:
	}
	s.mu.Unlock()
	if onQueued != nil {
		onQueued()
	}
	select {
	case <-queue.permits:
		return func() { s.releasePromptQueue(key, queue) }, nil
	case <-ctx.Done():
		s.releasePromptQueue(key, queue)
		return nil, ctx.Err()
	}
}

func (s *networkApprovalService) releasePromptQueue(key networkApprovalKey, queue *networkApprovalPromptQueue) {
	if queue == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case queue.permits <- struct{}{}:
	default:
	}
	queue.refs--
	if queue.refs <= 0 && s.promptQueues[key] == queue {
		delete(s.promptQueues, key)
	}
}

// networkApprovalPromptGate serializes the visible manual prompt for one
// endpoint (Rust #58351). It is created per execution and released when the
// execution commits or abandons its decision, so the next waiter observes the
// committed session policy.
type networkApprovalPromptGate struct {
	service    *networkApprovalService
	router     *RuntimeRouter
	ctx        context.Context
	key        networkApprovalKey
	pendingKey pendingNetworkApprovalKey
	pending    *pendingNetworkApproval
	threadID   string
	turnID     string
	ownerCall  *activeNetworkApprovalCall
	queuedWait *tooltiming.BlockingGuard
	releaseFn  func()
}

// acquire waits for the endpoint's prompt permit. It reports a decision when
// the execution no longer needs its own prompt: it was cancelled while queued,
// or a session grant/deny rule committed while it waited.
func (g *networkApprovalPromptGate) acquire() (network.ProxyDecision, bool) {
	if g == nil || g.service == nil {
		return network.ProxyDecision{}, false
	}
	release, err := g.service.acquirePromptQueue(g.ctx, g.key, func() {
		g.queuedWait = g.router.startUserWaitTiming(g.threadID, g.turnID,
			g.router.userWaitTimingOwner(g.threadID, g.turnID, networkApprovalTimingCallID(g.ownerCall)))
	})
	if err != nil {
		g.release()
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed), true
	}
	g.releaseFn = release
	s := g.service
	s.mu.Lock()
	if s.pending[g.pendingKey] != g.pending {
		decision := g.pending.decision
		s.mu.Unlock()
		return decision, true
	}
	var decision network.ProxyDecision
	decided := false
	if _, ok := s.denied[g.key]; ok {
		decision = network.DenyProxyDecision(network.ProxyReasonNotAllowed)
		decided = true
	} else if _, ok := s.allowed[g.key]; ok {
		decision = network.AllowProxyDecision()
		decided = true
	}
	s.mu.Unlock()
	if decided && !decision.Allow {
		s.recordUserDenialForThread(g.threadID)
	}
	return decision, decided
}

// release ends the queued-wait timing and returns the endpoint permit after the
// execution committed its decision.
func (g *networkApprovalPromptGate) release() {
	if g == nil {
		return
	}
	g.queuedWait.End()
	if g.releaseFn != nil {
		release := g.releaseFn
		g.releaseFn = nil
		release()
	}
}

func (s *networkApprovalService) requestApproval(ctx context.Context, active *networkApprovalTurn, key networkApprovalKey, request network.ProxyPolicyRequest, ownerCall *activeNetworkApprovalCall, promptGate *networkApprovalPromptGate) (network.ProxyDecision, NetworkPolicyRuleAction) {
	if ownerCall != nil && ownerCall.isCancelled() {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
	}
	protocol := networkApprovalProtocol(request.Protocol)
	target := networkApprovalTarget(request.Protocol, request.Host, request.Port)
	// Rust #57207: a network approval has its own display identity, while the
	// triggering tool call owns the user wait.
	userWaitOwner := s.router.userWaitTimingOwner(active.threadID, active.turnID, networkApprovalTimingCallID(ownerCall))
	approvalID := fmt.Sprintf("network#%s#%s#%s#%d", key.environmentID, networkApprovalProtocolKey(protocol), key.host, key.port)
	// Rust reports every resolved network approval through tool_decision, with no
	// source and the owning tool's identity (network_approval.rs).
	decisionToolName, decisionCallID := networkApprovalDecisionTelemetry(approvalID, ownerCall)
	// Rust Session::request_approval: PermissionRequest hooks decide before the
	// Guardian review and the user approval request. The hook sees the
	// network-access command as a Bash payload.
	hookCommand := "network-access " + target
	var hookToolCWD *string
	if ownerCall != nil && ownerCall.trigger != nil {
		hookToolCWD = localHookToolCWD(key.environmentID, ownerCall.trigger.CWD)
	}
	if verdict, ok := s.router.permissionRequestHookVerdict(ctx, active.threadID, active.turnID, approvalID, "Bash", nil,
		map[string]any{"command": hookCommand, "description": hookCommand}, hookToolCWD, key.environmentID); ok && verdict != nil {
		switch verdict.Kind {
		case HookPermissionRequestAllow:
			s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
				telemetry.ToolDecisionApproved, telemetry.ToolDecisionSourceConfig)
			return network.AllowProxyDecision(), ""
		case HookPermissionRequestDeny:
			reason := ""
			if verdict.Message != nil {
				reason = strings.TrimSpace(*verdict.Message)
			}
			if reason == "" {
				reason = "Network access was denied by a permission-request hook."
			}
			s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
				telemetry.ToolDecisionDenied, telemetry.ToolDecisionSourceConfig)
			s.recordGuardianOutcome(ownerCall, reason)
			return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
		}
	}
	if s.routesApprovalToGuardian(active) {
		return s.requestGuardianApproval(ctx, active, request, protocol, target, ownerCall), ""
	}
	if decision, decided := promptGate.acquire(); decided {
		return decision, ""
	}
	environmentID := key.environmentID
	// Rust #56154: a review-domain host may be allowlisted, so the prompt must
	// not claim the host is missing from the allowlist.
	reason := fmt.Sprintf("Network access to %s requires approval", request.Host)
	command := "network-access " + target
	approvalCWD := firstNonEmpty(turnCWD(active.params), s.router.services.DefaultCWD)
	if ownerCall != nil && ownerCall.trigger != nil {
		// Rust #44575 keeps the originating execution's working directory even
		// when a later turn's cwd has changed.
		approvalCWD = firstNonEmpty(strings.TrimSpace(ownerCall.trigger.CWD), approvalCWD)
	}
	params := &CommandExecutionRequestApprovalParams{
		ThreadID:               active.threadID,
		TurnID:                 active.turnID,
		ItemID:                 approvalID,
		StartedAtMS:            uint64(time.Now().UTC().UnixMilli()),
		EnvironmentID:          &environmentID,
		Reason:                 &reason,
		NetworkApprovalContext: &NetworkApprovalContext{Host: request.Host, Protocol: protocol},
		Command:                &command,
		CWD:                    stringPtrIfNotEmpty(approvalCWD),
		ProposedNetworkPolicyAmendments: []map[string]any{
			{"host": request.Host, "action": string(NetworkPolicyRuleAllow)},
			{"host": request.Host, "action": string(NetworkPolicyRuleDeny)},
		},
	}
	userWait := s.router.startUserWaitTiming(active.threadID, active.turnID, userWaitOwner)
	defer userWait.End()
	var response CommandExecutionRequestApprovalResponse
	err := s.router.requireServerRequests().RequestToConnection(ctx, active.connectionID, ServerRequestCommandExecutionApproval, params, &response)
	if err != nil {
		if ctx.Err() != nil {
			// Execution cancellation interrupted a pending review without a
			// user decision; it is not a new denial (Rust #44575).
			return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
		}
		// Rust: a failed approval request reports denied("network approval failed").
		s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
			telemetry.ToolDecisionDenied, "")
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
	}
	switch approvalDecisionString(response.Decision) {
	case string(CommandExecutionApprovalAccept):
		s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
			networkApprovalUserDecision(response.Decision, false, false), "")
		return network.AllowProxyDecision(), ""
	case string(CommandExecutionApprovalAcceptForSession):
		s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
			networkApprovalUserDecision(response.Decision, false, false), "")
		return network.AllowProxyDecision(), NetworkPolicyRuleAllow
	case string(CommandExecutionApprovalAcceptWithExecpolicyAmendment):
		s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
			networkApprovalUserDecision(response.Decision, false, false), "")
		return network.AllowProxyDecision(), ""
	case string(CommandExecutionApprovalApplyNetworkPolicyAmendment):
		amendment, ok := networkPolicyAmendmentFromDecision(response.Decision)
		if !ok || network.NormalizeProxyHost(amendment.Host) != key.host {
			s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
				networkApprovalUserDecision(response.Decision, false, false), "")
			return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
		}
		if err := s.router.persistNetworkPolicyAmendment(amendment, protocol); err != nil {
			slog.Warn("Failed to apply network policy amendment", "error", err)
			s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
				networkApprovalUserDecision(response.Decision, false, false), "")
			s.recordPolicyDenialForThread(active.threadID, "Network access was blocked by policy because the approved network policy amendment could not be applied.")
			return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
		}
		s.rememberNetworkRuleSaved(active.threadID, active.turnID, amendment)
		s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
			networkApprovalUserDecision(response.Decision, true, amendment.Action == NetworkPolicyRuleAllow), "")
		if amendment.Action == NetworkPolicyRuleAllow {
			return network.AllowProxyDecision(), NetworkPolicyRuleAllow
		}
		s.recordUserDenialForThread(active.threadID)
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed), NetworkPolicyRuleDeny
	default:
		s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
			networkApprovalUserDecision(response.Decision, false, false), "")
		s.recordUserDenialForThread(active.threadID)
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed), ""
	}
}

func (s *networkApprovalService) routesApprovalToGuardian(active *networkApprovalTurn) bool {
	if s == nil || s.router == nil || active == nil {
		return false
	}
	cfg, err := s.router.effectiveConfigForTurn(active.params)
	if err != nil {
		return false
	}
	reviewer := state.ReviewerFromString(turnApprovalsReviewerForTurn(cfg, active.params))
	return reviewer.RoutesToGuardian()
}

func (s *networkApprovalService) requestGuardianApproval(ctx context.Context, active *networkApprovalTurn, request network.ProxyPolicyRequest, protocol NetworkApprovalProtocol, target string, ownerCall *activeNetworkApprovalCall) network.ProxyDecision {
	reviewer := s.router.services.GuardianReviewer
	// The guardian-resolved decision reports the same tool identity as the user
	// approval path (network_approval.rs records it with no source).
	decisionToolName, decisionCallID := networkApprovalDecisionTelemetry(active.turnID+":"+target, ownerCall)
	if reviewer == nil && s.router.services.Agent != nil {
		reviewer = s.router.ensureGuardianReviewer(s.router.services.Agent)
	}
	action := state.Action{
		Type:          "network_access",
		EnvironmentID: strings.TrimSpace(request.EnvironmentID),
		Host:          request.Host,
		Protocol:      string(protocol),
		Port:          request.Port,
		Target:        target,
	}
	if ownerCall != nil && ownerCall.trigger != nil {
		action.CWD = strings.TrimSpace(ownerCall.trigger.CWD)
		action.Extra = map[string]any{"trigger": *ownerCall.trigger}
	}
	if reviewer == nil {
		s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
			telemetry.ToolDecisionDenied, "")
		s.recordGuardianOutcome(ownerCall, "Auto-approval review is unavailable; the request was denied.")
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	// Rust #57207: the review's display identity stays empty, while its
	// critical-path owner is the triggering tool call.
	reviewCtx := withGuardianTimingCallID(ctx, networkApprovalTimingCallID(ownerCall))
	if ownerCall != nil {
		reviewCtx = withGuardianReviewOrigin(reviewCtx, ownerCall.origin)
	}
	if reviewCtx.Err() != nil {
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	decision, reason, err := reviewer.Review(reviewCtx, active.threadID, active.turnID, "", action)
	if err == nil && decision == state.DecisionApproved {
		s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
			telemetry.ToolDecisionApproved, "")
		return network.AllowProxyDecision()
	}
	if reviewCtx.Err() != nil {
		// A ready approval wins the race inside Review; otherwise cancellation
		// resolves the pending review as denied without inventing a rejection.
		return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" && err != nil {
		reason = strings.TrimSpace(err.Error())
	}
	if reason == "" {
		reason = "Auto-approval review denied the request."
	}
	s.router.emitToolDecisionRecords(ctx, active.threadID, decisionToolName, decisionCallID,
		telemetry.ToolDecisionDenied, "")
	s.recordGuardianOutcome(ownerCall, reason)
	return network.DenyProxyDecision(network.ProxyReasonNotAllowed)
}

// networkApprovalTimingCallID reports the tool call whose critical-path entry
// owns a network approval. Only the trigger carries a real invocation ID
// (Rust #57207).
func networkApprovalTimingCallID(ownerCall *activeNetworkApprovalCall) string {
	if ownerCall == nil || ownerCall.trigger == nil {
		return ""
	}
	return strings.TrimSpace(ownerCall.trigger.CallID)
}

func networkApprovalCallKey(threadID string, turnID string, callID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID) + "\x00" + strings.TrimSpace(callID)
}

// networkApprovalDecisionTelemetry reports the tool identity Rust attributes a
// network approval's decision record to: the owning tool call, or the plain
// `network_access` tool with an opaque id when nothing owns the request
// (network_approval.rs's telemetry_tool_name/telemetry_call_id).
func networkApprovalDecisionTelemetry(approvalID string, ownerCall *activeNetworkApprovalCall) (tool.ToolName, string) {
	if ownerCall != nil {
		callID := strings.TrimSpace(ownerCall.callID)
		toolName := ""
		if ownerCall.trigger != nil {
			if callID == "" {
				callID = strings.TrimSpace(ownerCall.trigger.CallID)
			}
			toolName = strings.TrimSpace(ownerCall.trigger.ToolName)
		}
		if callID != "" {
			return tool.PlainName(toolName), callID
		}
	}
	return tool.PlainName("network_access"), strings.TrimSpace(approvalID)
}

// networkApprovalUserDecision mirrors the decision Rust reports after a network
// approval resolves: the opaque review decision of the user's answer, with a
// policy amendment reported as its own decision string.
func networkApprovalUserDecision(decision any, amendmentApplied bool, amendmentAllow bool) string {
	switch approvalDecisionString(decision) {
	case string(CommandExecutionApprovalAccept):
		return telemetry.ToolDecisionApproved
	case string(CommandExecutionApprovalAcceptForSession):
		return telemetry.ToolDecisionApprovedForSession
	case string(CommandExecutionApprovalAcceptWithExecpolicyAmendment):
		return telemetry.ToolDecisionApprovedWithAmendment
	case string(CommandExecutionApprovalApplyNetworkPolicyAmendment):
		if !amendmentApplied {
			// Rust downgrades an amendment that was not applied to the resolved
			// decision, which Go's path resolves as a denial.
			return telemetry.ToolDecisionDenied
		}
		if amendmentAllow {
			return telemetry.ToolDecisionApprovedWithNetworkPolicyAllow
		}
		return telemetry.ToolDecisionDeniedWithNetworkPolicyDeny
	default:
		return telemetry.ToolDecisionDenied
	}
}

func (s *networkApprovalService) registerActiveCall(threadID string, turnID string, invocation *tool.Invocation) {
	s.registerActiveCallWithContext(nil, threadID, turnID, invocation)
}

// registerActiveCallWithContext records the execution that owns a network
// approval. toolCtx is the invocation context: once the tool call ends it
// cancels the execution unless the process has been marked as outliving it.
func (s *networkApprovalService) registerActiveCallWithContext(ctx context.Context, threadID string, turnID string, invocation *tool.Invocation) {
	if s == nil || invocation == nil || !isNetworkApprovalCommandInvocation(invocation) {
		return
	}
	callID := firstNonEmpty(strings.TrimSpace(invocation.CallID), invocation.ToolName.Key())
	key := networkApprovalCallKey(threadID, turnID, callID)
	environmentID, trigger := s.guardianTriggerForInvocation(threadID, invocation)
	executionCtx, executionCancel := context.WithCancelCause(context.Background())
	call := &activeNetworkApprovalCall{
		threadID:        threadID,
		turnID:          turnID,
		callID:          callID,
		environmentID:   environmentID,
		trigger:         trigger,
		cancel:          invocation.Cancel,
		executionCtx:    executionCtx,
		executionCancel: executionCancel,
		origin:          s.guardianReviewOriginForInvocation(threadID, turnID, environmentID, trigger),
	}
	if ctx != nil {
		context.AfterFunc(ctx, func() {
			s.cancelExecutionIfNotPersistent(call, context.Cause(ctx))
		})
	}
	s.callsMu.Lock()
	s.calls[key] = call
	s.callsMu.Unlock()
}

func (s *networkApprovalService) guardianReviewOriginForInvocation(threadID string, turnID string, environmentID string, trigger *guardianNetworkAccessTrigger) *guardianReviewOrigin {
	if s == nil || s.router == nil {
		return nil
	}
	origin := &guardianReviewOrigin{EnvironmentID: strings.TrimSpace(environmentID)}
	if params := s.router.activeTurnParams(threadID); params != nil {
		origin.Environments = cloneMapSlice(params.Environments)
	}
	if trigger != nil {
		origin.CWD = strings.TrimSpace(trigger.CWD)
	}
	if profile, cwd, resolvedID := s.router.guardianTurnPermissionProfileForTurn(threadID, turnID, environmentID); profile != nil {
		origin.PermissionProfile = profile
		if origin.CWD == "" && strings.TrimSpace(cwd) != "" {
			origin.CWD = strings.TrimSpace(cwd)
		}
		if resolvedID != nil && strings.TrimSpace(*resolvedID) != "" {
			origin.EnvironmentID = strings.TrimSpace(*resolvedID)
		}
	}
	return origin
}

func networkApprovalExecutionKey(threadID string, callID string) string {
	return networkApprovalCallKey(threadID, "", callID)
}

// markExecutionPersistent detaches an execution from its launching tool call.
// Unified exec processes can outlive the call and turn that started them, so
// their environment snapshot and cancellation stay registered until the
// process reports that it exited (Rust #44575).
func (s *networkApprovalService) markExecutionPersistent(threadID string, turnID string, callID string) {
	if s == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	key := networkApprovalCallKey(threadID, turnID, callID)
	s.callsMu.Lock()
	call := s.calls[key]
	if call == nil {
		s.callsMu.Unlock()
		return
	}
	call.persistent = true
	// Detach unconditionally: a tool-context cancellation racing with this
	// transition must not cancel the process-owned token.
	call.executionCtx, call.executionCancel = context.WithCancelCause(context.Background())
	executionKey := networkApprovalExecutionKey(threadID, callID)
	s.executions[executionKey] = call
	s.callsMu.Unlock()
	s.mu.Lock()
	for pendingKey, pending := range s.pending {
		if pending != nil && pendingKey.callID == callID && pendingKey.threadID == strings.TrimSpace(threadID) {
			pending.persistent = true
		}
	}
	s.mu.Unlock()
}

// finishExecution retires an execution that outlived its tool call. It cancels
// the execution token so an in-flight review resolves as denied, and drops the
// snapshot so a late proxy callback cannot reuse it.
func (s *networkApprovalService) finishExecution(threadID string, callID string, reason string) {
	if s == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	key := networkApprovalExecutionKey(threadID, callID)
	s.callsMu.Lock()
	call := s.executions[key]
	delete(s.executions, key)
	s.callsMu.Unlock()
	if call == nil || call.executionCancel == nil {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "execution finished"
	}
	call.executionCancel(errors.New(reason))
}

func (s *networkApprovalService) cancelExecutionIfNotPersistent(call *activeNetworkApprovalCall, cause error) {
	if s == nil || call == nil {
		return
	}
	s.callsMu.Lock()
	persistent := call.persistent
	cancel := call.executionCancel
	s.callsMu.Unlock()
	if persistent {
		return
	}
	if cancel != nil {
		if cause == nil {
			cause = errors.New("execution finished")
		}
		cancel(cause)
	}
}

func (s *networkApprovalService) guardianTriggerForInvocation(threadID string, invocation *tool.Invocation) (string, *guardianNetworkAccessTrigger) {
	environmentID := "local"
	if s == nil || s.router == nil || invocation == nil {
		return environmentID, nil
	}
	var args tool.ExecCommandArgs
	if err := invocation.DecodeArguments(&args); err != nil || strings.TrimSpace(args.Cmd) == "" {
		return environmentID, nil
	}
	params := s.router.activeTurnParams(threadID)
	cwd := firstNonEmpty(strings.TrimSpace(turnCWD(params)), s.router.services.DefaultCWD)
	shell := tool.NewDefaultShell()
	environments := s.router.unifiedExecEnvironmentsForTurn(params)
	requestedEnvironmentID := strings.TrimSpace(args.EnvironmentID)
	if requestedEnvironmentID == "" && len(environments) > 0 {
		requestedEnvironmentID = strings.TrimSpace(environments[0].ID)
	}
	if requestedEnvironmentID != "" {
		environmentID = requestedEnvironmentID
	}
	for i := range environments {
		if strings.TrimSpace(environments[i].ID) != environmentID {
			continue
		}
		cwd = firstNonEmpty(strings.TrimSpace(environments[i].CWD), cwd)
		if environments[i].Shell != nil {
			shell = environments[i].Shell
		}
		break
	}
	if override := firstNonEmpty(strings.TrimSpace(args.CWD), strings.TrimSpace(args.Workdir)); override != "" {
		cwd = override
	}
	resolved, err := tool.ResolveCommand(&args, shell, false)
	if err != nil {
		return environmentID, nil
	}
	permissions := args.SandboxPermissions
	if permissions == "" {
		permissions = sandbox.SandboxPermissionsUseDefault
	}
	return environmentID, &guardianNetworkAccessTrigger{
		CallID:                firstNonEmpty(strings.TrimSpace(invocation.CallID), invocation.ToolName.Key()),
		Command:               append([]string(nil), resolved.Command...),
		CWD:                   cwd,
		SandboxPermissions:    string(permissions),
		AdditionalPermissions: cloneAdditionalPermissionProfile(args.AdditionalPermissions),
		Justification:         stringPtrIfNotEmpty(strings.TrimSpace(args.Justification)),
		ToolName:              invocation.ToolName.Key(),
		TTY:                   args.TTY,
	}
}

func (s *networkApprovalService) activeCallForApproval(active *networkApprovalTurn, environmentID string, executionID string) *activeNetworkApprovalCall {
	if s == nil || active == nil {
		return nil
	}
	environmentID = firstNonEmpty(strings.TrimSpace(environmentID), "local")
	executionID = strings.TrimSpace(executionID)
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	if executionID != "" {
		// The execution ID names a process, not the turn that is active now: a
		// background process reports under a later turn but keeps the snapshot
		// captured when it started (Rust #44575).
		for _, call := range s.calls {
			if call == nil || call.threadID != active.threadID || call.callID != executionID {
				continue
			}
			if firstNonEmpty(strings.TrimSpace(call.environmentID), "local") == environmentID {
				return call
			}
		}
		if call := s.executions[networkApprovalExecutionKey(active.threadID, executionID)]; call != nil {
			if firstNonEmpty(strings.TrimSpace(call.environmentID), "local") == environmentID {
				return call
			}
		}
		return nil
	}
	var selected *activeNetworkApprovalCall
	matches := 0
	consider := func(call *activeNetworkApprovalCall) {
		if call == nil || call.threadID != active.threadID || firstNonEmpty(strings.TrimSpace(call.environmentID), "local") != environmentID {
			return
		}
		matches++
		if matches == 1 {
			selected = call
		}
	}
	for _, call := range s.calls {
		if call == nil || call.turnID != active.turnID {
			continue
		}
		consider(call)
	}
	if matches == 0 {
		for _, call := range s.executions {
			if call == nil || call.turnID != active.turnID {
				continue
			}
			consider(call)
		}
	}
	if matches > 1 {
		return nil
	}
	return selected
}

func (s *networkApprovalService) recordGuardianOutcome(call *activeNetworkApprovalCall, message string) {
	if s == nil || call == nil || strings.TrimSpace(message) == "" {
		return
	}
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	key := networkApprovalCallKey(call.threadID, call.turnID, call.callID)
	// Rust #38256: an explicit network-review outcome replaces the previously
	// recorded outcome for the execution instead of keeping the first one.
	if active := s.calls[key]; active == call {
		active.outcome = message
		return
	}
	if active := s.executions[networkApprovalExecutionKey(call.threadID, call.callID)]; active == call {
		active.outcome = message
	}
}

func (s *networkApprovalService) finishActiveCall(threadID string, turnID string, invocation *tool.Invocation) string {
	if s == nil || invocation == nil || !isNetworkApprovalCommandInvocation(invocation) {
		return ""
	}
	callID := firstNonEmpty(strings.TrimSpace(invocation.CallID), invocation.ToolName.Key())
	key := networkApprovalCallKey(threadID, turnID, callID)
	s.callsMu.Lock()
	call := s.calls[key]
	delete(s.calls, key)
	cancelExecution := call != nil && !call.persistent
	if call != nil && !call.persistent {
		delete(s.executions, networkApprovalExecutionKey(threadID, callID))
	}
	s.callsMu.Unlock()
	if cancelExecution && call.executionCancel != nil {
		call.executionCancel(errors.New("execution finished"))
	}
	if call == nil {
		return ""
	}
	return call.outcome
}

func (s *networkApprovalService) clearActiveCallsForTurn(threadID string, turnID string) {
	if s == nil {
		return
	}
	var cancels []context.CancelCauseFunc
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	for key, call := range s.calls {
		if call.threadID == threadID && (turnID == "" || call.turnID == turnID) {
			delete(s.calls, key)
			// Persistent executions belong to their process, not the turn;
			// they stay registered until the process exits (Rust #44575).
			if !call.persistent && call.executionCancel != nil {
				cancels = append(cancels, call.executionCancel)
			}
		}
	}
	for _, cancel := range cancels {
		cancel(errors.New("turn finished"))
	}
}

func (s *networkApprovalService) clearExecutionsForThread(threadID string) {
	if s == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	var cancels []context.CancelCauseFunc
	s.callsMu.Lock()
	for key, call := range s.executions {
		if call == nil || call.threadID != threadID {
			continue
		}
		delete(s.executions, key)
		if call.executionCancel != nil {
			cancels = append(cancels, call.executionCancel)
		}
	}
	s.callsMu.Unlock()
	for _, cancel := range cancels {
		cancel(errors.New("thread closed"))
	}
}

func (s *networkApprovalService) cancelPendingForTurn(threadID string, turnID string) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if s == nil || threadID == "" {
		return
	}
	s.cancelPending(func(key networkApprovalKey, pending *pendingNetworkApproval) bool {
		if pending.persistent && turnID != "" {
			return false
		}
		return key.threadID == threadID && (turnID == "" || pending.turnID == turnID)
	})
}

func (s *networkApprovalService) cancelPendingForConnection(connectionID string) {
	connectionID = normalizeConnectionID(connectionID)
	if s == nil || connectionID == "" {
		return
	}
	now := time.Now()
	var elapsed time.Duration
	s.mu.Lock()
	for _, pending := range s.pending {
		if pending != nil && normalizeConnectionID(pending.connectionID) == connectionID && !pending.startedAt.IsZero() {
			if current := now.Sub(pending.startedAt); current > elapsed {
				elapsed = current
			}
		}
	}
	s.mu.Unlock()
	// Rust #39284: when a proxy request disconnects while awaiting a network
	// approval decision, the owning tool call gets a model-visible explanation
	// that includes how long the request waited.
	outcome := "Network request disconnected before approval could complete"
	if elapsed > 0 {
		outcome = fmt.Sprintf("Network request disconnected after %d ms, before approval could complete", elapsed.Milliseconds())
	}
	s.recordDisconnectOutcomeForConnection(connectionID, outcome)
	s.cancelPending(func(_ networkApprovalKey, pending *pendingNetworkApproval) bool {
		return normalizeConnectionID(pending.connectionID) == connectionID
	})
}

func (s *networkApprovalService) recordDisconnectOutcomeForConnection(connectionID string, message string) {
	if s == nil {
		return
	}
	connectionID = normalizeConnectionID(connectionID)
	turns := map[string]string{}
	s.mu.Lock()
	for _, pending := range s.pending {
		if pending != nil && normalizeConnectionID(pending.connectionID) == connectionID && pending.turnID != "" {
			turns[pending.turnID] = ""
		}
	}
	s.mu.Unlock()
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	seen := map[*activeNetworkApprovalCall]struct{}{}
	for _, call := range s.calls {
		if call == nil {
			continue
		}
		seen[call] = struct{}{}
		if call.outcome != "" {
			continue
		}
		if _, ok := turns[call.turnID]; ok {
			call.outcome = message
		}
	}
	for _, call := range s.executions {
		if call == nil {
			continue
		}
		if _, ok := seen[call]; ok {
			continue
		}
		if _, ok := turns[call.turnID]; ok && call.outcome == "" {
			call.outcome = message
		}
	}
}

func (s *networkApprovalService) cancelPending(matches func(networkApprovalKey, *pendingNetworkApproval) bool) {
	if s == nil || matches == nil {
		return
	}
	denied := network.DenyProxyDecision(network.ProxyReasonNotAllowed)
	var cancels []context.CancelFunc
	s.mu.Lock()
	for pendingKey, pending := range s.pending {
		if pending == nil || !matches(pendingKey.networkApprovalKey, pending) {
			continue
		}
		delete(s.pending, pendingKey)
		pending.decision = denied
		close(pending.done)
		if pending.cancel != nil {
			cancels = append(cancels, pending.cancel)
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *networkApprovalService) clearThread(threadID string) {
	if s == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	s.cancelPendingForTurn(threadID, "")
	s.mu.Lock()
	for key := range s.allowed {
		if key.threadID == threadID {
			delete(s.allowed, key)
		}
	}
	for key := range s.denied {
		if key.threadID == threadID {
			delete(s.denied, key)
		}
	}
	for key := range s.saved {
		if strings.HasPrefix(key, threadID+"\x00") {
			delete(s.saved, key)
		}
	}
	s.mu.Unlock()
	s.clearActiveCallsForTurn(threadID, "")
	s.clearExecutionsForThread(threadID)
}

func (s *networkApprovalService) recordUserDenialForThread(threadID string) {
	if s == nil {
		return
	}
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	selected := s.singleActiveCallLocked(threadID)
	if selected != nil {
		selected.outcome = "rejected by user"
	}
}

func (s *networkApprovalService) recordPolicyDenialForThread(threadID string, message string) {
	if s == nil || strings.TrimSpace(message) == "" {
		return
	}
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	selected := s.singleActiveCallLocked(threadID)
	if selected != nil {
		selected.outcome = message
	}
}

// singleActiveCallLocked resolves the one execution a thread-scoped blocked
// request or denial belongs to. Background executions stay in the registry
// after their tool call completes, and the same call object may be present in
// both maps, so pointers are counted once. Ambiguity resolves to no owner.
func (s *networkApprovalService) singleActiveCallLocked(threadID string) *activeNetworkApprovalCall {
	threadID = strings.TrimSpace(threadID)
	seen := map[*activeNetworkApprovalCall]struct{}{}
	var selected *activeNetworkApprovalCall
	matches := 0
	consider := func(call *activeNetworkApprovalCall) {
		if call == nil || (threadID != "" && call.threadID != threadID) {
			return
		}
		if _, ok := seen[call]; ok {
			return
		}
		seen[call] = struct{}{}
		matches++
		if matches == 1 {
			selected = call
		}
	}
	for _, call := range s.calls {
		consider(call)
	}
	for _, call := range s.executions {
		consider(call)
	}
	if matches != 1 {
		return nil
	}
	return selected
}

func (s *networkApprovalService) OnBlockedRequest(_ context.Context, blocked network.ProxyBlockedRequest) {
	s.onBlockedRequestForThread("", blocked)
}

func (s *networkApprovalService) onBlockedRequestForThread(threadID string, blocked network.ProxyBlockedRequest) {
	if s == nil || blocked.Decision != string(network.ProxyPolicyDecisionDeny) {
		return
	}
	message := deniedNetworkPolicyMessage(blocked)
	if message == "" {
		return
	}
	s.callsMu.Lock()
	call := s.singleActiveCallLocked(threadID)
	if call == nil {
		s.callsMu.Unlock()
		return
	}
	if call.outcome == "" {
		call.outcome = message
	}
	outcome := call.outcome
	cancel := call.cancel
	s.callsMu.Unlock()
	if cancel != nil {
		cancel(tool.RespondToModel(outcome))
	}
}

func deniedNetworkPolicyMessage(blocked network.ProxyBlockedRequest) string {
	host := strings.TrimSpace(blocked.Host)
	if host == "" {
		return "Network access was blocked by policy."
	}
	detail := "request is blocked by network policy"
	switch blocked.Reason {
	case network.ProxyReasonDenied:
		detail = "domain is explicitly denied by policy and cannot be approved from this prompt"
	case network.ProxyReasonNotAllowed:
		detail = "domain is not on the allowlist for the current sandbox mode"
	case network.ProxyReasonNotAllowedLocal:
		detail = "local/private network addresses are blocked by the sandbox policy"
	case network.ProxyReasonMethodNotAllowed:
		detail = "request method is blocked by the current network mode"
	case network.ProxyReasonProxyDisabled:
		detail = "network proxy is disabled"
	}
	return fmt.Sprintf("Network access to %q was blocked: %s.", host, detail)
}

func isNetworkApprovalCommandInvocation(invocation *tool.Invocation) bool {
	if invocation == nil {
		return false
	}
	switch invocation.ToolName.Key() {
	case "shell_command", "exec_command", "write_stdin":
		return true
	default:
		return false
	}
}

func networkApprovalTurnKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func (s *networkApprovalService) rememberNetworkRuleSaved(threadID string, turnID string, amendment *NetworkPolicyAmendment) {
	if s == nil || amendment == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := networkApprovalTurnKey(threadID, turnID)
	s.saved[key] = append(s.saved[key], networkRuleSavedFragment{host: amendment.Host, action: amendment.Action})
}

func (s *networkApprovalService) takeNetworkRulesSaved(threadID string, turnID string) []networkRuleSavedFragment {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := networkApprovalTurnKey(threadID, turnID)
	fragments := append([]networkRuleSavedFragment(nil), s.saved[key]...)
	delete(s.saved, key)
	return fragments
}

func (s *networkApprovalService) syncApprovedHostsForFork(sourceThreadID string, targetThreadID string) {
	if s == nil || strings.TrimSpace(sourceThreadID) == "" || strings.TrimSpace(targetThreadID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.allowed {
		if key.threadID != sourceThreadID {
			continue
		}
		clone := key
		clone.threadID = targetThreadID
		s.allowed[clone] = struct{}{}
	}
}

func networkRuleSavedText(fragment networkRuleSavedFragment) string {
	action := "Allowed"
	listName := "allowlist"
	if fragment.action == NetworkPolicyRuleDeny {
		action = "Denied"
		listName = "denylist"
	}
	return fmt.Sprintf("%s network rule saved in execpolicy (%s): %s", action, listName, fragment.host)
}

func (r *RuntimeRouter) networkApprovalPostToolInputItems(threadID string, turnID string, base turn.ToolPostExecutionInputItems, appendSessionItems func([]session.Item)) turn.ToolPostExecutionInputItems {
	if r == nil || r.networkApproval == nil {
		return base
	}
	return func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) []any {
		if outcome := r.networkApproval.finishActiveCall(threadID, turnID, invocation); outcome != "" && output != nil {
			output.Success = false
			output.Body = outcome
			output.Error = outcome
		}
		items := []any{}
		if base != nil {
			items = append(items, base(ctx, invocation, output)...)
		}
		fragments := r.networkApproval.takeNetworkRulesSaved(threadID, turnID)
		if len(fragments) == 0 {
			return items
		}
		createdAt := time.Now().UTC()
		if output != nil && !output.CompletedAt.IsZero() {
			createdAt = output.CompletedAt.UTC()
		}
		sessionItems := make([]session.Item, 0, len(fragments))
		for index, fragment := range fragments {
			text := networkRuleSavedText(fragment)
			items = append(items, modelInputTextMessage("developer", text))
			sessionItems = append(sessionItems, session.Item{
				ID:        fmt.Sprintf("network-rule-saved-%s-%d", safeIdentifier(turnID), index+1),
				Type:      "message",
				Role:      "developer",
				Text:      text,
				CreatedAt: createdAt,
				Metadata:  appTurnMetadata(turnID, map[string]any{"kind": "network_rule_saved"}),
			})
		}
		if appendSessionItems != nil {
			appendSessionItems(sessionItems)
		}
		return items
	}
}

func (r *RuntimeRouter) activeTurnForNetworkApproval(environmentID string) *networkApprovalTurn {
	return r.activeTurnForNetworkApprovalThread("", environmentID)
}

func (r *RuntimeRouter) activeTurnForNetworkApprovalThread(threadID string, environmentID string) *networkApprovalTurn {
	if r == nil {
		return nil
	}
	environmentID = strings.TrimSpace(environmentID)
	var selected *activeRuntimeTurn
	for _, active := range r.threads.ActiveTurns() {
		if active == nil || strings.TrimSpace(active.TurnID) == "" {
			continue
		}
		if threadID != "" && active.ThreadID != threadID {
			continue
		}
		if environmentID != "" && environmentID != "local" && !turnSelectsEnvironment(active.Params, environmentID) {
			continue
		}
		if selected != nil {
			return nil
		}
		selected = active
	}
	if selected == nil {
		return nil
	}
	return &networkApprovalTurn{
		threadID:     selected.ThreadID,
		turnID:       selected.TurnID,
		connectionID: selected.ConnectionID,
		params:       cloneTurnStartParams(selected.Params),
		runConfig:    selected.RunConfig,
	}
}

func turnSelectsEnvironment(params *turn.TurnStartParams, environmentID string) bool {
	if params == nil {
		return false
	}
	for _, selected := range params.Environments {
		if strings.TrimSpace(firstNonEmpty(networkSelectionString(selected, "environmentId"), networkSelectionString(selected, "environment_id"), networkSelectionString(selected, "id"))) == environmentID {
			return true
		}
	}
	return false
}

func networkSelectionString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func (r *RuntimeRouter) networkApprovalAllowedForTurn(active *networkApprovalTurn) bool {
	if r == nil || active == nil {
		return false
	}
	cfg, err := r.effectiveConfigForTurn(active.params)
	if err != nil {
		return false
	}
	if turnApprovalPolicyForTurn(cfg, active.params) == sandbox.ApprovalNever {
		return false
	}
	profile, err := turnSandboxPermissionProfile(cfg, firstNonEmpty(turnCWD(active.params), r.services.DefaultCWD), active.params)
	return err == nil && profile != nil && profile.Profile != nil && !profile.Profile.Disabled
}

func networkApprovalProtocol(protocol network.ProxyProtocol) NetworkApprovalProtocol {
	switch protocol {
	case network.ProxyProtocolHTTPSConnect:
		return NetworkApprovalHTTPS
	case network.ProxyProtocolSocks5TCP:
		return NetworkApprovalSocks5TCP
	case network.ProxyProtocolSocks5UDP:
		return NetworkApprovalSocks5UDP
	default:
		return NetworkApprovalHTTP
	}
}

func networkApprovalProtocolKey(protocol NetworkApprovalProtocol) string {
	switch protocol {
	case NetworkApprovalHTTPS:
		return "https"
	case NetworkApprovalSocks5TCP:
		return "socks5-tcp"
	case NetworkApprovalSocks5UDP:
		return "socks5-udp"
	default:
		return "http"
	}
}

func networkApprovalTarget(protocol network.ProxyProtocol, host string, port uint16) string {
	return fmt.Sprintf("%s://%s:%d", networkApprovalProtocolKey(networkApprovalProtocol(protocol)), host, port)
}

func networkPolicyAmendmentFromDecision(value any) (*NetworkPolicyAmendment, bool) {
	object, ok := commandExecutionApprovalDecisionObject(value)
	if !ok {
		return nil, false
	}
	payload, ok := commandExecutionApprovalDecisionObject(object[string(CommandExecutionApprovalApplyNetworkPolicyAmendment)])
	if !ok {
		return nil, false
	}
	amendment, ok := commandExecutionApprovalDecisionObject(firstNonNil(payload["network_policy_amendment"], payload["networkPolicyAmendment"]))
	if !ok {
		return nil, false
	}
	host, _ := amendment["host"].(string)
	action, _ := amendment["action"].(string)
	parsed := &NetworkPolicyAmendment{Host: host, Action: NetworkPolicyRuleAction(action)}
	if network.NormalizeProxyHost(parsed.Host) == "" || (parsed.Action != NetworkPolicyRuleAllow && parsed.Action != NetworkPolicyRuleDeny) {
		return nil, false
	}
	return parsed, true
}

func (r *RuntimeRouter) persistNetworkPolicyAmendment(amendment *NetworkPolicyAmendment, protocol NetworkApprovalProtocol) error {
	if r == nil || amendment == nil || r.services.Config == nil {
		return fmt.Errorf("managed network config is unavailable")
	}
	host := network.NormalizeProxyHost(amendment.Host)
	if host == "" {
		return fmt.Errorf("network policy amendment host is empty")
	}
	decision := execpolicy.DecisionAllow
	verb := "Allow"
	if amendment.Action == NetworkPolicyRuleDeny {
		decision = execpolicy.DecisionForbidden
		verb = "Deny"
	}
	policyProtocol := string(protocol)
	justificationProtocol := policyProtocol
	if protocol == NetworkApprovalHTTPS {
		policyProtocol = "https"
		justificationProtocol = "https_connect"
	}
	return execpolicy.AppendNetworkRule(
		execpolicy.DefaultPolicyPath(r.services.Config.CodexHome()),
		host,
		policyProtocol,
		decision,
		fmt.Sprintf("%s %s access to %s", verb, justificationProtocol, host),
	)
}

func applyExecPolicyNetworkRules(proxyConfig *network.ProxyConfig, codexHome string) error {
	if proxyConfig == nil {
		return nil
	}
	rules, err := loadExecPolicyNetworkRules(codexHome)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		proxyConfig.Network.UpsertDomainPermission(rule.Host, rule.Permission, network.NormalizeProxyHost)
	}
	return nil
}

func loadExecPolicyNetworkRules(codexHome string) ([]network.ProxyNetworkRule, error) {
	if strings.TrimSpace(codexHome) == "" {
		return nil, nil
	}
	path := execpolicy.DefaultPolicyPath(codexHome)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	policy, err := execpolicy.LoadPolicies([]string{path})
	if err != nil {
		return nil, err
	}
	rules := make([]network.ProxyNetworkRule, 0, len(policy.NetworkRules))
	for _, rule := range policy.NetworkRules {
		switch rule.Decision {
		case execpolicy.DecisionAllow:
			rules = append(rules, network.ProxyNetworkRule{Host: rule.Host, Permission: network.ProxyDomainAllow})
		case execpolicy.DecisionForbidden:
			rules = append(rules, network.ProxyNetworkRule{Host: rule.Host, Permission: network.ProxyDomainDeny})
		}
	}
	return rules, nil
}

func (r *RuntimeRouter) networkProxyAuditMetadata(request network.ProxyPolicyRequest) network.ProxyAuditMetadata {
	return r.networkProxyAuditMetadataForThread("", request)
}

func (r *RuntimeRouter) networkProxyAuditMetadataForThread(threadID string, request network.ProxyPolicyRequest) network.ProxyAuditMetadata {
	metadata := network.ProxyAuditMetadata{AppVersion: appServerBuildVersion()}
	active := r.activeTurnForNetworkApprovalThread(strings.TrimSpace(threadID), request.EnvironmentID)
	if active != nil {
		metadata.ConversationID = active.threadID
		if active.runConfig != nil {
			metadata.Model = active.runConfig.Model
			metadata.Slug = active.runConfig.Model
			metadata.Originator = active.runConfig.Originator
		}
		if info := r.clientInfoForConnection(active.connectionID); info.Name != "" {
			metadata.TerminalType = info.Name
		}
	}
	if r != nil && r.services.Account != nil {
		snapshot := r.services.Account.AuthSnapshot()
		if snapshot != nil {
			metadata.AuthMode = snapshot.Mode()
			metadata.UserAccountID = auth.AccountIDFromAuthForRestrictions(snapshot)
			if account := auth.AccountFromAuth(snapshot); account != nil && account.Email != nil {
				metadata.UserEmail = *account.Email
			}
		}
	}
	return metadata
}

func (r *RuntimeRouter) clientInfoForConnection(connectionID string) ClientInfo {
	if r == nil {
		return ClientInfo{}
	}
	r.clientInfoMu.RLock()
	defer r.clientInfoMu.RUnlock()
	return r.clientInfo[normalizeConnectionID(connectionID)]
}

func appServerBuildVersion() string {
	return appServerVersion()
}

func emitNetworkProxyAuditEvent(event network.ProxyPolicyAuditEvent) {
	scope := "domain"
	if event.Reason == network.ProxyReasonMethodNotAllowed || event.Reason == network.ProxyReasonProxyDisabled || event.Reason == network.ProxyReasonUnixSocketUnsupported {
		scope = "non_domain"
	}
	slog.Info("codex.network_proxy.policy_decision",
		"event.name", "codex.network_proxy.policy_decision",
		"event.timestamp", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"conversation.id", event.Metadata.ConversationID,
		"app.version", event.Metadata.AppVersion,
		"auth_mode", event.Metadata.AuthMode,
		"originator", event.Metadata.Originator,
		"user.account_id", event.Metadata.UserAccountID,
		"user.email", event.Metadata.UserEmail,
		"terminal.type", event.Metadata.TerminalType,
		"model", event.Metadata.Model,
		"slug", event.Metadata.Slug,
		"network.policy.scope", scope,
		"network.policy.decision", event.Decision,
		"network.policy.source", event.Source,
		"network.policy.reason", event.Reason,
		"network.transport.protocol", event.Request.Protocol,
		"server.address", event.Request.Host,
		"server.port", event.Request.Port,
		"http.request.method", firstNonEmpty(event.Request.Method, "none"),
		"client.address", firstNonEmpty(event.Request.ClientAddr, "unknown"),
		"network.policy.override", event.PolicyOverride,
	)
}
