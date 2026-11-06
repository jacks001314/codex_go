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
	codextui "codex_go/tui"
)

// TestRemoteOlderHistoryPagesLikeRust covers the daemon-owned carrier for Rust
// app/history_pagination.rs: the read-only summary snapshot asks for the turns
// before its cursor and replays them in chronological order.
func TestRemoteOlderHistoryPagesLikeRust(t *testing.T) {
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
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway || errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadTurnsList):
				var params appserver.ThreadTurnsListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-1" || params.Cursor == nil || *params.Cursor != "cursor-a" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("turns/list params = %#v", params))
					return
				}
				if params.SortDirection != appserver.SortDesc || params.ItemsView != appserver.TurnItemsSummary {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("turns/list ordering = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"data": []map[string]any{
						{
							"id": "turn-2",
							"items": []map[string]any{
								{"id": "user-2", "type": "userMessage", "text": "prompt-2"},
								{"id": "agent-2", "type": "agentMessage", "text": "answer-2"},
							},
						},
						{
							"id": "turn-1",
							"items": []map[string]any{
								{"id": "user-1", "type": "userMessage", "text": "prompt-1"},
								{"id": "agent-1", "type": "agentMessage", "text": "answer-1"},
							},
						},
					},
					"nextCursor": nil,
				}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	page, err := interactiveRemoteOlderHistoryLoader(ctx, endpoint)("thread-1", "cursor-a")
	if err != nil {
		t.Fatalf("older history loader returned error: %v", err)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
	if page.HasOlder || page.Cursor != "" {
		t.Fatalf("page hasOlder=%v cursor=%q, want the beginning", page.HasOlder, page.Cursor)
	}
	var got []string
	for _, message := range page.Messages {
		got = append(got, fmt.Sprintf("%v:%s", message.Role, message.Text))
	}
	want := []string{
		fmt.Sprintf("%v:prompt-1", codextui.RoleUser),
		fmt.Sprintf("%v:answer-1", codextui.RoleAssistant),
		fmt.Sprintf("%v:prompt-2", codextui.RoleUser),
		fmt.Sprintf("%v:answer-2", codextui.RoleAssistant),
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("older messages = %v, want %v", got, want)
	}
}
