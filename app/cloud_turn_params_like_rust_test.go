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

// Rust #57267 (codex-rs/tui/src/app_server_session.rs:1611-1634): turn/start
// carries the turn identity, inputs, trigger and additional context, but a
// server-owned conversation must not send the client's cwd, permissions, model,
// effort, summary or collaboration mode. #61178 carries a conversation-scoped
// speed choice on the next new turn.
func TestCloudTurnStartOmitsClientSettingsLikeRust(t *testing.T) {
	body := sendTurnStartAgainstFakeServer(t, ThreadParamsModeCloud)
	for _, key := range []string{
		"cwd", "model", "approvalPolicy", "sandboxPolicy", "permissions", "config",
		"effort", "summary", "collaborationMode", "runtimeWorkspaceRoots",
	} {
		if raw, ok := body[key]; ok {
			t.Fatalf("cloud turn/start sent client-owned %q = %s", key, raw)
		}
	}
	for _, key := range []string{"threadId", "input", "turnTrigger", "clientUserMessageId", "additionalContext"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("cloud turn/start omitted shared field %q: %#v", key, body)
		}
	}
	var threadID string
	if err := json.Unmarshal(body["threadId"], &threadID); err != nil || threadID != "thread-cloud" {
		t.Fatalf("cloud threadId = %s (err %v), want thread-cloud", body["threadId"], err)
	}
	var tier string
	if err := json.Unmarshal(body["serviceTier"], &tier); err != nil || tier != "priority" {
		t.Fatalf("cloud serviceTier = %s (err %v), want priority", body["serviceTier"], err)
	}
}

func TestCloudTurnStartClearsServiceTierWithExplicitNullLikeRust(t *testing.T) {
	body := sendTurnStartAgainstFakeServerWithTier(t, ThreadParamsModeCloud, "default")
	raw, ok := body["serviceTier"]
	if !ok {
		t.Fatalf("cloud turn/start omitted the explicit service tier clear: %#v", body)
	}
	if string(raw) != "null" {
		t.Fatalf("cloud serviceTier = %s, want null", raw)
	}
}

// Rust #57267: Embedded and Remote keep the client-owned request shape, so the
// settings omission above is scoped to the server-owned mode.
func TestClientOwnedTurnStartKeepsSettingsLikeRust(t *testing.T) {
	for _, mode := range []ThreadParamsMode{ThreadParamsModeEmbedded, ThreadParamsModeRemote} {
		body := sendTurnStartAgainstFakeServer(t, mode)
		for _, key := range []string{"cwd", "model", "effort", "serviceTier", "collaborationMode"} {
			if _, ok := body[key]; !ok {
				t.Fatalf("%s turn/start omitted %q: %#v", mode, key, body)
			}
		}
		var model string
		if err := json.Unmarshal(body["model"], &model); err != nil || model != "gpt-5.1" {
			t.Fatalf("%s turn/start model = %s (err %v), want the submission override", mode, body["model"], err)
		}
	}
}

// sendTurnStartAgainstFakeServer drives the real client.startTurn path against
// a websocket fake server and returns the turn/start params actually written to
// the wire.
func sendTurnStartAgainstFakeServer(t *testing.T, mode ThreadParamsMode) map[string]json.RawMessage {
	return sendTurnStartAgainstFakeServerWithTier(t, mode, "priority")
}

func sendTurnStartAgainstFakeServerWithTier(t *testing.T, mode ThreadParamsMode, tier string) map[string]json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	paramsCh := make(chan map[string]json.RawMessage, 1)
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
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || ctx.Err() != nil {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodTurnStart):
				var body map[string]json.RawMessage
				if err := json.Unmarshal(req.Params, &body); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				paramsCh <- body
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-cloud"}},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	root := &cli.RootOptions{Shared: cli.SharedOptions{
		CWD:                  `D:\repo`,
		Model:                "gpt-5",
		ApprovalPolicy:       "on-request",
		Sandbox:              "workspace-write",
		ModelReasoningEffort: "high",
	}}
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", CWD: `D:\repo`, ReasoningEffort: "high"})
	state.ServiceTier = tier
	messages := make(chan bubbletea.Msg, 8)
	client := &remoteAppServerTUIClient{
		endpoint:         endpoint,
		root:             root,
		state:            state,
		threadParamsMode: mode,
		messages:         messages,
		dial:             websocket.Dial,
	}
	if err := client.connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	turnID, err := client.startTurn(ctx, root, state, "thread-cloud", codextea.SubmitRequest{
		Prompt:   "hello",
		ClientID: "client-1",
		Model:    "gpt-5.1",
		AdditionalContext: map[string]any{
			"notes": map[string]any{"kind": "application", "value": "context"},
		},
	})
	if err != nil {
		t.Fatalf("startTurn: %v", err)
	}
	if turnID != "turn-cloud" {
		t.Fatalf("turn id = %q, want turn-cloud", turnID)
	}
	select {
	case body := <-paramsCh:
		select {
		case err := <-serverErrs:
			t.Fatalf("server error: %v", err)
		default:
		}
		return body
	case <-time.After(2 * time.Second):
		t.Fatal("turn/start was not sent")
		return nil
	}
}
