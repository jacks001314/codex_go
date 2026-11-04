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

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// A side conversation forked from a paginated thread must retry the ephemeral
// fork with excludeTurns once the server rejects the turn-copying form, then
// wait for the injected boundary prompt.
func TestInteractiveRemoteStartSideRetriesPaginatedFork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 8)
	serverErrs := make(chan error, 1)
	forks := 0
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
			case string(appserver.MethodThreadFork):
				var params appserver.ThreadForkParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				forks++
				if forks == 1 {
					if params.ExcludeTurns {
						remoteTUITestSendErr(serverErrs, fmt.Errorf("first fork must copy turns: %#v", params))
						return
					}
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{
						"code":    -32600,
						"message": "ephemeral paginated thread/fork requires `excludeTurns: true`",
					}})
					continue
				}
				if !params.ExcludeTurns || !params.Ephemeral || params.ThreadID != "thread-parent" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("retry fork params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"thread": map[string]any{"id": "thread-side", "ephemeral": true, "turns": []any{}},
					},
				})
			case string(appserver.MethodThreadInjectItems):
				var params appserver.ThreadInjectItemsParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-side" || len(params.Items) != 1 || !strings.Contains(string(params.Items[0]), "Side conversation boundary.") {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("inject params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	state := codextui.NewState(&codextui.Options{Model: "gpt-side", ApprovalPolicy: "on-request", Sandbox: "workspace-write"})
	response, err := interactiveRemoteStartSide(ctx, &cli.RootOptions{}, endpoint, state, codextea.SideStartParams{
		ParentThreadID: "thread-parent",
	})
	if err != nil {
		t.Fatalf("interactiveRemoteStartSide error = %v", err)
	}
	if response.SideThreadID != "thread-side" || response.ParentThreadID != "thread-parent" {
		t.Fatalf("side response = %#v", response)
	}
	for index, want := range []appserver.Method{
		appserver.MethodInitialize,
		appserver.MethodThreadFork,
		appserver.MethodThreadFork,
		appserver.MethodThreadInjectItems,
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

// The discovery gate keys off the server-reported permission profile, never the
// legacy sandbox mode a remote thread also carries.
func TestRemoteTUIHasActiveNamedProfileIgnoresSandboxMode(t *testing.T) {
	client := &remoteAppServerTUIClient{state: codextui.NewState(&codextui.Options{Sandbox: "workspace-write"})}
	if remoteTUIHasActiveNamedProfile(client) {
		t.Fatal("legacy sandbox mode must not count as an active named profile")
	}
	client.state.ActivePermissionProfile = "trusted"
	if !remoteTUIHasActiveNamedProfile(client) {
		t.Fatal("server-reported named profile must enable discovery")
	}
	client.state.ActivePermissionProfile = ":workspace"
	if remoteTUIHasActiveNamedProfile(client) {
		t.Fatal("built-in profile ids must not enable named discovery")
	}
}
