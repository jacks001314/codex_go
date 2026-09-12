package appserver

import (
	"context"
	"log/slog"
	"strings"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/features"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/tool"
)

// appserverMCPToolApprovalHandler serves Rust's custom-MCP-server tool approval
// policy (core/src/mcp_tool_call.rs maybe_request_mcp_tool_approval): it applies
// the session/persistent remembered choices, asks the client through the
// `tool/requestUserInput` surface, and records the answer.
type appserverMCPToolApprovalHandler struct {
	router         *RuntimeRouter
	service        *mcp.MCPService
	responder      tool.UserInputResponder
	threadID       string
	turnID         string
	approvalPolicy sandbox.AskForApproval
	// persistentApprovalAllowed mirrors Rust's ToolCallMcpElicitation feature
	// gate: only when it is on may the prompt offer "allow and don't ask me
	// again".
	persistentApprovalAllowed bool
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
	responder := r.userInputResponderForTurn(threadID, strings.TrimSpace(turnID))
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
		},
	}
}

// ApproveMCPToolCall returns the decision for a custom MCP tool call that the
// executor determined requires approval.
func (h *appserverMCPToolApprovalHandler) ApproveMCPToolCall(ctx context.Context, request *mcp.MCPToolApprovalRequest) (mcp.MCPToolApprovalDecision, error) {
	if h == nil || request == nil {
		return mcp.MCPToolApprovalDeny, nil
	}
	if request.SessionKey != nil && h.router.mcpToolApprovalRemembered(h.threadID, *request.SessionKey) {
		return mcp.MCPToolApprovalApprove, nil
	}
	if h.responder == nil {
		return mcp.MCPToolApprovalDeny, nil
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
	response, err := h.responder(ctx, &tool.RequestUserInputArgs{Questions: []tool.UserInputQuestion{question}})
	if err != nil {
		// Rust's request_user_input failure aborts the call.
		return mcp.MCPToolApprovalDeny, nil
	}
	decision := mcp.NormalizeMCPToolApprovalDecision(
		mcp.ParseMCPToolApprovalResponse(response, questionID),
		request.ApprovalMode,
	)
	if request.SessionKey == nil {
		return decision, nil
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
	return decision, nil
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
