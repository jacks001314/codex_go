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

	bubbletea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

type remoteApprovalTestFrame struct {
	ID     any                      `json:"id"`
	Method string                   `json:"method"`
	Params json.RawMessage          `json:"params"`
	Result json.RawMessage          `json:"result"`
	Error  *appserver.ResponseError `json:"error"`
}

// TestRemoteTurnDeliversQueuedApprovalsWithoutBlockingReadLoop pins Rust's
// app-server request contract: a request that waits on a human is recorded and
// answered later, so the notification stream keeps flowing while it is
// outstanding. Before that shape, the TUI read loop blocked inside the first
// approval and a second request never reached the operator, which surfaced as
// "the approval menu never appeared".
func TestRemoteTurnDeliversQueuedApprovalsWithoutBlockingReadLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	responses := make(chan remoteApprovalResponse, 2)
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		answered := 0
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
					websocket.CloseStatus(err) == websocket.StatusGoingAway ||
					websocket.CloseStatus(err) == websocket.StatusAbnormalClosure {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var frame remoteApprovalTestFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			if strings.TrimSpace(frame.Method) == "" {
				answered++
				responses <- remoteApprovalResponse{ID: fmt.Sprint(frame.ID), Result: frame.Result, Error: frame.Error}
				if answered == 2 {
					remoteTUITestWrite(ctx, conn, map[string]any{
						"jsonrpc": "2.0",
						"method":  string(appserver.NotificationTurnCompleted),
						"params": map[string]any{
							"threadId": "thread-approvals",
							"turn":     map[string]any{"id": "turn-approvals", "items": []any{}, "status": "completed"},
						},
					})
				}
				continue
			}
			req := remoteTUITestRequest{ID: frame.ID, Method: frame.Method, Params: frame.Params}
			if remoteTUITestHandleConfigRequest(ctx, conn, req) {
				continue
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"thread": map[string]any{"id": "thread-approvals"}},
				})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-approvals", "items": []any{}, "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      "approval-1",
					"method":  string(appserver.ServerRequestCommandExecutionApproval),
					"params": map[string]any{
						"threadId":    "thread-approvals",
						"turnId":      "turn-approvals",
						"itemId":      "exec-1",
						"startedAtMs": 1,
						"command":     "go build ./...",
						"cwd":         "D:\\repo",
					},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      "approval-2",
					"method":  string(appserver.ServerRequestCommandExecutionApproval),
					"params": map[string]any{
						"threadId":                    "thread-approvals",
						"turnId":                      "turn-approvals",
						"itemId":                      "exec-2",
						"startedAtMs":                 2,
						"command":                     "go test ./...",
						"cwd":                         "D:\\repo",
						"proposedExecpolicyAmendment": []string{"go", "test"},
					},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	state := codextui.NewState(nil)
	messages := make(chan bubbletea.Msg, 64)
	brokers := newRemoteTUIBrokers()
	done := make(chan struct{})
	go func() {
		runInteractiveRemoteTurn(ctx, &cli.RootOptions{}, endpoint, state, codextea.SubmitRequest{Prompt: "run two commands"}, messages, brokers, nil)
		close(done)
	}()

	first := remoteTUITestReadApprovalMessage(t, messages)
	// The second approval must reach the TUI while the first is unanswered.
	second := remoteTUITestReadApprovalMessage(t, messages)
	requests := map[string]codextea.ApprovalRequestMsg{
		first.ServerRequestID:  first,
		second.ServerRequestID: second,
	}
	if len(requests) != 2 || requests["approval-1"].ID == "" || requests["approval-2"].ID == "" {
		t.Fatalf("approvals = %#v/%#v", first, second)
	}

	// Answer out of order: Rust resolves each request from its pending table.
	brokers.respond(codextea.ModalResponse{ID: requests["approval-2"].ID, Kind: codextea.ModalKindApproval, OptionID: "allow_prefix"})
	brokers.respond(codextea.ModalResponse{ID: requests["approval-1"].ID, Kind: codextea.ModalKindApproval, OptionID: "allow_once"})

	decisions := map[string]string{}
	for range 2 {
		response := <-responses
		if response.Error != nil {
			t.Fatalf("approval %s error = %#v", response.ID, response.Error)
		}
		var result appserver.CommandExecutionRequestApprovalResponse
		if err := json.Unmarshal(response.Result, &result); err != nil {
			t.Fatalf("decode approval %s: %v", response.ID, err)
		}
		encoded, err := json.Marshal(result.Decision)
		if err != nil {
			t.Fatal(err)
		}
		decisions[response.ID] = string(encoded)
	}
	if got, want := decisions["approval-1"], `"accept"`; got != want {
		t.Fatalf("approval-1 decision = %s, want %s", got, want)
	}
	// ExecPolicyAmendment is #[serde(transparent)] over Vec<String> in Rust.
	if got, want := decisions["approval-2"], `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["go","test"]}}`; got != want {
		t.Fatalf("approval-2 decision = %s, want %s", got, want)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the remote approval turn")
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

type remoteApprovalResponse struct {
	ID     string
	Result json.RawMessage
	Error  *appserver.ResponseError
}
