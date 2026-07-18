package mcp

import "encoding/json"

type McpToolCallStatus string

const (
	McpToolCallInProgress McpToolCallStatus = "inProgress"
	McpToolCallCompleted  McpToolCallStatus = "completed"
	McpToolCallFailed     McpToolCallStatus = "failed"
)

type McpToolCallAppContext struct {
	ConnectorID string  `json:"connectorId"`
	LinkID      *string `json:"linkId"`
	ResourceURI *string `json:"resourceUri"`
	AppName     *string `json:"appName"`
	ActionName  *string `json:"actionName"`
}

type McpToolCallError struct {
	Message string `json:"message"`
}

type McpToolCallResult struct {
	Content           []any `json:"content"`
	StructuredContent any   `json:"structuredContent"`
	Meta              any   `json:"_meta"`
}

func (r *McpToolCallResult) MarshalJSON() ([]byte, error) {
	content := append([]any(nil), r.Content...)
	if content == nil {
		content = []any{}
	}
	return json.Marshal(struct {
		Content           []any `json:"content"`
		StructuredContent any   `json:"structuredContent"`
		Meta              any   `json:"_meta"`
	}{
		Content:           content,
		StructuredContent: r.StructuredContent,
		Meta:              r.Meta,
	})
}
