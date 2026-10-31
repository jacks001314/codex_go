package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/state"
)

type appserverMCPElicitationHandler struct {
	broker    *ServerRequestBroker
	service   *mcp.MCPService
	reviewer  GuardianReviewer
	authority func(string, string, string) mcpElicitationAuthority
	persist   func(*mcp.MCPElicitationRequest, *MCPElicitationRequestResponse) error
	record    func(*mcp.MCPElicitationRequest, *MCPElicitationRequestResponse)
	// invalidateTiming drops the turn's captured critical path. An elicitation
	// request ID is not a reliable tool invocation owner (Rust #57207).
	invalidateTiming    func(threadID, turnID string)
	fullAccessFormInput atomic.Bool
	// userVerificationConnection identifies the one trusted local connection
	// activated at initialize. Verification requests never broadcast to other
	// clients (Rust #44613).
	userVerificationConnection func() string
}

// EnableFullAccessFormInput turns on surfacing non-approval MCP forms in
// full-access, user-initiated root threads. It is enabled only after session
// startup so required MCP servers cannot block startup waiting for form input
// (Rust 4b0e2a0bff).
func (h *appserverMCPElicitationHandler) EnableFullAccessFormInput() {
	if h == nil {
		return
	}
	h.fullAccessFormInput.Store(true)
}

func (h *appserverMCPElicitationHandler) fullAccessFormInputEnabled() bool {
	return h != nil && h.fullAccessFormInput.Load()
}

type mcpElicitationAuthority struct {
	ApprovalPolicy        sandbox.AskForApproval
	ApprovalsReviewer     string
	PermissionProfile     *sandbox.PermissionProfile
	AllowsMCPElicitations bool
	// ServerAuthorityPublished reports that the thread's MCP runtime published
	// per-server authority. When set and PermissionProfile is nil, the server
	// has no owner permissions and the elicitation must be declined (Rust #40728).
	ServerAuthorityPublished bool
}

func (h *appserverMCPElicitationHandler) HandleMCPElicitation(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
	authority := mcpElicitationAuthority{ApprovalPolicy: sandbox.ApprovalOnRequest, ApprovalsReviewer: "user", AllowsMCPElicitations: true}
	legacyAuthority := h == nil || h.authority == nil
	if h != nil && h.authority != nil {
		threadID := ""
		if request != nil {
			threadID = strings.TrimSpace(request.ThreadID)
		}
		authority = h.authority(threadID, mcpElicitationServerName(request), mcpElicitationConnectorID(request))
	}
	// Rust 4b0e2a0bff: tool-suggestion elicitations are never surfaced as
	// form input; they stay on their existing decline path.
	if mcpElicitationApprovalKind(request) == "tool_suggestion" {
		return mcpElicitationAutoDecline(), nil
	}
	// Rust #40728: a server whose published authority is unavailable must not
	// surface an elicitation at all.
	if authority.ServerAuthorityPublished && authority.PermissionProfile == nil {
		return mcpElicitationDeclineWithoutMessage(), nil
	}
	switch authority.ApprovalPolicy {
	case sandbox.ApprovalNever:
		permissionAutoApproved := mcpElicitationPermissionAutoApproved(authority.PermissionProfile)
		if permissionAutoApproved && mcpElicitationHasEmptyForm(request) {
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionAccept, Content: map[string]any{}}, nil
		}
		if permissionAutoApproved && h.fullAccessFormInputEnabled() && mcpElicitationSurfacedForm(request) {
			return h.requestMCPElicitation(ctx, request)
		}
		return mcpElicitationAutoDecline(), nil
	case sandbox.ApprovalGranular:
		if !authority.AllowsMCPElicitations {
			return mcpElicitationAutoDecline(), nil
		}
	}
	if guardianMCPApprovalRequested(request) {
		// Rust #42256: User approval mode skips ordinary node_repl.js execution
		// confirmations, while sensitive actions and requests for user input keep
		// their existing behavior.
		if !legacyAuthority && mcpElicitationUserCUAAutoApproved(authority, request) && mcpElicitationHasEmptyForm(request) {
			meta := map[string]any{"approvals_reviewer": "auto_review"}
			if ctx != nil && ctx.Err() != nil {
				return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel, Meta: meta}, nil
			}
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionAccept, Content: map[string]any{}, Meta: meta}, nil
		}
		if !legacyAuthority && ((authority.ApprovalPolicy != sandbox.ApprovalOnRequest && authority.ApprovalPolicy != sandbox.ApprovalGranular) || !strings.EqualFold(strings.TrimSpace(authority.ApprovalsReviewer), "auto_review")) {
			return h.requestMCPElicitation(ctx, request)
		}
		if reason := validateGuardianMCPElicitation(request); reason != "" {
			return mcpElicitationAutoDecline(), nil
		}
		serverName := mcpElicitationServerName(request)
		originatingCallID := mcpElicitationLiveCallID(request)
		liveInvocation, liveOK := mcp.MCPInvocationMetadata{}, false
		if h != nil && h.service != nil {
			liveInvocation, liveOK = h.service.LiveToolInvocation(serverName, originatingCallID)
		}
		liveComputerUse := liveOK && strings.TrimSpace(liveInvocation.ServerName) == serverName &&
			mcp.IsNodeReplBackedConnector(serverName, liveInvocation.ConnectorID)
		strict := mcpElicitationStrictAutoReview(request)
		action := guardianMCPAction(request)
		reviewID := appserverMCPElicitationID(request)
		if liveComputerUse {
			// Rust #47647: the nested elicitation keeps its own action identity,
			// while the assessment is attributed to the originating call.
			reviewID = originatingCallID
		}
		if strict && mcp.IsCodexAppsMCPServerName(serverName) && !liveComputerUse {
			// A hosted connector without a live Browser invocation must be
			// reviewed through its registered outer invocation. The identity
			// comes from host-registered tool metadata, never from the
			// provider-supplied elicitation arguments.
			if !liveOK || strings.TrimSpace(liveInvocation.ServerName) != serverName ||
				mcpElicitationConnectorID(request) != strings.TrimSpace(liveInvocation.ConnectorID) ||
				mcpElicitationToolName(request) != strings.TrimSpace(liveInvocation.ToolName) {
				return mcpElicitationStrictAutoReviewDecline(), nil
			}
			reviewID = originatingCallID
			action = guardianMCPRegisteredInvocationAction(liveInvocation)
		}
		if h != nil && h.reviewer != nil {
			// Rust #57207: attribute the review to the live tool invocation when
			// one is known; the elicitation ID is only a display identity.
			reviewCtx := ctx
			if liveOK && strings.TrimSpace(liveInvocation.ServerName) == serverName {
				reviewCtx = withGuardianTimingCallID(ctx, originatingCallID)
			}
			decision, reason, err := h.reviewer.Review(reviewCtx, request.ThreadID, request.TurnID, reviewID, action)
			if err == nil && decision == state.DecisionAskUser && !strict {
				// Rust #44569: an optional review that exhausts its local input
				// budget produces no Guardian response, so the elicitation falls
				// back to the user. A strict auto review still declines.
				return h.requestMCPElicitation(ctx, request)
			}
			if err == nil {
				// Rust #40031: strict MCP auto-review propagates canonical
				// approved / denied / timed-out / aborted outcomes instead of
				// collapsing them into a generic decline.
				switch decision {
				case state.DecisionApproved:
					return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionAccept, Meta: map[string]any{"approvals_reviewer": "auto_review"}}, nil
				case state.DecisionAborted:
					return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel, Meta: map[string]any{"approvals_reviewer": "auto_review"}}, nil
				}
			}
			meta := map[string]any{"approvals_reviewer": "auto_review"}
			if strings.TrimSpace(reason) != "" {
				meta["reason"] = reason
			}
			if err == nil && decision == state.DecisionTimedOut {
				meta["timed_out"] = true
			}
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionDecline, Meta: meta}, nil
		}
		if strict {
			// Strict auto review never falls back to prompting the user.
			return mcpElicitationStrictAutoReviewDecline(), nil
		}
	}
	return h.requestMCPElicitation(ctx, request)
}

func (h *appserverMCPElicitationHandler) requestMCPElicitation(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
	// Rust #57207: the callback carries a request ID, not a reliable tool
	// invocation owner, so its human wait cannot be attributed.
	if h != nil && h.invalidateTiming != nil && request != nil {
		h.invalidateTiming(request.ThreadID, request.TurnID)
	}
	if h == nil || h.broker == nil {
		if mcp.IsUserVerificationElicitation(request) {
			return &mcp.MCPElicitationResponse{
				Action: mcp.MCPElicitationActionCancel,
				Meta:   mcp.UserVerificationReasonMeta(mcp.UserVerificationReasonApprovalUnavailable),
			}, nil
		}
		return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}, nil
	}
	params := appserverMCPElicitationParams(request)
	connectionID := ""
	if mcp.IsUserVerificationElicitation(request) {
		if h.userVerificationConnection != nil {
			connectionID = strings.TrimSpace(h.userVerificationConnection())
		}
		if connectionID == "" {
			return &mcp.MCPElicitationResponse{
				Action: mcp.MCPElicitationActionCancel,
				Meta:   mcp.UserVerificationReasonMeta(mcp.UserVerificationReasonApprovalUnavailable),
			}, nil
		}
	}
	var response MCPElicitationRequestResponse
	if err := h.broker.RequestToConnection(ctx, connectionID, ServerRequestMCPElicitation, params, &response); err != nil {
		if mcp.IsUserVerificationElicitation(request) {
			reason := mcp.UserVerificationReasonResponseError
			if errors.Is(err, ErrServerRequestConnectionClosed) {
				reason = mcp.UserVerificationReasonResponseChannelClosed
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				reason = mcp.UserVerificationReasonInterrupted
			}
			return &mcp.MCPElicitationResponse{
				Action: mcp.MCPElicitationActionCancel,
				Meta:   mcp.UserVerificationReasonMeta(reason),
			}, nil
		}
		action := mcp.MCPElicitationActionDecline
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			action = mcp.MCPElicitationActionCancel
		}
		cancelled := &mcp.MCPElicitationResponse{Action: action}
		if action == mcp.MCPElicitationActionCancel && mcpElicitationApprovalKind(request) == "mcp_tool_call" {
			// Rust #54679: the synthesized cancel of an interrupted tool-call
			// approval records the interruption, so the abort reports a
			// cancellation before approval instead of a user action. A successful
			// client reply keeps its own provenance, including an absent reason.
			cancelled.Meta = map[string]any{
				mcp.MCPApprovalCancellationReasonKey: string(mcp.MCPApprovalCancellationInterrupted),
			}
		}
		return cancelled, nil
	}
	// Rust 2230d64464 (#38108): "Allow and don't ask me again" persists the
	// MCP tool approval as a policy amendment (approval_mode: approve) on the
	// owning client. Non-MCP approval paths and non-accept actions reject it.
	if response.Action == MCPElicitationActionAccept && mcpElicitationApprovalKind(request) == "mcp_tool_call" && mcpElicitationRequestsPersistentApproval(&response) && h.persist != nil {
		if err := h.persist(request, &response); err != nil {
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionDecline, Meta: map[string]any{"message": "failed to persist MCP tool approval: " + err.Error()}}, nil
		}
	}
	// Rust 2230d64464 (#38108): record the unified approval resolution source
	// (user) for MCP tool call approvals resolved through the client.
	if mcpElicitationApprovalKind(request) == "mcp_tool_call" && h.record != nil {
		h.record(request, &response)
	}
	return &mcp.MCPElicitationResponse{
		Action:  mcp.MCPElicitationAction(response.Action),
		Content: response.Content,
		Meta:    response.Meta,
	}, nil
}

// mcpElicitationRequestsPersistentApproval reports whether the client's
// elicitation response opts into a persistent MCP policy amendment
// (Rust codex_protocol::mcp_approval_meta::PERSIST_ALWAYS).
func mcpElicitationRequestsPersistentApproval(response *MCPElicitationRequestResponse) bool {
	if response == nil {
		return false
	}
	meta, ok := response.Meta.(map[string]any)
	if !ok {
		return false
	}
	return strings.TrimSpace(stringFromMap(meta, "persist")) == "always"
}

func mcpElicitationAutoDecline() *mcp.MCPElicitationResponse {
	return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionDecline, Meta: map[string]any{"approvals_reviewer": "auto_review"}}
}

// mcpElicitationDeclineWithoutMessage mirrors Rust
// mcp_elicitation_decline_without_message: a fail-closed decline with no meta.
func mcpElicitationDeclineWithoutMessage() *mcp.MCPElicitationResponse {
	return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionDecline}
}

func mcpElicitationPermissionAutoApproved(profile *sandbox.PermissionProfile) bool {
	if profile == nil {
		return false
	}
	return profile.Disabled || profile.LegacySandboxPolicy().HasFullDiskWriteAccess()
}

func mcpElicitationHasEmptyForm(request *mcp.MCPElicitationRequest) bool {
	if request == nil || strings.TrimSpace(request.Method) != "elicitation/create" || strings.TrimSpace(request.URL) != "" {
		return false
	}
	schema, ok := request.RequestedSchema.(map[string]any)
	if !ok {
		return request.RequestedSchema == nil
	}
	properties, ok := schema["properties"].(map[string]any)
	return !ok || len(properties) == 0
}

// mcpElicitationSurfacedForm reports whether a standard MCP form requires
// user-entered values and is eligible for full-access form input surfacing:
// a form elicitation with a non-empty properties schema and no privileged
// Codex approval-kind metadata.
func mcpElicitationSurfacedForm(request *mcp.MCPElicitationRequest) bool {
	if request == nil || strings.TrimSpace(request.Method) != "elicitation/create" || strings.TrimSpace(request.URL) != "" {
		return false
	}
	if mcpElicitationApprovalKind(request) != "" {
		return false
	}
	schema, ok := request.RequestedSchema.(map[string]any)
	if !ok {
		return false
	}
	properties, ok := schema["properties"].(map[string]any)
	return ok && len(properties) > 0
}

func mcpElicitationApprovalKind(request *mcp.MCPElicitationRequest) string {
	meta, ok := requestMetaMap(request)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stringFromMap(meta, "codex_approval_kind"))
}

func mcpElicitationStrictAutoReview(request *mcp.MCPElicitationRequest) bool {
	meta, ok := requestMetaMap(request)
	if !ok {
		return false
	}
	value, _ := meta["codex_strict_auto_review"].(bool)
	return value
}

// mcpStrictAutoReviewDeclineMessage mirrors Rust
// codex_mcp::elicitation::STRICT_AUTO_REVIEW_DECLINE_MESSAGE: a strict review
// that cannot verify the request fails closed without prompting the user.
const mcpStrictAutoReviewDeclineMessage = "Automated review of this operation failed. Do not proceed without asking the user for explicit approval."

func mcpElicitationStrictAutoReviewDecline() *mcp.MCPElicitationResponse {
	return &mcp.MCPElicitationResponse{
		Action: mcp.MCPElicitationActionDecline,
		Meta:   map[string]any{"message": mcpStrictAutoReviewDeclineMessage},
	}
}

func mcpElicitationToolName(request *mcp.MCPElicitationRequest) string {
	meta, ok := requestMetaMap(request)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stringFromMap(meta, "tool_name"))
}

func mcpElicitationLiveCallID(request *mcp.MCPElicitationRequest) string {
	meta, ok := requestMetaMap(request)
	if !ok {
		return ""
	}
	if mcpElicitationServerName(request) == mcp.RuntimeCodexAppsMCPServerName {
		if apps := mapFromAny(meta[mcp.MCPToolCodexAppsMetaKey]); apps != nil {
			return strings.TrimSpace(stringFromMap(apps, "call_id"))
		}
		return ""
	}
	return strings.TrimSpace(stringFromMap(meta, "callId"))
}

// guardianMCPRegisteredInvocationAction builds the review action for a hosted
// connector's registered outer invocation. Rust #47647 reviews the invocation
// recorded by the host instead of trusting connector or tool identity supplied
// by the nested elicitation.
func guardianMCPRegisteredInvocationAction(invocation mcp.MCPInvocationMetadata) state.Action {
	arguments := cloneAnyMapAppserver(mapFromAny(invocation.Arguments))
	return state.Action{
		Type:                 "mcp_tool_call",
		Server:               strings.TrimSpace(invocation.ServerName),
		ToolName:             strings.TrimSpace(invocation.ToolName),
		ConnectorID:          strings.TrimSpace(invocation.ConnectorID),
		ConnectorName:        strings.TrimSpace(invocation.ConnectorName),
		ToolTitle:            strings.TrimSpace(invocation.ToolTitle),
		Arguments:            arguments,
		ToolDescription:      strings.TrimSpace(invocation.ToolDescription),
		ConnectorDescription: strings.TrimSpace(invocation.ConnectorDescription),
		Extra:                map[string]any{"arguments": arguments},
	}
}

func guardianMCPAction(request *mcp.MCPElicitationRequest) state.Action {
	meta, _ := requestMetaMap(request)
	connectorID := strings.TrimSpace(stringFromMap(meta, "connector_id"))
	connectorName := strings.TrimSpace(stringFromMap(meta, "connector_name"))
	toolTitle := strings.TrimSpace(stringFromMap(meta, "tool_title"))
	return state.Action{
		Type:            "mcp_tool_call",
		Server:          strings.TrimSpace(request.ServerName),
		ToolName:        strings.TrimSpace(stringFromMap(meta, "tool_name")),
		ConnectorID:     connectorID,
		ConnectorName:   connectorName,
		ToolTitle:       toolTitle,
		Arguments:       cloneAnyMapAppserver(mapFromAny(meta["tool_params"])),
		ToolDescription: strings.TrimSpace(stringFromMap(meta, "tool_description")),
		ConnectorDescription: firstNonEmpty(
			stringFromMap(meta, "connector_description"),
			stringFromMap(meta, "connectorDescription"),
		),
		Extra: map[string]any{
			"arguments": cloneAnyMapAppserver(mapFromAny(meta["tool_params"])),
		},
	}
}

func mapFromAny(value any) map[string]any {
	values, _ := value.(map[string]any)
	return values
}

func guardianMCPApprovalRequested(request *mcp.MCPElicitationRequest) bool {
	// Rust's guardian_elicitation_review_request only inspects the MCP
	// elicitation variants: the OpenAI form modes and user verification answer
	// NotRequested before any metadata is read (session/mcp.rs), so such an
	// elicitation is never reviewed and never declined here.
	if request == nil || strings.TrimSpace(request.Method) != "elicitation/create" {
		return false
	}
	meta, ok := requestMetaMap(request)
	return ok && strings.TrimSpace(stringFromMap(meta, "codex_request_type")) == "approval_request"
}

// mcpElicitationUserCUAAutoApproved mirrors Rust #42256's user_cua_execution:
// in User approval mode, an ordinary node_repl.js tool-call confirmation is
// accepted without review. Sensitive-action checks and requests that explicitly
// need user input are excluded, as are non-node-repl servers and other tools.
func mcpElicitationUserCUAAutoApproved(authority mcpElicitationAuthority, request *mcp.MCPElicitationRequest) bool {
	if !strings.EqualFold(strings.TrimSpace(authority.ApprovalsReviewer), string(config.ApprovalsReviewerUser)) {
		return false
	}
	if request == nil || !mcp.IsNodeReplBackedServer(strings.TrimSpace(request.ServerName)) {
		return false
	}
	meta, ok := requestMetaMap(request)
	if !ok {
		return false
	}
	sensitive, _ := meta["codex_sensitive_action"].(bool)
	requiresUserInput, _ := meta["codex_requires_user_input"].(bool)
	return strings.TrimSpace(stringFromMap(meta, "tool_name")) == "js" &&
		strings.TrimSpace(stringFromMap(meta, "connector_id")) == "node_repl" &&
		strings.TrimSpace(stringFromMap(meta, "codex_approval_kind")) == "mcp_tool_call" &&
		!sensitive &&
		!requiresUserInput
}

func validateGuardianMCPElicitation(request *mcp.MCPElicitationRequest) string {
	if request == nil || strings.TrimSpace(request.Method) != "elicitation/create" || strings.TrimSpace(request.URL) != "" {
		return "guardian MCP elicitation review only supports form elicitations"
	}
	meta, ok := requestMetaMap(request)
	if !ok || strings.TrimSpace(stringFromMap(meta, "codex_approval_kind")) != "mcp_tool_call" {
		return "guardian MCP elicitation metadata must declare mcp_tool_call approval kind"
	}
	if schema, ok := request.RequestedSchema.(map[string]any); ok {
		if properties, ok := schema["properties"].(map[string]any); ok && len(properties) > 0 {
			return "guardian MCP elicitation review only supports empty form schemas"
		}
	}
	if strings.TrimSpace(stringFromMap(meta, "tool_name")) == "" {
		return "guardian MCP elicitation metadata must include a non-empty tool_name"
	}
	if value, exists := meta["tool_params"]; exists {
		if _, ok := value.(map[string]any); !ok {
			return "guardian MCP elicitation tool_params must be an object"
		}
	}
	return ""
}

func requestMetaMap(request *mcp.MCPElicitationRequest) (map[string]any, bool) {
	if request == nil {
		return nil, false
	}
	meta, ok := request.Meta.(map[string]any)
	return meta, ok
}

// mcpElicitationServerName returns the MCP server name that issued the
// elicitation request, falling back to the request's server field.
func mcpElicitationServerName(request *mcp.MCPElicitationRequest) string {
	if request == nil {
		return ""
	}
	return strings.TrimSpace(request.ServerName)
}

// mcpElicitationConnectorID returns the connector id carried in the
// elicitation metadata for codex-apps tool calls (Rust
// codex_protocol::mcp_approval_meta::CONNECTOR_ID_KEY).
func mcpElicitationConnectorID(request *mcp.MCPElicitationRequest) string {
	meta, ok := requestMetaMap(request)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stringFromMap(meta, "connector_id"))
}

func appserverMCPElicitationParams(request *mcp.MCPElicitationRequest) *MCPElicitationRequestParams {
	if request == nil {
		return &MCPElicitationRequestParams{Mode: "form"}
	}
	threadID, turnID := appserverMCPElicitationContext(request.Meta)
	if strings.TrimSpace(request.ThreadID) != "" {
		threadID = strings.TrimSpace(request.ThreadID)
	}
	if strings.TrimSpace(request.TurnID) != "" {
		turnIDValue := strings.TrimSpace(request.TurnID)
		turnID = &turnIDValue
	}
	mode := "form"
	switch method := strings.TrimSpace(request.Method); method {
	case "openai/form", "openaiForm", "openai/userVerification":
		mode = method
	case "openai/elicitation/create":
		// The umbrella OpenAI method carries its kind in the payload: the form
		// mode is Rust's OpenAiElicitationForm, whose client-facing mode is
		// `openaiForm`.
		if strings.TrimSpace(request.Mode) == "form" {
			mode = "openaiForm"
		}
	}
	if strings.TrimSpace(request.URL) != "" {
		mode = "url"
	}
	return &MCPElicitationRequestParams{
		ThreadID:        threadID,
		TurnID:          turnID,
		ServerName:      strings.TrimSpace(request.ServerName),
		Server:          strings.TrimSpace(request.ServerName),
		Mode:            mode,
		Meta:            request.Meta,
		Message:         request.Message,
		RequestedSchema: request.RequestedSchema,
		Schema:          request.RequestedSchema,
		Title:           request.Title,
		Description:     request.Description,
		Challenge:       request.Challenge,
		URL:             request.URL,
		ElicitationID:   appserverMCPElicitationID(request),
	}
}

func appserverMCPElicitationContext(meta any) (string, *string) {
	values, ok := meta.(map[string]any)
	if !ok {
		return "", nil
	}
	threadID := strings.TrimSpace(stringFromMap(values, "threadId"))
	turnID := strings.TrimSpace(stringFromMap(values, "turnId"))
	if turnID == "" {
		return threadID, nil
	}
	return threadID, &turnID
}

func appserverMCPElicitationID(request *mcp.MCPElicitationRequest) string {
	if request == nil {
		return ""
	}
	if id := strings.TrimSpace(request.ElicitationID); id != "" {
		return id
	}
	if len(request.ID) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(request.ID, &value); err != nil {
		return strings.TrimSpace(string(request.ID))
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%.0f", typed))
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
