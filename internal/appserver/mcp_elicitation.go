package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/internal/mcp"
	"codex_go/internal/state"
)

type appserverMCPElicitationHandler struct {
	broker   *ServerRequestBroker
	reviewer GuardianReviewer
}

func (h *appserverMCPElicitationHandler) HandleMCPElicitation(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
	if guardianMCPApprovalRequested(request) {
		if reason := validateGuardianMCPElicitation(request); reason != "" {
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionDecline, Meta: map[string]any{"approvals_reviewer": "auto_review"}}, nil
		}
		if h != nil && h.reviewer != nil {
			decision, reason, err := h.reviewer.Review(ctx, request.ThreadID, request.TurnID, appserverMCPElicitationID(request), guardianMCPAction(request))
			if err == nil && decision == state.DecisionApproved {
				return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionAccept, Meta: map[string]any{"approvals_reviewer": "auto_review"}}, nil
			}
			meta := map[string]any{"approvals_reviewer": "auto_review"}
			if strings.TrimSpace(reason) != "" {
				meta["reason"] = reason
			}
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionDecline, Meta: meta}, nil
		}
	}
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
	return &mcp.MCPElicitationResponse{
		Action:  mcp.MCPElicitationAction(response.Action),
		Content: response.Content,
		Meta:    response.Meta,
	}, nil
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
