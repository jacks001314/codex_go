package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/state"
)

type appserverMCPElicitationHandler struct {
	broker              *ServerRequestBroker
	reviewer            GuardianReviewer
	authority           func(string, string, string) mcpElicitationAuthority
	persist             func(*mcp.MCPElicitationRequest, *MCPElicitationRequestResponse) error
	record              func(*mcp.MCPElicitationRequest, *MCPElicitationRequestResponse)
	fullAccessFormInput atomic.Bool
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
}

func (h *appserverMCPElicitationHandler) HandleMCPElicitation(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
	// Rust 4b0e2a0bff: tool-suggestion elicitations are never surfaced as
	// form input; they stay on their existing decline path.
	if mcpElicitationApprovalKind(request) == "tool_suggestion" {
		return mcpElicitationAutoDecline(), nil
	}
	authority := mcpElicitationAuthority{ApprovalPolicy: sandbox.ApprovalOnRequest, ApprovalsReviewer: "user", AllowsMCPElicitations: true}
	legacyAuthority := h == nil || h.authority == nil
	if h != nil && h.authority != nil {
		threadID := ""
		if request != nil {
			threadID = strings.TrimSpace(request.ThreadID)
		}
		authority = h.authority(threadID, mcpElicitationServerName(request), mcpElicitationConnectorID(request))
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
		if !legacyAuthority && ((authority.ApprovalPolicy != sandbox.ApprovalOnRequest && authority.ApprovalPolicy != sandbox.ApprovalGranular) || !strings.EqualFold(strings.TrimSpace(authority.ApprovalsReviewer), "auto_review")) {
			return h.requestMCPElicitation(ctx, request)
		}
		if reason := validateGuardianMCPElicitation(request); reason != "" {
			return mcpElicitationAutoDecline(), nil
		}
		if h != nil && h.reviewer != nil {
			decision, reason, err := h.reviewer.Review(ctx, request.ThreadID, request.TurnID, appserverMCPElicitationID(request), guardianMCPAction(request))
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
	}
	return h.requestMCPElicitation(ctx, request)
}

func (h *appserverMCPElicitationHandler) requestMCPElicitation(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
	if h == nil || h.broker == nil {
		return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}, nil
	}
	params := appserverMCPElicitationParams(request)
	var response MCPElicitationRequestResponse
	if err := h.broker.Request(ctx, ServerRequestMCPElicitation, params, &response); err != nil {
		action := mcp.MCPElicitationActionDecline
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			action = mcp.MCPElicitationActionCancel
		}
		return &mcp.MCPElicitationResponse{Action: action}, nil
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

func guardianMCPAction(request *mcp.MCPElicitationRequest) state.Action {
	meta, _ := requestMetaMap(request)
	connectorID := strings.TrimSpace(stringFromMap(meta, "connector_id"))
	connectorName := strings.TrimSpace(stringFromMap(meta, "connector_name"))
	toolTitle := strings.TrimSpace(stringFromMap(meta, "tool_title"))
	return state.Action{
		Type:          "mcp_tool_call",
		Server:        strings.TrimSpace(request.ServerName),
		ToolName:      strings.TrimSpace(stringFromMap(meta, "tool_name")),
		ConnectorID:   connectorID,
		ConnectorName: connectorName,
		ToolTitle:     toolTitle,
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
	meta, ok := requestMetaMap(request)
	return ok && strings.TrimSpace(stringFromMap(meta, "codex_request_type")) == "approval_request"
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
	if strings.TrimSpace(request.Method) == "openai/form" {
		mode = "openai/form"
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
