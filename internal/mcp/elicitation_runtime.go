package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

type MCPElicitationAction string

const (
	MCPElicitationActionAccept  MCPElicitationAction = "accept"
	MCPElicitationActionDecline MCPElicitationAction = "decline"
	MCPElicitationActionCancel  MCPElicitationAction = "cancel"
)

type MCPElicitationRequest struct {
	ServerName      string          `json:"serverName"`
	ThreadID        string          `json:"threadId,omitempty"`
	TurnID          string          `json:"turnId,omitempty"`
	Method          string          `json:"method"`
	ID              json.RawMessage `json:"id,omitempty"`
	Message         string          `json:"message,omitempty"`
	RequestedSchema any             `json:"requestedSchema,omitempty"`
	URL             string          `json:"url,omitempty"`
	ElicitationID   string          `json:"elicitationId,omitempty"`
	Meta            any             `json:"_meta,omitempty"`
	Params          json.RawMessage `json:"params,omitempty"`
}

type MCPElicitationResponse struct {
	Action  MCPElicitationAction `json:"action"`
	Content any                  `json:"content,omitempty"`
	Meta    any                  `json:"_meta,omitempty"`
}

type MCPElicitationHandler interface {
	HandleMCPElicitation(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error)
}

type MCPElicitationHandlerFunc func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error)

func (f MCPElicitationHandlerFunc) HandleMCPElicitation(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
	if f == nil {
		return nil, nil
	}
	return f(ctx, request)
}

func mcpElicitationResult(ctx context.Context, serverName string, handler MCPElicitationHandler, method string, id json.RawMessage, params json.RawMessage) any {
	request := parseMCPElicitationRequest(serverName, method, id, params)
	request.ThreadID, request.TurnID = mcpElicitationContextFromContext(ctx)
	if handler == nil {
		return &MCPElicitationResponse{Action: MCPElicitationActionCancel}
	}
	response, err := handler.HandleMCPElicitation(ctx, request)
	if err != nil {
		return &MCPElicitationResponse{
			Action: MCPElicitationActionDecline,
			Meta:   map[string]any{"message": err.Error()},
		}
	}
	return normalizeMCPElicitationResponse(response)
}

func parseMCPElicitationRequest(serverName string, method string, id json.RawMessage, params json.RawMessage) *MCPElicitationRequest {
	request := &MCPElicitationRequest{
		ServerName: strings.TrimSpace(serverName),
		Method:     strings.TrimSpace(method),
		ID:         append(json.RawMessage(nil), id...),
		Params:     append(json.RawMessage(nil), params...),
	}
	var raw map[string]any
	if len(params) == 0 || json.Unmarshal(params, &raw) != nil {
		return request
	}
	request.Message = stringFromAnyMap(raw, "message")
	request.URL = stringFromAnyMap(raw, "url")
	request.ElicitationID = stringFromAnyMap(raw, "elicitationId")
	if request.ElicitationID == "" {
		request.ElicitationID = stringFromAnyMap(raw, "elicitation_id")
	}
	if value, ok := raw["requestedSchema"]; ok {
		request.RequestedSchema = cloneJSONValue(value)
	} else if value, ok := raw["requested_schema"]; ok {
		request.RequestedSchema = cloneJSONValue(value)
	}
	if value, ok := raw["_meta"]; ok {
		request.Meta = cloneJSONValue(value)
	} else if value, ok := raw["meta"]; ok {
		request.Meta = cloneJSONValue(value)
	}
	return request
}

type mcpElicitationContextKey struct{}

type mcpElicitationContextValue struct {
	ThreadID string
	TurnID   string
	ItemID   string
	Roots    []MCPRoot
}

func contextWithMCPElicitationContext(ctx context.Context, threadID string, turnID string) context.Context {
	return contextWithMCPClientContext(ctx, threadID, turnID, "")
}

func contextWithMCPClientContext(ctx context.Context, threadID string, turnID string, itemID string) context.Context {
	return contextWithMCPClientContextAndRoots(ctx, threadID, turnID, itemID, nil)
}

func contextWithMCPClientContextAndRoots(ctx context.Context, threadID string, turnID string, itemID string, roots []MCPRoot) context.Context {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	itemID = strings.TrimSpace(itemID)
	clonedRoots := cloneMCPRoots(roots)
	if threadID == "" && turnID == "" && itemID == "" && len(clonedRoots) == 0 {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, mcpElicitationContextKey{}, &mcpElicitationContextValue{ThreadID: threadID, TurnID: turnID, ItemID: itemID, Roots: clonedRoots})
}

func mcpElicitationContextFromContext(ctx context.Context) (string, string) {
	threadID, turnID, _ := mcpClientContextFromContext(ctx)
	return threadID, turnID
}

func mcpClientContextFromContext(ctx context.Context) (string, string, string) {
	if ctx == nil {
		return "", "", ""
	}
	value, ok := ctx.Value(mcpElicitationContextKey{}).(*mcpElicitationContextValue)
	if !ok || value == nil {
		return "", "", ""
	}
	return value.ThreadID, value.TurnID, value.ItemID
}

func mcpRootsFromContext(ctx context.Context) []MCPRoot {
	if ctx == nil {
		return []MCPRoot{}
	}
	value, ok := ctx.Value(mcpElicitationContextKey{}).(*mcpElicitationContextValue)
	if !ok || value == nil {
		return []MCPRoot{}
	}
	roots := cloneMCPRoots(value.Roots)
	if roots == nil {
		return []MCPRoot{}
	}
	return roots
}

func normalizeMCPElicitationResponse(response *MCPElicitationResponse) *MCPElicitationResponse {
	if response == nil {
		return &MCPElicitationResponse{Action: MCPElicitationActionCancel}
	}
	out := &MCPElicitationResponse{
		Action:  response.Action,
		Content: cloneJSONValue(response.Content),
		Meta:    cloneJSONValue(response.Meta),
	}
	switch out.Action {
	case MCPElicitationActionAccept, MCPElicitationActionDecline, MCPElicitationActionCancel:
	default:
		out.Action = MCPElicitationActionCancel
	}
	return out
}
