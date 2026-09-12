package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	tuiapp "codex_go/tui/app"
	"codex_go/tui/chatwidget"
)

// TestInteractiveRemotePromptEditHandlerForksAndAttaches covers the backtrack
// prompt-edit round trip against the app server: resolve the selected transcript
// ordinal to a turn, fork before it, then attach to the branched thread.
func TestInteractiveRemotePromptEditHandlerForksAndAttaches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)

	turns := func() []any {
		return []any{
			map[string]any{"id": "turn-1", "status": "completed", "items": []any{
				map[string]any{"id": "u1", "type": "userMessage", "text": "first"},
				map[string]any{"id": "a1", "type": "agentMessage", "text": "one"},
			}},
			map[string]any{"id": "turn-2", "status": "completed", "items": []any{
				map[string]any{"id": "u2", "type": "userMessage", "text": "second"},
			}},
		}
	}

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
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				threadTurns := turns()
				if params.ThreadID == "thread-forked" {
					threadTurns = turns()[:1]
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"thread": map[string]any{
						"id":    params.ThreadID,
						"cwd":   "/repo",
						"turns": threadTurns,
					}},
				})
			case string(appserver.MethodThreadFork):
				var params appserver.ThreadForkParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-source" || params.BeforeTurnID != "turn-2" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/fork params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"thread": map[string]any{
						"id":    "thread-forked",
						"cwd":   "/repo",
						"turns": turns()[:1],
					}},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	handler := interactiveRemotePromptEditHandler(ctx, endpoint, nil, nil)
	response, err := handler(tuiapp.PromptEditSelection{
		ThreadID:    "thread-source",
		UserOrdinal: 1,
		Prompt:      chatwidget.ThreadComposerState{Text: "second"},
	})
	if err != nil {
		t.Fatalf("prompt edit error = %v", err)
	}
	if response.Summary == nil || response.Summary.ThreadID != "thread-forked" {
		t.Fatalf("summary = %#v", response.Summary)
	}
	if len(response.Messages) == 0 {
		t.Fatalf("branched thread messages = %#v, want the pre-edit history", response.Messages)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestInteractiveRemotePromptEditHandlerRejectsSteer covers the Rust guard that a
// steered prompt cannot be branched independently.
func TestInteractiveRemotePromptEditHandlerRejectsSteer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)

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
			case string(appserver.MethodThreadRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"thread": map[string]any{
						"id": "thread-source",
						"turns": []any{map[string]any{
							"id": "turn-1", "status": "completed",
							"items": []any{
								map[string]any{"id": "u1", "type": "userMessage", "text": "initial"},
								map[string]any{"id": "u2", "type": "userMessage", "text": "steer"},
							},
						}},
					}},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	handler := interactiveRemotePromptEditHandler(ctx, endpoint, nil, nil)
	if _, err := handler(tuiapp.PromptEditSelection{ThreadID: "thread-source", UserOrdinal: 1}); err == nil {
		t.Fatal("a steered prompt should not be branchable")
	} else if !strings.Contains(err.Error(), "steer") {
		t.Fatalf("error = %v, want the steer rejection", err)
	}
	select {
	case err := <-serverErrs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("server error: %v", err)
		}
	default:
	}
}
