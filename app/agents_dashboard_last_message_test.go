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
	"codex_go/session"
)

// TestPreviewAgentMessageMatchesRust covers Rust preview_agent_message:
// markdown fences are unwrapped before the bounded preview, while ordinary code
// fences are preserved.
func TestPreviewAgentMessageMatchesRust(t *testing.T) {
	table := "```markdown\n| A | B |\n|---|---|\n| 1 | 2 |\n```"
	got := previewAgentMessage(table)
	if strings.Contains(got, "```") || !strings.Contains(got, "| 1 | 2 |") {
		t.Fatalf("previewAgentMessage(table) = %q", got)
	}
	code := "```go\nfunc main() {}\n```"
	if got := previewAgentMessage(code); !strings.Contains(got, "```go") {
		t.Fatalf("ordinary code fence was altered: %q", got)
	}
	if got := previewAgentMessage("   "); got != "" {
		t.Fatalf("blank preview = %q, want empty", got)
	}
	long := strings.Repeat("x", 600)
	if got := previewAgentMessage(long); len([]rune(got)) != 512 {
		t.Fatalf("long preview length = %d, want 512", len([]rune(got)))
	}
}

// TestLastAgentMessagePreviewPicksTheLastAgentMessage covers the item scan:
// the newest agent message wins and non-agent items are ignored.
func TestLastAgentMessagePreviewPicksTheLastAgentMessage(t *testing.T) {
	items := []appserver.ThreadItem{
		{ID: "user-1", Type: "userMessage", Text: "do the thing"},
		{ID: "agent-1", Type: "agentMessage", Text: "first answer"},
		{ID: "cmd-1", Type: "commandExecution", Text: "ls"},
		{ID: "agent-2", Type: "agentMessage", Text: "second answer"},
	}
	if got := lastAgentMessagePreview(items); got != "second answer" {
		t.Fatalf("last agent message = %q", got)
	}
	if got := lastAgentMessagePreview(nil); got != "" {
		t.Fatalf("empty items = %q", got)
	}
	sessionItems := []session.Item{
		{ID: "agent-1", Type: "agentMessage", Text: "from the store"},
		{ID: "user-1", Type: "userMessage", Text: "prompt"},
	}
	if got := lastAgentMessagePreviewFromSessionItems(sessionItems); got != "from the store" {
		t.Fatalf("session last agent message = %q", got)
	}
}

// TestRemoteAgentsDashboardReadsLastMessage covers Rust #44752's dashboard
// refresh: each task's details pane is filled from its latest turn.
func TestRemoteAgentsDashboardReadsLastMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 8)
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
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadLoadedList):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"data": []string{"thread-1"}, "nextCursor": nil}})
			case string(appserver.MethodThreadRead):
				thread := remoteAgentTestThread("thread-1", "alpha", "", "", "idle", nil, nil)
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": thread}})
			case string(appserver.MethodThreadTurnsList):
				var params appserver.ThreadTurnsListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-1" || params.Limit == nil || *params.Limit != 1 {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("turns/list params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"data": []map[string]any{{
						"id": "turn-1",
						"items": []map[string]any{
							{"id": "user-1", "type": "userMessage", "text": "do the thing"},
							{"id": "agent-1", "type": "agentMessage", "text": "final answer"},
						},
					}},
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
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		t.Fatalf("open remote client: %v", err)
	}
	defer client.close()
	rows, err := newRemoteAgentsDashboardSource(client, "").List(ctx)
	if err != nil {
		t.Fatalf("dashboard list: %v", err)
	}
	if len(rows) != 1 || rows[0].ThreadID != "thread-1" {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].LastMessage != "final answer" {
		t.Fatalf("last message = %q, want the latest agent message", rows[0].LastMessage)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
