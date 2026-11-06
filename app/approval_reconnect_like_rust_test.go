package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	codextea "codex_go/tui/tea"
)

// Rust #58720 (app/reconnect.rs:551 refresh_pending_thread_approvals) rebuilds
// the background approval set after a reconnect. The app-server is the source of
// truth: resuming a thread replays the requests it still holds
// (app-server/src/outgoing_message.rs:525
// replay_requests_to_connection_for_thread). This harness drives the real Go
// client against a fake app-server: a background approval is queued, the
// connection dies, the reconnect command resumes the owner thread, and the
// replayed request rebuilds the TUI approval and carries the answer back on the
// new connection.
func TestRemoteTUIReconnectRebuildsBackgroundApprovalLikeRust(t *testing.T) {
	t.Run("pending request is rebuilt and answered", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var connections int32
		resumes := make(chan string, 4)
		answers := make(chan map[string]any, 4)
		serverErrs := make(chan error, 8)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			if atomic.AddInt32(&connections, 1) == 1 {
				serveApprovalTurnConnection(ctx, conn, serverErrs)
				return
			}
			serveApprovalReconnectConnection(ctx, conn, serverErrs, resumes, answers, true)
		}))
		defer server.Close()

		pump := startApprovalReconnectModel(t, ctx, server)

		// 1. The live turn queues the background approval.
		pump.until(10*time.Second, func() bool { return len(pump.model.PendingApprovals()) == 1 })
		if labels := pump.model.PendingApprovalThreadLabels(); len(labels) != 1 || labels[0] != "Agent (1)" {
			t.Fatalf("queued labels = %#v, want the background thread", labels)
		}

		// 2. The transport dies: the dead queue is retired and the host is asked
		// to reattach the thread that was waiting.
		resumedThread := ""
		resumeSeen := false
		pump.until(10*time.Second, func() bool {
			if resumeSeen {
				return true
			}
			select {
			case resumedThread = <-resumes:
				resumeSeen = true
				return true
			default:
				return false
			}
		})
		if resumedThread != "thread-bg-1" {
			t.Fatalf("resumed thread = %q, want thread-bg-1", resumedThread)
		}

		// 3. The replayed request rebuilds the approval for its thread.
		pump.until(10*time.Second, func() bool { return len(pump.model.PendingApprovals()) == 1 })
		if labels := pump.model.PendingApprovalThreadLabels(); len(labels) != 1 || labels[0] != "Agent (1)" {
			t.Fatalf("rebuilt labels = %#v", labels)
		}
		pump.model.OpenPendingApprovalForThread("thread-bg-1")
		if !pump.model.ApprovalModalOpenFor("thread-bg-1") {
			t.Fatal("the rebuilt approval did not open for its thread")
		}

		// 4. The answer travels back over the reconnect connection, to the id the
		// server replayed.
		_, answerCmd := pump.model.Update(keymsg.KeyMsg{Type: keymsg.KeyEnter})
		if answerCmd != nil {
			_ = answerCmd()
		}
		answer := remoteTUITestReadMap(t, answers)
		if decision, _ := answer["decision"].(string); decision != string(appserver.CommandExecutionApprovalAccept) {
			t.Fatalf("replayed approval answer = %#v, want accept", answer)
		}
		deadline := time.Now().Add(2 * time.Second)
		for pump.brokers.approval.pendingCount() != 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := pump.brokers.approval.pendingCount(); got != 0 {
			t.Fatalf("broker still holds %d pending approvals", got)
		}
	})

	t.Run("resolved request is not rebuilt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var connections int32
		resumes := make(chan string, 4)
		answers := make(chan map[string]any, 4)
		serverErrs := make(chan error, 8)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			if atomic.AddInt32(&connections, 1) == 1 {
				serveApprovalTurnConnection(ctx, conn, serverErrs)
				return
			}
			serveApprovalReconnectConnection(ctx, conn, serverErrs, resumes, answers, false)
		}))
		defer server.Close()

		pump := startApprovalReconnectModel(t, ctx, server)
		pump.until(10*time.Second, func() bool { return len(pump.model.PendingApprovals()) == 1 })
		resumedThread := ""
		resumeSeen := false
		pump.until(10*time.Second, func() bool {
			if resumeSeen {
				return true
			}
			select {
			case resumedThread = <-resumes:
				resumeSeen = true
				return true
			default:
				return false
			}
		})
		if resumedThread != "thread-bg-1" {
			t.Fatalf("resumed thread = %q", resumedThread)
		}
		pump.forDuration(800 * time.Millisecond)
		if got := pump.model.PendingApprovals(); len(got) != 0 {
			t.Fatalf("the server resolved the request but the TUI rebuilt it: %#v", got)
		}
		if got := pump.model.PendingApprovalThreadLabels(); len(got) != 0 {
			t.Fatalf("resolved approval still has notice rows: %#v", got)
		}
		select {
		case answer := <-answers:
			t.Fatalf("the TUI answered a resolved request: %#v", answer)
		default:
		}
	})
}

func serveApprovalTurnConnection(ctx context.Context, conn *websocket.Conn, serverErrs chan<- error) {
	for {
		req, err := remoteTUITestReadRequest(ctx, conn)
		if err != nil {
			return
		}
		switch req.Method {
		case string(appserver.MethodInitialize):
			remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		case string(appserver.MethodTurnStart):
			remoteTUITestWrite(ctx, conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"turn": map[string]any{"id": "turn-active", "items": []any{}, "status": "inProgress"}},
			})
			// A subagent thread asks for a command approval on this connection
			// while another thread owns the screen (Rust #58720 background queue).
			remoteTUITestWrite(ctx, conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      "srv-approval-1",
				"method":  string(appserver.ServerRequestCommandExecutionApproval),
				"params": map[string]any{
					"threadId":    "thread-bg-1",
					"turnId":      "turn-bg-1",
					"itemId":      "item-bg-1",
					"command":     "rm -rf build",
					"startedAtMs": 1,
				},
			})
			time.Sleep(50 * time.Millisecond)
			// Drop the transport before the turn completes.
			return
		default:
			remoteTUITestSendErr(serverErrs, fmt.Errorf("turn connection: unexpected method %s", req.Method))
			return
		}
	}
}

func serveApprovalReconnectConnection(ctx context.Context, conn *websocket.Conn, serverErrs chan<- error, resumes chan<- string, answers chan<- map[string]any, replay bool) {
	for {
		message, err := readApprovalTestMessage(ctx, conn)
		if err != nil {
			return
		}
		if strings.TrimSpace(message.Method) == "" {
			if fmt.Sprint(message.ID) == "srv-approval-1" {
				var result map[string]any
				_ = json.Unmarshal(message.Result, &result)
				answers <- result
			}
			continue
		}
		switch message.Method {
		case string(appserver.MethodInitialize):
			remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{}})
		case string(appserver.MethodThreadResume):
			var params appserver.ThreadResumeParams
			if err := json.Unmarshal(message.Params, &params); err != nil {
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			resumes <- params.ThreadID
			remoteTUITestWrite(ctx, conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      message.ID,
				"result":  map[string]any{"thread": map[string]any{"id": params.ThreadID}},
			})
			if !replay {
				continue
			}
			// The server still holds the request and replays it to the resumed
			// connection with the original request id.
			remoteTUITestWrite(ctx, conn, map[string]any{
				"jsonrpc": "2.0",
				"id":      "srv-approval-1",
				"method":  string(appserver.ServerRequestCommandExecutionApproval),
				"params": map[string]any{
					"threadId":    "thread-bg-1",
					"turnId":      "turn-bg-1",
					"itemId":      "item-bg-1",
					"command":     "rm -rf build",
					"startedAtMs": 1,
				},
			})
		default:
			remoteTUITestSendErr(serverErrs, fmt.Errorf("reconnect connection: unexpected method %s", message.Method))
			return
		}
	}
}

type approvalTestMessage struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

func readApprovalTestMessage(ctx context.Context, conn *websocket.Conn) (approvalTestMessage, error) {
	var message approvalTestMessage
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return message, err
	}
	if typ != websocket.MessageText {
		return message, errors.New("non-text websocket message")
	}
	return message, json.Unmarshal(data, &message)
}

func remoteTUITestReadMap(t *testing.T, values <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the replayed approval answer")
		return nil
	}
}

// modelPump ties a Model to the real app-server streams and runs the commands it
// produces. Command results are funneled through one long-lived channel so a
// message that arrives after a stop condition is not lost for the next phase.
type modelPump struct {
	t        *testing.T
	model    *codextea.Model
	brokers  remoteTUIBrokers
	stream   chan bubbletea.Msg
	produced chan bubbletea.Msg
}

func startApprovalReconnectModel(t *testing.T, ctx context.Context, server *httptest.Server) *modelPump {
	t.Helper()
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	root := &cli.RootOptions{}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-active")
	brokers := newRemoteTUIBrokers()
	model := codextea.NewModel(state, codextea.Options{
		Width:  100,
		Height: 40,
		OnModalResponse: func(response codextea.ModalResponse) bubbletea.Cmd {
			brokers.respond(response)
			return nil
		},
		OnReconnectThreadApprovals: func(threadIDs []string) bubbletea.Cmd {
			return interactiveRemoteReconnectThreadApprovalsCommand(ctx, root, endpoint, brokers, threadIDs)
		},
	})
	messages := make(chan bubbletea.Msg, 64)
	go runInteractiveRemoteTurn(ctx, root, endpoint, state, codextea.SubmitRequest{Prompt: "hello"}, messages, brokers, nil)
	return &modelPump{t: t, model: model, brokers: brokers, stream: messages, produced: make(chan bubbletea.Msg, 256)}
}

func (p *modelPump) launch(cmd bubbletea.Cmd) {
	if cmd == nil {
		return
	}
	go func() { p.produced <- cmd() }()
}

func (p *modelPump) until(timeout time.Duration, stop func() bool) {
	start := time.Now()
	deadline := time.Now().Add(timeout)
	for !stop() && time.Now().Before(deadline) {
		var msg bubbletea.Msg
		select {
		case msg = <-p.produced:
			if msg == nil {
				continue
			}
			if batch, ok := msg.(bubbletea.BatchMsg); ok {
				for _, cmd := range batch {
					p.launch(cmd)
				}
				continue
			}
			if nested, ok := msg.(bubbletea.Cmd); ok {
				p.launch(nested)
				continue
			}
			_, next := p.model.Update(msg)
			p.launch(next)
		case msg = <-p.stream:
			if msg == nil {
				p.stream = nil
				continue
			}
			_, next := p.model.Update(msg)
			p.launch(next)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !stop() {
		p.t.Fatalf("model did not reach the expected state within %s (elapsed %s)", timeout, time.Since(start))
	}
	p.t.Logf("phase reached after %s", time.Since(start))
}

func (p *modelPump) forDuration(duration time.Duration) {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		select {
		case msg := <-p.produced:
			if msg == nil {
				continue
			}
			if batch, ok := msg.(bubbletea.BatchMsg); ok {
				for _, cmd := range batch {
					p.launch(cmd)
				}
				continue
			}
			_, next := p.model.Update(msg)
			p.launch(next)
		case msg := <-p.stream:
			if msg == nil {
				p.stream = nil
				continue
			}
			_, next := p.model.Update(msg)
			p.launch(next)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
