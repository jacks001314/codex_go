package mcp

import (
	"strings"
)

// MCPToolCodexAppsMetaKey is the top-level meta key used for Codex Apps signaling.
const MCPToolCodexAppsMetaKey = "_codex_apps"

const (
	connectorAuthFailureMetaKey          = "connector_auth_failure"
	connectorAuthFailureIsAuthFailureKey = "is_auth_failure"
	connectorAuthFailureAuthReasonKey    = "auth_reason"
	connectorAuthFailureConnectorIDKey   = "connector_id"
	connectorAuthFailureLinkIDKey        = "link_id"
	connectorAuthFailureErrorCodeKey     = "error_code"
	connectorAuthFailureErrorHTTPStatus  = "error_http_status_code"
	connectorAuthFailureErrorActionKey   = "error_action"
)

// CodexAppsConnectorAuthFailure mirrors Rust's equivalent type.
type CodexAppsConnectorAuthFailure struct {
	ConnectorID         string `json:"connector_id"`
	ConnectorName       string `json:"connector_name"`
	InstallURL          string `json:"install_url"`
	AuthReason          string `json:"auth_reason,omitempty"`
	LinkID              string `json:"link_id,omitempty"`
	ErrorCode           string `json:"error_code,omitempty"`
	ErrorHTTPStatusCode *int64 `json:"error_http_status_code,omitempty"`
	ErrorAction         string `json:"error_action,omitempty"`
}

// CodexAppsAuthElicitation mirrors Rust's equivalent type.
type CodexAppsAuthElicitation struct {
	Meta          any    `json:"meta"`
	Message       string `json:"message"`
	URL           string `json:"url"`
	ElicitationID string `json:"elicitation_id"`
}

// CodexAppsAuthElicitationPlan mirrors Rust's equivalent type.
type CodexAppsAuthElicitationPlan struct {
	AuthFailure *CodexAppsConnectorAuthFailure `json:"auth_failure"`
	Elicitation *CodexAppsAuthElicitation      `json:"elicitation"`
}

// ConnectorAuthFailureFromToolResult parses a connector auth failure from an
// MCP tool call result's metadata. It mirrors Rust's
// connector_auth_failure_from_tool_result.
func ConnectorAuthFailureFromToolResult(result *MCPToolCallResponse, connectorID string, connectorName string, installURL string) *CodexAppsConnectorAuthFailure {
	if result == nil || result.IsError == nil || !*result.IsError {
		return nil
	}

	authFailure := extractConnectorAuthFailure(result.Meta)
	if authFailure == nil {
		return nil
	}

	if isAuthFailure, _ := authFailure[connectorAuthFailureIsAuthFailureKey].(bool); !isAuthFailure {
		return nil
	}

	connectorID = strings.TrimSpace(connectorID)
	if connectorID == "" {
		return nil
	}
	if authConnectorID := stringFromMetaMap(authFailure, connectorAuthFailureConnectorIDKey); authConnectorID != "" && authConnectorID != connectorID {
		return nil
	}

	connectorName = strings.TrimSpace(connectorName)
	if connectorName == "" {
		connectorName = connectorID
	}
	installURL = strings.TrimSpace(installURL)
	if installURL == "" {
		return nil
	}

	failure := &CodexAppsConnectorAuthFailure{
		ConnectorID:   connectorID,
		ConnectorName: connectorName,
		InstallURL:    installURL,
		AuthReason:    stringFromMetaMap(authFailure, connectorAuthFailureAuthReasonKey),
		LinkID:        stringFromMetaMap(authFailure, connectorAuthFailureLinkIDKey),
		ErrorCode:     stringFromMetaMap(authFailure, connectorAuthFailureErrorCodeKey),
		ErrorAction:   stringFromMetaMap(authFailure, connectorAuthFailureErrorActionKey),
	}

	if statusCode, ok := authFailure[connectorAuthFailureErrorHTTPStatus].(float64); ok {
		code := int64(statusCode)
		failure.ErrorHTTPStatusCode = &code
	}

	return failure
}

// BuildAuthElicitationPlan builds a CodexAppsAuthElicitationPlan from a tool
// call ID and result metadata. It mirrors Rust's build_auth_elicitation_plan.
func BuildAuthElicitationPlan(callID string, result *MCPToolCallResponse, connectorID string, connectorName string, installURL string) *CodexAppsAuthElicitationPlan {
	authFailure := ConnectorAuthFailureFromToolResult(result, connectorID, connectorName, installURL)
	if authFailure == nil {
		return nil
	}
	return &CodexAppsAuthElicitationPlan{
		AuthFailure: authFailure,
		Elicitation: BuildAuthElicitation(callID, authFailure),
	}
}

// BuildAuthElicitation builds an elicitation payload from an auth failure.
// It mirrors Rust's build_auth_elicitation.
func BuildAuthElicitation(callID string, authFailure *CodexAppsConnectorAuthFailure) *CodexAppsAuthElicitation {
	if authFailure == nil {
		return nil
	}
	meta := map[string]any{
		MCPToolCodexAppsMetaKey: map[string]any{
			connectorAuthFailureMetaKey: authFailureMetaMap(authFailure),
		},
	}
	return &CodexAppsAuthElicitation{
		Meta:          meta,
		Message:       authElicitationMessage(authFailure),
		URL:           authFailure.InstallURL,
		ElicitationID: AuthElicitationID(callID),
	}
}

// AuthElicitationID mirrors Rust's auth_elicitation_id.
func AuthElicitationID(callID string) string {
	return "codex_apps_auth_" + strings.TrimSpace(callID)
}

// AuthElicitationCompletedResult mirrors Rust's auth_elicitation_completed_result.
func AuthElicitationCompletedResult(authFailure *CodexAppsConnectorAuthFailure, meta any) *MCPToolCallResponse {
	isError := true
	content := []MCPToolCallContent{
		{
			Type: "text",
			Text: "Authentication for " + authFailure.ConnectorName + " was requested and accepted. Retry this tool call now.",
		},
	}
	return &MCPToolCallResponse{
		Content: content,
		IsError: &isError,
		Meta:    meta,
	}
}

func extractConnectorAuthFailure(meta any) map[string]any {
	if meta == nil {
		return nil
	}
	metaMap, ok := meta.(map[string]any)
	if !ok {
		return nil
	}
	codexApps, ok := metaMap[MCPToolCodexAppsMetaKey].(map[string]any)
	if !ok {
		return nil
	}
	authFailure, ok := codexApps[connectorAuthFailureMetaKey].(map[string]any)
	if !ok {
		return nil
	}
	return authFailure
}

func stringFromMetaMap(m map[string]any, key string) string {
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func authFailureMetaMap(authFailure *CodexAppsConnectorAuthFailure) map[string]any {
	out := map[string]any{
		connectorAuthFailureIsAuthFailureKey: true,
		connectorAuthFailureConnectorIDKey:   authFailure.ConnectorID,
		"connector_name":                     authFailure.ConnectorName,
		"install_url":                        authFailure.InstallURL,
	}
	if authFailure.AuthReason != "" {
		out[connectorAuthFailureAuthReasonKey] = authFailure.AuthReason
	}
	if authFailure.LinkID != "" {
		out[connectorAuthFailureLinkIDKey] = authFailure.LinkID
	}
	if authFailure.ErrorCode != "" {
		out[connectorAuthFailureErrorCodeKey] = authFailure.ErrorCode
	}
	if authFailure.ErrorHTTPStatusCode != nil {
		out[connectorAuthFailureErrorHTTPStatus] = *authFailure.ErrorHTTPStatusCode
	}
	if authFailure.ErrorAction != "" {
		out[connectorAuthFailureErrorActionKey] = authFailure.ErrorAction
	}
	return out
}

func authElicitationMessage(authFailure *CodexAppsConnectorAuthFailure) string {
	switch authFailure.AuthReason {
	case "oauth_upgrade_required":
		return "Reconnect " + authFailure.ConnectorName + " on ChatGPT to grant the permissions needed for this request."
	case "reauthentication_required":
		return "Reconnect " + authFailure.ConnectorName + " on ChatGPT to restore access for this request."
	case "missing_link":
		return "Sign in to " + authFailure.ConnectorName + " on ChatGPT to use it in Codex."
	default:
		return "Sign in to " + authFailure.ConnectorName + " on ChatGPT to continue."
	}
}

func authFailureResult() *MCPToolCallResponse {
	isError := true
	return &MCPToolCallResponse{
		Content: []MCPToolCallContent{{
			Type: "text",
			Text: "Connector reauthentication required",
		}},
		IsError: &isError,
		Meta: map[string]any{
			MCPToolCodexAppsMetaKey: map[string]any{
				connectorAuthFailureMetaKey: map[string]any{
					connectorAuthFailureIsAuthFailureKey: true,
					connectorAuthFailureAuthReasonKey:    "reauthentication_required",
					connectorAuthFailureConnectorIDKey:   "connector_calendar",
					"connector_name":                     "Untrusted Calendar",
					connectorAuthFailureLinkIDKey:        "link_123",
					connectorAuthFailureErrorCodeKey:     "UNAUTHORIZED",
					connectorAuthFailureErrorHTTPStatus:  float64(401),
					connectorAuthFailureErrorActionKey:   "TRIGGER_REAUTHENTICATION",
				},
			},
		},
	}
}

// (cloneJSONValue is defined in api.go)
