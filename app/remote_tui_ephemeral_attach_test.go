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

	"codex_go/appserver"
	appserverdaemon "codex_go/appserverdaemon"
	"codex_go/cli"

	"github.com/coder/websocket"
)

// Mirrors Rust #47900: ephemeral agent threads are not yet available for
// replay or live attach.
func TestInteractiveRemoteSwitchAgentThreadRejectsEphemeralLikeRust(t *testing.T) {
	ctx := context.Background()
	requests := make(chan remoteTUITestRequest, 8)
	serverErrs := make(chan error, 4)
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
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				thread := remoteAgentTestThread(params.ThreadID, "Scout", "review", "subagent", "active", nil, nil)
				thread["ephemeral"] = true
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": thread}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	_, err := interactiveRemoteSwitchAgentThread(ctx, endpoint, &cli.RootOptions{Shared: cli.SharedOptions{CWD: remoteResumeTestCWD}}, "thread-worker")
	if err == nil || !strings.Contains(err.Error(), "not yet available for replay or live attach") {
		t.Fatalf("switch error = %v, want ephemeral rejection", err)
	}
	for len(requests) > 0 {
		if request := <-requests; request.Method == string(appserver.MethodThreadResume) {
			t.Fatal("ephemeral agent thread was resumed before rejection")
		}
	}
}
