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

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	agentsoverview "codex_go/tui/agents_overview"
	codextea "codex_go/tui/tea"
)

func TestAgentsOverviewRowsApplyReadReceiptsLikeRust(t *testing.T) {
	rows := agentsOverviewRowsFromThreadsWithReadReceipts(dashboardThreadRows(), "root-1", map[string]agentsOverviewReadReceipt{
		"root-1": {Unread: true, Revision: "revision-1"},
		"root-3": {Unread: false, Revision: "revision-3"},
	})
	byID := map[string]agentsoverview.Row{}
	for _, row := range rows {
		byID[row.ThreadID] = row
	}
	if got := byID["root-1"]; !got.Unread || got.UnreadRevision != "revision-1" {
		t.Fatalf("unread row = %#v", got)
	}
	if got := byID["root-3"]; got.Unread || got.UnreadRevision != "revision-3" {
		t.Fatalf("read row = %#v", got)
	}
}

func TestRemoteAgentsDashboardReadStatesLikeRust(t *testing.T) {
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
			case string(appserver.MethodThreadLoadedList):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"data": []string{"thread-loaded"}, "nextCursor": nil,
				}})
			case string(appserver.MethodThreadList):
				var params appserver.ThreadListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.SectionID.Set || len(params.SourceKinds) > 0 {
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
						"data": []any{}, "nextCursor": nil,
					}})
					continue
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"data": []any{remoteAgentTestThread("thread-recent", "", "", "", "idle", nil, nil)},
					"readStates": map[string]any{
						"thread-recent": map[string]any{
							"firstUnread": map[string]any{"type": "turn", "turnId": "turn-9"},
							"revision":    "revision-recent",
						},
					},
					"nextCursor": nil,
				}})
			case string(appserver.MethodThreadRead):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"thread": remoteAgentTestThread("thread-loaded", "", "", "", "idle", nil, nil),
					"readState": map[string]any{
						"firstUnread": map[string]any{"type": "threadStart"},
						"revision":    "revision-loaded",
					},
				}})
			case string(appserver.MethodThreadTurnsList):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
					"data": []any{}, "nextCursor": nil,
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
	byID := map[string]agentsoverview.Row{}
	for _, row := range rows {
		byID[row.ThreadID] = row
	}
	if got := byID["thread-recent"]; !got.Unread || got.UnreadRevision != "revision-recent" {
		t.Fatalf("recent read state = %#v", got)
	}
	if got := byID["thread-loaded"]; !got.Unread || got.UnreadRevision != "revision-loaded" {
		t.Fatalf("loaded read state = %#v", got)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestThreadReadStateNotificationLikeRust(t *testing.T) {
	messages := make(chan bubbletea.Msg, 1)
	client := &remoteAppServerTUIClient{messages: messages}
	params, err := json.Marshal(appserver.ThreadReadStateChangedNotification{
		ThreadID: "thread-1",
		ReadState: appserver.ThreadReadState{
			FirstUnread: &appserver.ThreadUnreadPosition{Type: "turn", TurnID: "turn-1"},
			Revision:    "revision-1",
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadReadStateChanged), Params: params,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	message, ok := (<-messages).(codextea.AgentThreadReadStateMsg)
	if !ok || message.ThreadID != "thread-1" || !message.Unread || message.Revision != "revision-1" {
		t.Fatalf("notification message = %#v", message)
	}
}

func TestRemoteReadStateConflictLikeRust(t *testing.T) {
	_, ok := remoteReadStateConflict(&remoteRPCError{
		Code: -32602,
		Data: map[string]any{
			"reason": "readStateConflict",
			"readState": map[string]any{
				"firstUnread": map[string]any{"type": "threadStart"},
				"revision":    "newer",
			},
		},
	})
	if !ok {
		t.Fatal("readStateConflict error was not recognized")
	}
	if _, ok := remoteReadStateConflict(errors.New("other")); ok {
		t.Fatal("plain error was treated as a read state conflict")
	}
}
