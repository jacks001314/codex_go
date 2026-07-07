package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/internal/mcp"
)

type appserverMCPElicitationHandler struct {
	broker *ServerRequestBroker
}

func (h *appserverMCPElicitationHandler) HandleMCPElicitation(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
	if h == nil || h.broker == nil {
		return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}, nil
	}
	params := appserverMCPElicitationParams(request)
	var response MCPElicitationRequestResponse
	if err := h.broker.Request(ctx, ServerRequestMCPElicitation, params, &response); err != nil {
		return nil, err
	}
	return &mcp.MCPElicitationResponse{
		Action:  mcp.MCPElicitationAction(response.Action),
		Content: response.Content,
		Meta:    response.Meta,
	}, nil
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
