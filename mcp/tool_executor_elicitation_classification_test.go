package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/tool"
)

// Mirrors Rust #45716: a host-owned apps call whose raw result carries a trusted
// connector auth failure is reported before any elicitation can rewrite it, and
// an ordinary result is not reported at all.
func TestMCPToolExecutorReportsConnectorAuthFailuresLikeRust(t *testing.T) {
	var reported []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode MCP request error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-auth-failure")
			writeAuthFailureMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": RuntimeCodexAppsMCPServerName, "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeAuthFailureMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "list_events"}}})
		case "tools/call":
			var params map[string]any
			_ = json.Unmarshal(request.Params, &params)
			if toolName, _ := params["name"].(string); toolName == "list_events" {
				writeAuthFailureMCPResponse(t, w, request.ID, map[string]any{
					"content": []map[string]string{{"type": "text", "text": "Connector reauthentication required"}},
					"isError": true,
					"_meta": map[string]any{
						MCPToolCodexAppsMetaKey: map[string]any{
							connectorAuthFailureMetaKey: map[string]any{
								connectorAuthFailureIsAuthFailureKey: true,
								connectorAuthFailureAuthReasonKey:    "reauthentication_required",
								connectorAuthFailureConnectorIDKey:   "connector_calendar",
								"connector_name":                     "Calendar",
								connectorAuthFailureLinkIDKey:        "link_123",
								connectorAuthFailureErrorCodeKey:     "UNAUTHORIZED",
								connectorAuthFailureErrorHTTPStatus:  float64(401),
								connectorAuthFailureErrorActionKey:   "TRIGGER_REAUTHENTICATION",
							},
						},
					},
				})
				return
			}
			writeAuthFailureMCPResponse(t, w, request.ID, map[string]any{
				"content": []map[string]string{{"type": "text", "text": "ok"}},
				"isError": false,
			})
		default:
			writeAuthFailureMCPResponse(t, w, request.ID, map[string]any{})
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		RuntimeCodexAppsMCPServerName: {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	executor := NewToolExecutor(&ToolExecutorOptions{
		Service:       service,
		ServerName:    RuntimeCodexAppsMCPServerName,
		ConnectorID:   "connector_calendar",
		ConnectorName: "Calendar",
		ToolInfo:      &MCPToolInfo{Name: "list_events"},
		ConnectorAuthFailureObserver: func(callID string) {
			reported = append(reported, callID)
		},
	})
	if _, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-auth-failure",
		ToolName: tool.NamespacedName(RuntimeCodexAppsMCPServerName, "list_events"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(reported) != 1 || reported[0] != "call-auth-failure" {
		t.Fatalf("reported auth failures = %#v", reported)
	}

	// An ordinary result reports nothing.
	okExecutor := NewToolExecutor(&ToolExecutorOptions{
		Service:       service,
		ServerName:    RuntimeCodexAppsMCPServerName,
		ConnectorID:   "connector_calendar",
		ConnectorName: "Calendar",
		ToolInfo:      &MCPToolInfo{Name: "search_events"},
		ConnectorAuthFailureObserver: func(callID string) {
			reported = append(reported, callID)
		},
	})
	if _, err := okExecutor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-plain",
		ToolName: tool.NamespacedName(RuntimeCodexAppsMCPServerName, "search_events"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(reported) != 1 {
		t.Fatalf("an ordinary result must not be reported: %#v", reported)
	}
}

func writeAuthFailureMCPResponse(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Errorf("Encode() error = %v", err)
	}
}
