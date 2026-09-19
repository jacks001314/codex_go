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
			case string(appserver.MethodThreadList):
				// The recent-session seed (#46579) runs alongside loaded sessions.
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"data": []any{}, "nextCursor": nil}})
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

// TestRemoteAgentsDashboardSeedsRecentSessionsLikeRust covers Rust #46579: the
// command center seeds up to RECENT_SESSION_LIMIT recent sessions (interactive
// plus exec/app-server sources) in addition to loaded sessions, skips ephemeral
// and child threads, and falls back to updated_at when a server rejects the
// recency_at sort key.
func TestRemoteAgentsDashboardSeedsRecentSessionsLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)
	// Each source-kind query starts at recency_at and falls back independently.
	recencyRejected := map[bool]bool{}
	childID := "thread-live-child"
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
			case string(appserver.MethodThreadLoadedList):
				// Only one loaded thread; the recent seed must add the rest.
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"data": []string{"thread-live"}, "nextCursor": nil}})
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-live" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected thread read %q", params.ThreadID))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": recentDashboardTestThread("thread-live", 500, false, nil),
				}})
			case string(appserver.MethodThreadList):
				var params appserver.ThreadListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				interactive := len(params.SourceKinds) == 0
				if params.SortKey == appserver.SortRecencyAt && !recencyRejected[interactive] {
					// An older server rejects recency_at; the seed retries with updated_at.
					recencyRejected[interactive] = true
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{
						"code":    appserver.JSONRPCInvalidParamsErrorCode,
						"message": "unknown sort key: recency_at",
					}})
					continue
				}
				if params.SortKey != appserver.SortUpdatedAt {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("sort key = %q", params.SortKey))
					return
				}
				if params.Limit == nil || *params.Limit != recentSessionLimit {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("recent limit = %#v", params.Limit))
					return
				}
				switch {
				case len(params.SourceKinds) == 0:
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
						"data": []any{
							recentDashboardTestThread("thread-recent-newest", 900, false, nil),
							recentDashboardTestThread("thread-ephemeral", 800, true, nil),
							recentDashboardTestThread(childID, 700, false, &childID),
						},
						"nextCursor": nil,
					}})
				case len(params.SourceKinds) == 2 && params.SourceKinds[0] == appserver.ThreadSourceKindExec && params.SourceKinds[1] == appserver.ThreadSourceKindAppServer:
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
						"data":       []any{recentDashboardTestThread("thread-exec", 600, false, nil)},
						"nextCursor": nil,
					}})
				default:
					remoteTUITestSendErr(serverErrs, fmt.Errorf("source kinds = %#v", params.SourceKinds))
					return
				}
			case string(appserver.MethodThreadTurnsList):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"data": []any{}, "nextCursor": nil}})
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
	got := map[string]bool{}
	for _, row := range rows {
		got[row.ThreadID] = true
	}
	for _, want := range []string{"thread-live", "thread-recent-newest", "thread-exec"} {
		if !got[want] {
			t.Fatalf("rows %#v missing %q", rows, want)
		}
	}
	if got["thread-ephemeral"] || got[childID] {
		t.Fatalf("recent seed kept an ephemeral or child thread: %#v", rows)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %#v, want the loaded thread plus two recent roots", rows)
	}
	// The recent seed is sorted by recency, so the newest root leads.
	if rows[0].ThreadID != "thread-recent-newest" {
		t.Fatalf("rows[0] = %q, want the newest recent root", rows[0].ThreadID)
	}
	if !recencyRejected[true] || !recencyRejected[false] {
		t.Fatal("the recency_at fallback was not exercised")
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// recentDashboardTestThread builds the thread/list metadata the recent-session
// seed reads, including the optional recency, ephemeral, and parent fields.
func recentDashboardTestThread(id string, recencyAt int64, ephemeral bool, parentID *string) map[string]any {
	thread := map[string]any{
		"id":           id,
		"sessionId":    id,
		"preview":      id,
		"ephemeral":    ephemeral,
		"recencyAt":    recencyAt,
		"updatedAt":    recencyAt,
		"createdAt":    recencyAt,
		"status":       map[string]any{"type": "idle"},
		"cwd":          "D:/workspace",
		"source":       "cli",
		"historyMode":  "paginated",
		"environments": []any{},
	}
	if parentID != nil {
		thread["parentThreadId"] = *parentID
	}
	return thread
}
