package appserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/features"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
)

// appserverMCPToolApprovalHandler serves Rust's custom-MCP-server tool approval
// policy (core/src/mcp_tool_call.rs maybe_request_mcp_tool_approval): it applies
// the session/persistent remembered choices, asks the client through the
// `tool/requestUserInput` surface, and records the answer.
type appserverMCPToolApprovalHandler struct {
	router  *RuntimeRouter
	service *mcp.MCPService
	// responder receives the calling MCP tool's ID so the approval wait is
	// charged to that invocation (Rust #57207).
	responder      tool.UserInputCallIDResponder
	threadID       string
	turnID         string
	approvalPolicy sandbox.AskForApproval
	// persistentApprovalAllowed mirrors Rust's ToolCallMcpElicitation feature
	// gate: only when it is on may the prompt offer "allow and don't ask me
	// again".
	persistentApprovalAllowed bool
	// elicitationEnabled mirrors Rust's tool_call_mcp_elicitation feature: when
	// on, the approval is surfaced as an MCP elicitation form (with the same
	// enablement as the persistent-approval option, since Rust gates both on the
	// same feature).
	elicitationEnabled bool
	// nonRootAgent mirrors Rust #46877's remaining root-only path: the legacy
	// request_user_input approval prompt stays restricted to the root thread,
	// while MCP elicitation prompts may be surfaced by subagents.
	nonRootAgent bool
}

// appsRequirementsForConfig returns the managed app requirements, if any.
func appsRequirementsForConfig(cfg *config.Config) apps.AppsRequirements {
	if cfg == nil || cfg.Requirements == nil {
		return nil
	}
	return cfg.Requirements.Apps
}

var _ mcp.MCPToolApprovalHandler = (*appserverMCPToolApprovalHandler)(nil)

// newAppserverMCPToolApprovalOptions builds the executor options for a turn, or
// nil when the runtime cannot surface a prompt (no client request sink).
func (r *RuntimeRouter) newAppserverMCPToolApprovalOptions(
	cfg *config.Config,
	service *mcp.MCPService,
	threadID string,
	turnID string,
	approvalPolicy sandbox.AskForApproval,
) *mcp.ToolApprovalOptions {
	if r == nil || service == nil || !r.serverRequestSinkConfigured() {
		return nil
	}
	responder := r.userInputCallIDResponderForTurn(threadID, strings.TrimSpace(turnID))
	if responder == nil {
		return nil
	}
	persistent := false
	cfgValues := map[string]any(nil)
	if cfg != nil {
		persistent = features.Enabled(cfg.FeatureSettings(), "tool_call_mcp_elicitation")
		cfgValues = cfg.Values
	}
	return &mcp.ToolApprovalOptions{
		ApprovalPolicy: approvalPolicy,
		// Rust mcp_tool_call.rs builds the app policy from the same config layer
		// stack the turn uses.
		AppPolicy: apps.NewAppToolPolicyEvaluatorWithRequirements(
			apps.AppsConfigFromValues(cfgValues),
			appsRequirementsForConfig(cfg),
		),
		PermissionProfileForServer: func(server string) *sandbox.PermissionProfile {
			profile, ok := service.PermissionProfileForServer(server)
			if !ok {
				return nil
			}
			return profile
		},
		Handler: &appserverMCPToolApprovalHandler{
			router:                    r,
			service:                   service,
			responder:                 responder,
			threadID:                  strings.TrimSpace(threadID),
			turnID:                    strings.TrimSpace(turnID),
			approvalPolicy:            approvalPolicy,
			persistentApprovalAllowed: persistent,
			elicitationEnabled:        persistent,
			nonRootAgent:              r.turnThreadIsSubagent(threadID),
		},
	}
}

// ApproveMCPToolCall returns the decision for a custom MCP tool call that the
// executor determined requires approval.
func (h *appserverMCPToolApprovalHandler) ApproveMCPToolCall(ctx context.Context, request *mcp.MCPToolApprovalRequest) (mcp.MCPToolApprovalOutcome, error) {
	outcome, err := h.approveMCPToolCall(ctx, request)
	// Rust #45716: a denied, aborted or timed-out approval is classified for
	// analytics; an approval is not, so the call's classification stays null.
	if h.router != nil && request != nil && (err != nil || mcpToolApprovalIsDenied(outcome.Decision)) {
		h.router.rememberMCPToolCallElicitation(h.threadID, h.turnID, strings.TrimSpace(request.CallID), telemetry.ElicitationTypeApproval)
	}
	return outcome, err
}

// mcpToolApprovalIsDenied reports whether an approval outcome denies or aborts
// the call (Rust ReviewDecision::Denied/Abort), which is the case that carries
// the approval classification.
func mcpToolApprovalIsDenied(decision mcp.MCPToolApprovalDecision) bool {
	return decision == mcp.MCPToolApprovalDeny || decision == mcp.MCPToolApprovalReject
}

func (h *appserverMCPToolApprovalHandler) approveMCPToolCall(ctx context.Context, request *mcp.MCPToolApprovalRequest) (mcp.MCPToolApprovalOutcome, error) {
	if h == nil || request == nil {
		return mcp.MCPToolApprovalOutcome{Decision: mcp.MCPToolApprovalDeny}, nil
	}
	if request.SessionKey != nil && h.router.mcpToolApprovalRemembered(h.threadID, *request.SessionKey) {
		return mcp.MCPToolApprovalOutcome{Decision: mcp.MCPToolApprovalApprove}, nil
	}
	// Rust Session::request_approval: PermissionRequest hooks decide first, then
	// an auto-review turn routes the call through the Guardian review, and only
	// then the user is asked.
	if decision, hookErr, handled := h.runPermissionRequestHooks(ctx, request); handled {
		return mcp.MCPToolApprovalOutcome{Decision: decision}, hookErr
	}
	if h.router != nil {
		if reviewer := h.mcpToolApprovalReviewer(request); reviewer.RoutesToGuardian() {
			decision, err := h.reviewViaGuardian(ctx, request)
			return mcp.MCPToolApprovalOutcome{Decision: decision}, err
		}
	}
	if h.responder == nil {
		return mcp.MCPToolApprovalOutcome{Decision: mcp.MCPToolApprovalDeny}, nil
	}
	promptOptions := mcp.MCPToolApprovalPromptOptionsFor(
		request.AllowSessionRemember,
		request.AllowPersistentApproval,
		h.persistentApprovalAllowed,
	)
	questionID := mcp.MCPToolApprovalQuestionIDPrefix
	if callID := strings.TrimSpace(request.CallID); callID != "" {
		questionID += "_" + callID
	}
	question := mcp.BuildMCPToolApprovalQuestion(
		questionID,
		request.Server,
		request.Tool,
		request.ConnectorName,
		promptOptions,
		"",
	)
	var decision mcp.MCPToolApprovalDecision
	cancellationReason := mcp.MCPApprovalCancellationReason("")
	if h.elicitationEnabled {
		elicitationDecision, elicitationReason, err := h.approveViaElicitation(ctx, request, questionID, question)
		if err != nil {
			// A failed elicitation aborts the call, matching Rust. Rust #54679: a
			// request that never produced a client answer is an interruption, not
			// a user action.
			reason := elicitationReason
			if reason == "" {
				reason = mcp.MCPApprovalCancellationInterrupted
			}
			return mcp.MCPToolApprovalOutcome{Decision: mcp.MCPToolApprovalDeny, CancellationReason: reason}, nil
		}
		decision = elicitationDecision
		cancellationReason = elicitationReason
	} else {
		// Rust #46877: the legacy request_user_input approval flow remains
		// restricted to the root thread; a subagent hands the blocker to its
		// parent instead of prompting.
		if h.nonRootAgent {
			return mcp.MCPToolApprovalOutcome{
				Decision: mcp.MCPToolApprovalDeny,
				Message:  mcp.MCPToolApprovalUserInputRootThreadMessage,
			}, nil
		}
		response, err := h.responder(ctx, strings.TrimSpace(request.CallID), &tool.RequestUserInputArgs{Questions: []tool.UserInputQuestion{question}})
		if err != nil {
			// Rust's request_user_input failure aborts the call, and #54679
			// reports that as an interruption.
			return mcp.MCPToolApprovalOutcome{
				Decision:           mcp.MCPToolApprovalDeny,
				CancellationReason: mcp.MCPApprovalCancellationInterrupted,
			}, nil
		}
		decision = mcp.ParseMCPToolApprovalResponse(response, questionID)
		cancellationReason = mcpToolApprovalUserInputCancellationReason(response, questionID)
	}
	decision = mcp.NormalizeMCPToolApprovalDecision(decision, request.ApprovalMode)
	// Rust #54679 keeps a cancellation reason only on an abort: an approval's
	// provenance is meaningless, and a rejection has its own message.
	if decision != mcp.MCPToolApprovalDeny {
		cancellationReason = ""
	}
	if request.SessionKey == nil {
		return mcp.MCPToolApprovalOutcome{Decision: decision, CancellationReason: cancellationReason}, nil
	}
	switch decision {
	case mcp.MCPToolApprovalApproveForSession:
		h.router.rememberMCPToolApproval(h.threadID, *request.SessionKey)
	case mcp.MCPToolApprovalApproveAndRemember:
		if err := h.persistApproval(request); err != nil {
			// Rust falls back to a session-remembered approval when the amendment
			// cannot be persisted.
			slog.Warn("failed to persist MCP tool approval", "server", request.Server, "tool", request.Tool, "error", err)
			h.router.rememberMCPToolApproval(h.threadID, *request.SessionKey)
		}
	}
	return mcp.MCPToolApprovalOutcome{Decision: decision, CancellationReason: cancellationReason}, nil
}

// mcpToolApprovalUserInputCancellationReason ports Rust #54679's provenance for
// the request_user_input prompt path: choosing the cancel option is a user
// action, while a missing response is an interruption.
func mcpToolApprovalUserInputCancellationReason(response *tool.UserInputResponse, questionID string) mcp.MCPApprovalCancellationReason {
	if response == nil {
		return mcp.MCPApprovalCancellationInterrupted
	}
	if mcp.MCPToolApprovalResponseCancels(response, questionID) {
		return mcp.MCPApprovalCancellationUser
	}
	return ""
}

// MCP tool approval elicitation values and labels (Rust codex_protocol's
// mcp_approval_meta plus the TUI's approval_action option values).
const (
	mcpToolApprovalElicitationField   = "decision"
	mcpToolApprovalResponseMode       = "approval_action"
	mcpToolApprovalRequestType        = "approval_request"
	mcpToolApprovalKind               = "mcp_tool_call"
	mcpToolApprovalAcceptValue        = "accept"
	mcpToolApprovalAcceptSessionValue = "accept_session"
	mcpToolApprovalAcceptAlwaysValue  = "accept_always"
	mcpToolApprovalDeclineValue       = "decline"
	mcpToolApprovalCancelValue        = "cancel"
	mcpToolApprovalAcceptLabel        = "Allow"
	mcpToolApprovalAcceptSessionLabel = "Allow for this session"
	mcpToolApprovalAcceptAlwaysLabel  = "Allow and don't ask me again"
	mcpToolApprovalDeclineLabel       = "Reject"
	mcpToolApprovalCancelLabel        = "Cancel"
)

// approveViaElicitation ports Rust's tool-call MCP elicitation path: the
// approval is surfaced as an elicitation form whose single select field carries
// the approval values the TUI maps back to accept/session/always/decline/cancel
// (Rust sends an empty form plus `codex_approval_kind` meta; Go's TUI detects
// the approval from `response_mode` or that select field, so both are sent).
// runPermissionRequestHooks runs the turn's PermissionRequest hooks for an MCP
// tool call with Rust's payload (the canonical hook tool name and the call
// arguments) and reports whether the hooks decided. A hook denial fails the
// call with the hook's message (Rust ToolError::Rejected).
func (h *appserverMCPToolApprovalHandler) runPermissionRequestHooks(ctx context.Context, request *mcp.MCPToolApprovalRequest) (mcp.MCPToolApprovalDecision, error, bool) {
	if h == nil || h.router == nil || request == nil {
		return mcp.MCPToolApprovalDeny, nil, false
	}
	toolName := ""
	aliases := []string(nil)
	if request.HookToolName != nil {
		toolName = request.HookToolName.Name
		aliases = request.HookToolName.MatcherAliases
	}
	if strings.TrimSpace(toolName) == "" {
		return mcp.MCPToolApprovalDeny, nil, false
	}
	verdict, ok := h.router.permissionRequestHookVerdict(ctx, h.threadID, h.turnID, strings.TrimSpace(request.CallID), toolName, aliases, mcpToolCallHookToolInput(request.Arguments), nil, request.EnvironmentID)
	if !ok || verdict == nil {
		return mcp.MCPToolApprovalDeny, nil, false
	}
	switch verdict.Kind {
	case HookPermissionRequestAllow:
		return mcp.MCPToolApprovalApprove, nil, true
	default:
		reason := ""
		if verdict.Message != nil {
			reason = strings.TrimSpace(*verdict.Message)
		}
		if reason == "" {
			reason = "A permission-request hook denied this MCP tool call."
		}
		// Rust rejects the call with the hook's message
		// (ToolError::Rejected(message)).
		return mcp.MCPToolApprovalDeny, fmt.Errorf("%s", reason), true
	}
}

// mcpToolCallHookToolInput mirrors Rust's McpToolCall permission payload: the
// hook input is the call's arguments object.
func mcpToolCallHookToolInput(arguments any) map[string]any {
	return mapFromAny(arguments)
}

// reviewViaGuardian routes an MCP tool call through the turn's automatic
// approval review (Rust request_reviewer_approval -> ApprovalAction::McpToolCall).
func (h *appserverMCPToolApprovalHandler) reviewViaGuardian(ctx context.Context, request *mcp.MCPToolApprovalRequest) (mcp.MCPToolApprovalDecision, error) {
	if h == nil || h.router == nil || request == nil {
		return mcp.MCPToolApprovalDeny, nil
	}
	action := state.Action{
		Type:          "mcp_tool_call",
		Server:        strings.TrimSpace(request.Server),
		ToolName:      strings.TrimSpace(request.Tool),
		ConnectorID:   strings.TrimSpace(request.ConnectorID),
		ConnectorName: strings.TrimSpace(request.ConnectorName),
		ToolTitle:     strings.TrimSpace(request.ToolTitle),
		Extra:         map[string]any{},
	}
	if request.Arguments != nil {
		action.Arguments = request.Arguments
	}
	if description := strings.TrimSpace(request.ToolDescription); description != "" {
		action.ToolDescription = description
	}
	if description := strings.TrimSpace(request.ConnectorDescription); description != "" {
		action.ConnectorDescription = description
	}
	if request.Annotations != nil {
		action.Annotations = &state.ActionAnnotations{
			DestructiveHint: request.Annotations.DestructiveHint,
			OpenWorldHint:   request.Annotations.OpenWorldHint,
			ReadOnlyHint:    request.Annotations.ReadOnlyHint,
		}
	}
	if arguments := mcpToolCallHookToolInput(request.Arguments); len(arguments) > 0 {
		action.Extra["arguments"] = arguments
	}
	if description := strings.TrimSpace(request.ToolDescription); description != "" {
		action.Extra["tool_description"] = description
	}
	if len(action.Extra) == 0 {
		action.Extra = nil
	}
	outcome := h.router.reviewApprovalWithGuardian(ctx, h.threadID, h.turnID, strings.TrimSpace(request.CallID), action)
	switch {
	case outcome.Abort:
		return mcp.MCPToolApprovalDeny, fmt.Errorf("%s", outcome.DenyReason)
	case outcome.Approved:
		return mcp.MCPToolApprovalApprove, nil
	default:
		return mcp.MCPToolApprovalDeny, fmt.Errorf("%s", outcome.DenyReason)
	}
}

// mcpToolApprovalReviewer resolves the reviewer for one MCP tool approval,
// honoring the app account link's approvals_reviewer before the connector, the
// apps default, and the thread-level reviewer (Rust #42056). Rust passes the
// link only on this approval path; elicitation routing keeps link_id unset.
func (h *appserverMCPToolApprovalHandler) mcpToolApprovalReviewer(request *mcp.MCPToolApprovalRequest) state.Reviewer {
	if h == nil || h.router == nil || request == nil {
		return state.ReviewerUser
	}
	reviewer := h.router.approvalsReviewerForTurn(h.threadID, h.turnID)
	cfg := h.router.effectiveMCPConfigForThread(h.threadID)
	model := ""
	if active := h.router.activeTurnParams(h.threadID); active != nil {
		model = strings.TrimSpace(active.Model)
	}
	if model == "" {
		if settings := h.router.threadSettingsForTurn(h.threadID); settings != nil {
			model = strings.TrimSpace(threadSettingsModel(settings))
		}
	}
	resolved := mcpApprovalsReviewerForElicitation(cfg, string(reviewer), request.Server, request.ConnectorID, request.LinkID, model)
	return state.ReviewerFromString(resolved)
}

func (h *appserverMCPToolApprovalHandler) approveViaElicitation(
	ctx context.Context,
	request *mcp.MCPToolApprovalRequest,
	questionID string,
	question tool.UserInputQuestion,
) (mcp.MCPToolApprovalDecision, mcp.MCPApprovalCancellationReason, error) {
	if h == nil || h.router == nil {
		return mcp.MCPToolApprovalDeny, mcp.MCPApprovalCancellationInterrupted, nil
	}
	values := []any{mcpToolApprovalAcceptValue}
	labels := []any{mcpToolApprovalAcceptLabel}
	if request.AllowSessionRemember {
		values = append(values, mcpToolApprovalAcceptSessionValue)
		labels = append(labels, mcpToolApprovalAcceptSessionLabel)
	}
	if request.AllowPersistentApproval {
		values = append(values, mcpToolApprovalAcceptAlwaysValue)
		labels = append(labels, mcpToolApprovalAcceptAlwaysLabel)
	}
	values = append(values, mcpToolApprovalDeclineValue, mcpToolApprovalCancelValue)
	labels = append(labels, mcpToolApprovalDeclineLabel, mcpToolApprovalCancelLabel)
	meta := map[string]any{
		"response_mode":       mcpToolApprovalResponseMode,
		"codex_request_type":  mcpToolApprovalRequestType,
		"codex_approval_kind": mcpToolApprovalKind,
		"tool_name":           request.Tool,
	}
	if request.Arguments != nil {
		meta["tool_params"] = request.Arguments
	}
	if annotations := mcpToolApprovalAnnotations(request.Annotations); len(annotations) > 0 {
		// Rust #58161: approvals carry the registered tool's annotation hints.
		meta["tool_annotations"] = annotations
	}
	if title := strings.TrimSpace(request.ToolTitle); title != "" {
		meta["tool_title"] = title
	}
	if description := strings.TrimSpace(request.ToolDescription); description != "" {
		meta["tool_description"] = description
	}
	if connectorID := strings.TrimSpace(request.ConnectorID); connectorID != "" {
		meta["connector_id"] = connectorID
	}
	// Rust #42134: the elicited approval carries the selected account link and
	// whether the Apps service synthesized it implicitly.
	if linkID := strings.TrimSpace(request.LinkID); linkID != "" {
		meta["link_id"] = linkID
		meta["link_is_implicit"] = mcp.IsImplicitAppLinkID(linkID)
	}
	switch {
	case request.AllowSessionRemember && request.AllowPersistentApproval:
		meta["persist"] = []any{"session", "always"}
	case request.AllowSessionRemember:
		meta["persist"] = "session"
	case request.AllowPersistentApproval:
		meta["persist"] = "always"
	}
	elicitation := &mcp.MCPElicitationRequest{
		ServerName:    strings.TrimSpace(request.Server),
		ThreadID:      h.threadID,
		TurnID:        h.turnID,
		Method:        "elicitation/create",
		ElicitationID: questionID,
		Message:       question.Question,
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				mcpToolApprovalElicitationField: map[string]any{
					"type":      "string",
					"enum":      values,
					"enumNames": labels,
				},
			},
		},
		Meta: meta,
	}
	var response MCPElicitationRequestResponse
	if err := h.router.requireServerRequests().Request(ctx, ServerRequestMCPElicitation, appserverMCPElicitationParams(elicitation), &response); err != nil {
		return mcp.MCPToolApprovalDeny, "", err
	}
	decision := mcp.ParseMCPToolApprovalElicitationResponse(
		string(response.Action),
		response.Meta,
		response.Content,
		questionID,
	)
	// Rust #54679: a received cancel takes its provenance from the client's
	// metadata; an absent reason keeps the pre-existing cancellation message.
	reason := mcp.MCPApprovalCancellationReason("")
	if response.Action == MCPElicitationActionCancel {
		if parsed, ok := mcp.ParseMCPApprovalCancellationReason(response.Meta); ok {
			reason = parsed
		}
	}
	return decision, reason, nil
}

func mcpToolApprovalAnnotations(annotations *mcp.RuntimeToolAnnotations) map[string]any {
	if annotations == nil {
		return nil
	}
	out := map[string]any{}
	if annotations.ReadOnlyHint != nil {
		out["readOnlyHint"] = *annotations.ReadOnlyHint
	}
	if annotations.DestructiveHint != nil {
		out["destructiveHint"] = *annotations.DestructiveHint
	}
	if annotations.IdempotentHint != nil {
		out["idempotentHint"] = *annotations.IdempotentHint
	}
	if annotations.OpenWorldHint != nil {
		out["openWorldHint"] = *annotations.OpenWorldHint
	}
	return out
}

// persistApproval reuses the elicitation-path persistence so the amendment lands
// on exactly the key Rust's maybe_persist_mcp_tool_approval targets.
func (h *appserverMCPToolApprovalHandler) persistApproval(request *mcp.MCPToolApprovalRequest) error {
	if h == nil || h.router == nil {
		return nil
	}
	meta := map[string]any{"tool_name": strings.TrimSpace(request.Tool)}
	if connectorID := strings.TrimSpace(request.ConnectorID); connectorID != "" {
		meta["connector_id"] = connectorID
	}
	if linkID := strings.TrimSpace(request.LinkID); linkID != "" {
		meta["link_id"] = linkID
	}
	persistRequest := &mcp.MCPElicitationRequest{
		ServerName: strings.TrimSpace(request.Server),
		ThreadID:   h.threadID,
		TurnID:     h.turnID,
		Meta:       meta,
	}
	return h.router.persistMCPToolApprovalAmendment(persistRequest, &MCPElicitationRequestResponse{
		Action: MCPElicitationActionAccept,
		Meta:   map[string]any{"persist": "always"},
	})
}

// mcpToolApprovalRemembered reports a session-remembered approval.
func (r *RuntimeRouter) mcpToolApprovalRemembered(threadID string, key mcp.MCPToolApprovalKey) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	r.mcpToolApprovalsMu.Lock()
	defer r.mcpToolApprovalsMu.Unlock()
	return r.mcpToolApprovals[threadID][key]
}

// rememberMCPToolApproval records a session-remembered approval.
func (r *RuntimeRouter) rememberMCPToolApproval(threadID string, key mcp.MCPToolApprovalKey) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.mcpToolApprovalsMu.Lock()
	defer r.mcpToolApprovalsMu.Unlock()
	if r.mcpToolApprovals == nil {
		r.mcpToolApprovals = map[string]map[mcp.MCPToolApprovalKey]bool{}
	}
	if r.mcpToolApprovals[threadID] == nil {
		r.mcpToolApprovals[threadID] = map[mcp.MCPToolApprovalKey]bool{}
	}
	r.mcpToolApprovals[threadID][key] = true
}

// forgetMCPToolApprovals drops a thread's remembered approvals when its session
// is unloaded (Rust drops the session's tool_approvals store).
func (r *RuntimeRouter) forgetMCPToolApprovals(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.mcpToolApprovalsMu.Lock()
	defer r.mcpToolApprovalsMu.Unlock()
	delete(r.mcpToolApprovals, threadID)
}
