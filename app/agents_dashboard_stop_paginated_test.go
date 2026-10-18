package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/turn"
)

type stopPaginatedScenario struct {
	name            string
	historyMode     appserver.ThreadHistoryMode
	embeddedTurns   []any
	pageTurns       []any
	turnsListFailed bool
	wantTurnID      string
}

// Rust #40787: stopping a task reads the thread without turns first, uses
// thread/turns/list for paginated history, and only falls back to embedded
// turns for legacy threads or servers that reject turns/list.
func TestRemoteAgentsStopRespectsPaginatedHistoryLikeRust(t *testing.T) {
	inProgress := func(id string) map[string]any {
		return map[string]any{"id": id, "status": "inProgress", "items": []any{}}
	}
	tests := []stopPaginatedScenario{
		{
			name:        "paginated",
			historyMode: appserver.ThreadHistoryPaginated,
			pageTurns:   []any{inProgress("turn-page")},
			wantTurnID:  "turn-page",
		},
		{
			name:          "legacy embedded turns",
			historyMode:   appserver.ThreadHistoryLegacy,
			embeddedTurns: []any{inProgress("turn-legacy")},
			wantTurnID:    "turn-legacy",
		},
		{
			name:            "paginated server fallback",
			historyMode:     appserver.ThreadHistoryPaginated,
			turnsListFailed: true,
			embeddedTurns:   []any{inProgress("turn-fallback")},
			wantTurnID:      "turn-fallback",
		},
		{
			name:        "paginated idle",
			historyMode: appserver.ThreadHistoryPaginated,
			pageTurns:   []any{map[string]any{"id": "turn-done", "status": "completed", "items": []any{}}},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			requests := runRemoteAgentsStopScenario(t, testCase)
			reads := 0
			sawTurnsList := false
			var interrupt turn.TurnInterruptParams
			sawInterrupt := false
			for _, request := range requests {
				switch request.Method {
				case string(appserver.MethodThreadRead):
					reads++
				case string(appserver.MethodThreadTurnsList):
					sawTurnsList = true
				case string(appserver.MethodTurnInterrupt):
					if err := json.Unmarshal(request.Params, &interrupt); err != nil {
						t.Fatalf("decode turn/interrupt: %v", err)
					}
					sawInterrupt = true
				}
			}
			if testCase.historyMode == appserver.ThreadHistoryPaginated && !sawTurnsList {
				t.Fatal("paginated stop did not call thread/turns/list")
			}
			if testCase.wantTurnID == "" {
				if sawInterrupt {
					t.Fatalf("idle thread interrupted %#v", interrupt)
				}
				return
			}
			if !sawInterrupt || interrupt.ThreadID != "thread-1" || interrupt.TurnID != testCase.wantTurnID {
				t.Fatalf("interrupt params = %#v, want thread-1/%s (reads=%d)", interrupt, testCase.wantTurnID, reads)
			}
			wantReads := 2
			if testCase.historyMode == appserver.ThreadHistoryPaginated && !testCase.turnsListFailed {
				wantReads = 1
			}
			if reads != wantReads {
				t.Fatalf("thread/read calls = %d, want %d", reads, wantReads)
			}
		})
	}
}

func runRemoteAgentsStopScenario(t *testing.T, scenario stopPaginatedScenario) []remoteTUITestRequest {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var requests []remoteTUITestRequest
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
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
					websocket.CloseStatus(err) == websocket.StatusGoingAway ||
					errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			mu.Lock()
			requests = append(requests, req)
			mu.Unlock()
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				thread := remoteSessionTestThread("thread-1", "alpha", false, 0)
				thread["historyMode"] = string(scenario.historyMode)
				if params.IncludeTurns {
					thread["turns"] = scenario.embeddedTurns
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": thread}})
			case string(appserver.MethodThreadTurnsList):
				if scenario.turnsListFailed {
					remoteTUITestWrite(ctx, conn, map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error": map[string]any{
							"code":    appserver.JSONRPCInvalidRequestErrorCode,
							"message": "method not found",
						},
					})
					continue
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"data": scenario.pageTurns, "nextCursor": nil},
				})
			case string(appserver.MethodTurnInterrupt):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			default:
				remoteTUITestSendErr(serverErrs, errors.New("unexpected method "+req.Method))
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
	if err := newRemoteAgentsDashboardSource(client, "").Stop(ctx, "thread-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case serverErr := <-serverErrs:
		t.Fatalf("server error: %v", serverErr)
	default:
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]remoteTUITestRequest(nil), requests...)
}
