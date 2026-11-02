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
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// Rust app_server_session.rs:1621-1628 (via app/thread_routing.rs:938-941): a
// turn on a thread with a named permission profile sends that profile id as
// `permissions`, and never the legacy `sandboxPolicy`, because the two are
// mutually exclusive. Go used to forward the profile id as a sandbox policy.
func TestClientTurnStartSendsActivePermissionProfileLikeRust(t *testing.T) {
	body := sendTurnStartWithPermissionProfile(t, ThreadParamsModeRemote, "trusted", "trusted")
	if raw, ok := body["permissions"]; !ok {
		t.Fatalf("turn/start omitted permissions: %#v", body)
	} else {
		var profile string
		if err := json.Unmarshal(raw, &profile); err != nil || profile != "trusted" {
			t.Fatalf("permissions = %s (err %v), want \"trusted\"", raw, err)
		}
	}
	if raw, ok := body["sandboxPolicy"]; ok {
		t.Fatalf("turn/start sent sandboxPolicy = %s next to permissions", raw)
	}
}

// Without a named profile the client keeps the legacy sandbox policy, exactly
// like Rust's TurnPermissionsOverride::LegacySandbox branch.
func TestClientTurnStartKeepsSandboxPolicyWithoutProfileLikeRust(t *testing.T) {
	body := sendTurnStartWithPermissionProfile(t, ThreadParamsModeRemote, "", "workspace-write")
	if raw, ok := body["sandboxPolicy"]; !ok {
		t.Fatalf("turn/start omitted sandboxPolicy: %#v", body)
	} else {
		var policy string
		if err := json.Unmarshal(raw, &policy); err != nil || policy != "workspace-write" {
			t.Fatalf("sandboxPolicy = %s (err %v), want workspace-write", raw, err)
		}
	}
	if raw, ok := body["permissions"]; ok {
		t.Fatalf("turn/start sent permissions = %s without a named profile", raw)
	}
}

// A server-owned (cloud) turn never carries either field, even when the client
// knows the profile id.
func TestCloudTurnStartIgnoresActivePermissionProfileLikeRust(t *testing.T) {
	body := sendTurnStartWithPermissionProfile(t, ThreadParamsModeCloud, "trusted", "trusted")
	for _, key := range []string{"permissions", "sandboxPolicy"} {
		if raw, ok := body[key]; ok {
			t.Fatalf("cloud turn/start sent %q = %s", key, raw)
		}
	}
}

// The assertions above are only meaningful if the params type really does
// serialize both fields; this control fails loudly if that ever stops.
func TestTurnStartParamsSerializePermissionAndSandboxLikeRust(t *testing.T) {
	profile := "trusted"
	raw, err := json.Marshal(turn.TurnStartParams{ThreadID: "thread-1", Permissions: &profile})
	if err != nil {
		t.Fatalf("marshal permissions control: %v", err)
	}
	var control map[string]json.RawMessage
	if err := json.Unmarshal(raw, &control); err != nil {
		t.Fatalf("unmarshal permissions control: %v", err)
	}
	if _, ok := control["permissions"]; !ok {
		t.Fatalf("control params lost permissions, the wire assertions are vacuous: %s", raw)
	}

	raw, err = json.Marshal(turn.TurnStartParams{ThreadID: "thread-1", SandboxPolicy: "workspace-write"})
	if err != nil {
		t.Fatalf("marshal sandbox control: %v", err)
	}
	control = nil
	if err := json.Unmarshal(raw, &control); err != nil {
		t.Fatalf("unmarshal sandbox control: %v", err)
	}
	if _, ok := control["sandboxPolicy"]; !ok {
		t.Fatalf("control params lost sandboxPolicy: %s", raw)
	}
}

// sendTurnStartWithPermissionProfile drives the real client.startTurn path and
// returns the turn/start params actually written to the wire.
func sendTurnStartWithPermissionProfile(t *testing.T, mode ThreadParamsMode, profile string, sandbox string) map[string]json.RawMessage {
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
					"result":  map[string]any{"turn": map[string]any{"id": "turn-profile"}},
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
	state.ActivePermissionProfile = profile
	// applyThreadSettingsValues mirrors Rust by also storing the profile id in
	// the legacy sandbox slot; the request builder must prefer `permissions`
	// over that value.
	state.Sandbox = sandbox
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
	if _, err := client.startTurn(ctx, root, state, "thread-profile", codextea.SubmitRequest{
		Prompt:   "hello",
		ClientID: "client-1",
	}); err != nil {
		t.Fatalf("startTurn: %v", err)
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
