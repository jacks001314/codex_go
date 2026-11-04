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

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Manual compaction through the daemon must resume an idle thread the server
// has evicted before retrying thread/compact/start.
func TestRemoteCompactResumesEvictedThread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 8)
	serverErrs := make(chan error, 1)
	compacts := 0
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
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadCompactStart):
				var params appserver.ThreadCompactStartParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-compact" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("compact params = %#v", params))
					return
				}
				compacts++
				if compacts == 1 {
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{
						"code":    -32600,
						"message": "thread not found: thread-compact",
					}})
					continue
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				return
			case string(appserver.MethodThreadResume):
				var params appserver.ThreadResumeParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-compact" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("resume params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-compact"}}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-compact")
	messages := make(chan bubbletea.Msg, 64)
	go runInteractiveRemoteCompact(ctx, nil, endpoint, state, remoteTUIBrokers{}, "thread-compact", messages)

	var result *codextea.CompactStartResultMsg
	for message := range messages {
		if value, ok := message.(codextea.CompactStartResultMsg); ok {
			value := value
			result = &value
		}
	}
	if result == nil || result.Err != nil {
		t.Fatalf("compact result = %#v", result)
	}
	for index, want := range []appserver.Method{
		appserver.MethodInitialize,
		appserver.MethodThreadCompactStart,
		appserver.MethodThreadResume,
		appserver.MethodThreadCompactStart,
	} {
		if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(want) {
			t.Fatalf("request %d = %q, want %q", index, got.Method, want)
		}
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}
