package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// TestInteractiveRemoteRecapGenerateHandlerRunsStructuredTurn covers the
// temporary structured recap turn: an isolated ephemeral thread, a
// schema-constrained turn, the collected assistant response, and detach.
func TestInteractiveRemoteRecapGenerateHandlerRunsStructuredTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	var threadStartParams appserver.ThreadStartParams
	var turnStartParams turn.TurnStartParams
	recapText := `{"recap":"Fixed the parser."}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodConfigRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"config": map[string]any{"mcp_servers": map[string]any{"server-a": map[string]any{}}}},
				})
			case string(appserver.MethodThreadStart):
				if err := json.Unmarshal(req.Params, &threadStartParams); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"thread": map[string]any{"id": "thread-temp", "cwd": "/repo"}},
				})
			case string(appserver.MethodTurnStart):
				if err := json.Unmarshal(req.Params, &turnStartParams); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-1", "items": []any{}, "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationItemCompleted),
					"params": map[string]any{
						"threadId": "thread-temp",
						"turnId":   "turn-1",
						"item":     map[string]any{"id": "a1", "type": "agentMessage", "text": recapText},
					},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnCompleted),
					"params": map[string]any{
						"threadId": "thread-temp",
						"turn":     map[string]any{"id": "turn-1", "items": []any{}, "status": "completed"},
					},
				})
			case string(appserver.MethodThreadUnsubscribe):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	handler := interactiveRemoteRecapGenerateHandler(ctx, endpoint)
	response, err := handler("thread-1", codextea.RecapThreadOptions{Model: "gpt-test", ModelProvider: "openai", CWD: "/repo"},
		"recap prompt", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("recap error = %v", err)
	}
	if response != recapText {
		t.Fatalf("response = %q, want %q", response, recapText)
	}

	// The temporary thread is isolated: ephemeral, no approvals, read-only, and
	// every tool/feature/MCP server disabled.
	if !threadStartParams.Ephemeral {
		t.Fatal("temporary structured thread must be ephemeral")
	}
	if threadStartParams.ApprovalPolicy != "never" {
		t.Fatalf("approval policy = %#v, want never", threadStartParams.ApprovalPolicy)
	}
	if threadStartParams.Sandbox != "read-only" {
		t.Fatalf("sandbox = %#v, want read-only", threadStartParams.Sandbox)
	}
	if threadStartParams.Config["features.shell_tool"] != false || threadStartParams.Config["web_search"] != "disabled" {
		t.Fatalf("config overrides = %#v", threadStartParams.Config)
	}
	servers, ok := threadStartParams.Config["mcp_servers"].(map[string]any)
	if !ok || servers["server-a"] == nil {
		t.Fatalf("mcp servers = %#v", threadStartParams.Config["mcp_servers"])
	}
	if !strings.Contains(string(mustJSON(t, servers["server-a"])), "false") {
		t.Fatalf("mcp server config = %#v", servers["server-a"])
	}
	if turnStartParams.OutputSchema == nil {
		t.Fatal("the structured turn must carry an output schema")
	}
	if len(turnStartParams.Input) != 1 || turnStartParams.Input[0].Type != "text" || turnStartParams.Input[0].Text != "recap prompt" {
		t.Fatalf("turn input = %#v", turnStartParams.Input)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
