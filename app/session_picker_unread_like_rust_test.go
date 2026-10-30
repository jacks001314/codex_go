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
	"codex_go/cli"
	codextui "codex_go/tui"
)

func TestRemoteSessionPickerItemsCarryReadStatesLikeRust(t *testing.T) {
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
			case string(appserver.MethodThreadList):
				var params appserver.ThreadListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				archived := params.Archived != nil && *params.Archived
				result := map[string]any{"data": []any{}, "nextCursor": nil, "backwardsCursor": nil}
				if archived {
					result["data"] = []any{remoteSessionTestThread("thread-archived", "Archived", true, 0)}
					result["readStates"] = map[string]any{
						"thread-archived": map[string]any{
							"firstUnread": map[string]any{"type": "turn", "turnId": "turn-2"},
							"revision":    "revision-archived",
						},
					}
				} else {
					result["data"] = []any{remoteSessionTestThread("thread-active", "Active", false, 0)}
					result["readStates"] = map[string]any{
						"thread-active": map[string]any{
							"firstUnread": map[string]any{"type": "turn", "turnId": "turn-1"},
							"revision":    "revision-active",
						},
						"thread-read": map[string]any{
							"firstUnread": nil,
							"revision":    "revision-read",
						},
					}
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	items, notice := interactiveRemoteSessionPickerItems(ctx, &cli.RootOptions{Shared: cli.SharedOptions{CWD: `D:\repo`}}, endpoint, codextui.NewBackendID())
	if notice != "" {
		t.Fatalf("session picker notice = %q, want empty", notice)
	}
	byID := map[string]codextui.SessionSummary{}
	for _, item := range items {
		byID[item.ThreadID] = item
	}
	if got := byID["thread-active"]; got.UnreadRevision != "revision-active" || !got.Unread() {
		t.Fatalf("active item = %#v", got)
	}
	if got := byID["thread-archived"]; got.UnreadRevision != "revision-archived" || !got.Unread() {
		t.Fatalf("archived item = %#v", got)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestSessionTranscriptAcknowledgeReturnsBackendReceiptLikeRust(t *testing.T) {
	unread, revision := threadReadReceiptFromReadState(appserver.ThreadReadState{
		FirstUnread: &appserver.ThreadUnreadPosition{Type: "turn", TurnID: "turn-1"},
		Revision:    " revision-2 ",
	})
	if !unread || revision != "revision-2" {
		t.Fatalf("unread receipt = (%v, %q)", unread, revision)
	}
	unread, revision = threadReadReceiptFromReadState(appserver.ThreadReadState{Revision: "revision-3"})
	if unread || revision != "" {
		t.Fatalf("read receipt = (%v, %q)", unread, revision)
	}
}
