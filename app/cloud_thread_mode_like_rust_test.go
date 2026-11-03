package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
)

// Rust #57267 (codex-rs/tui/src/app_server_session.rs:360-402): the transport
// selects ThreadParamsMode, Cloud hands settings to the server, and only Cloud
// reports a remote workspace.
func TestCloudThreadParamsModeLikeRust(t *testing.T) {
	cases := []struct {
		mode            ThreadParamsMode
		wantOwner       ThreadSettingsOwner
		wantRemoteSpace bool
		wantName        string
	}{
		{ThreadParamsModeEmbedded, ThreadSettingsOwnerClient, false, "embedded"},
		{ThreadParamsModeRemote, ThreadSettingsOwnerClient, true, "remote"},
		{ThreadParamsModeCloud, ThreadSettingsOwnerServer, true, "cloud"},
	}
	for _, tc := range cases {
		if got := tc.mode.SettingsOwner(); got != tc.wantOwner {
			t.Fatalf("%s settings owner = %v, want %v", tc.mode, got, tc.wantOwner)
		}
		if got := tc.mode.UsesRemoteWorkspace(); got != tc.wantRemoteSpace {
			t.Fatalf("%s uses remote workspace = %v, want %v", tc.mode, got, tc.wantRemoteSpace)
		}
		if got := tc.mode.String(); got != tc.wantName {
			t.Fatalf("mode name = %q, want %q", got, tc.wantName)
		}
	}

	// Rust lib.rs:400-406: AppServerTarget::thread_params_mode reports Remote
	// only for the explicit remote target; everything else is Embedded.
	if got := threadParamsModeForLaunch(&cli.RootOptions{}); got != ThreadParamsModeEmbedded {
		t.Fatalf("launch mode without --remote = %v, want embedded", got)
	}
	if got := threadParamsModeForLaunch(&cli.RootOptions{Remote: "wss://example.com/rpc"}); got != ThreadParamsModeRemote {
		t.Fatalf("launch mode with --remote = %v, want remote", got)
	}
	if got := threadParamsModeForTransport(appServerTransportCloud); got != ThreadParamsModeCloud {
		t.Fatalf("cloud transport mode = %v, want cloud", got)
	}

	// A nil or loopback endpoint stays embedded; a remote websocket is remote.
	if got := threadParamsModeForEndpoint(nil); got != ThreadParamsModeEmbedded {
		t.Fatalf("nil endpoint mode = %v, want embedded", got)
	}
	if got := threadParamsModeForEndpoint(appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:4500/rpc", nil)); got != ThreadParamsModeEmbedded {
		t.Fatalf("loopback endpoint mode = %v, want embedded", got)
	}
	if got := threadParamsModeForEndpoint(appserverdaemon.NewWebSocketEndpoint("wss://example.com/rpc", nil)); got != ThreadParamsModeRemote {
		t.Fatalf("remote endpoint mode = %v, want remote", got)
	}
}

// Rust #57267 (app_server_session.rs:2348-2358, 2001-2006): a server-owned
// conversation sends ThreadStartParams::default() and never overlays the
// client's managed new-thread defaults.
func TestCloudThreadStartParamsServerOwnedLikeRust(t *testing.T) {
	root := &cli.RootOptions{Shared: cli.SharedOptions{
		CWD:            `D:\repo`,
		Model:          "gpt-5",
		Sandbox:        "workspace-write",
		ApprovalPolicy: "on-request",
	}}
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", CWD: `D:\repo`})

	clientOwned, err := remoteThreadStartParamsForMode(ThreadParamsModeRemote, root, state)
	if err != nil {
		t.Fatalf("remote thread/start params: %v", err)
	}
	if clientOwned.Model != "gpt-5" || clientOwned.CWD == "" || clientOwned.ThreadSource == nil {
		t.Fatalf("client-owned params = %#v, want model/cwd/threadSource", clientOwned)
	}

	serverOwned, err := remoteThreadStartParamsForMode(ThreadParamsModeCloud, root, state)
	if err != nil {
		t.Fatalf("cloud thread/start params: %v", err)
	}
	if !reflect.DeepEqual(serverOwned, appserver.ThreadStartParams{}) {
		t.Fatalf("cloud params = %#v, want Rust ThreadStartParams::default()", serverOwned)
	}

	client := &remoteAppServerTUIClient{threadParamsMode: ThreadParamsModeCloud, root: root, state: state}
	managed, err := client.remoteManagedThreadStartParams(context.Background(), root, state)
	if err != nil {
		t.Fatalf("managed cloud thread/start params: %v", err)
	}
	if !reflect.DeepEqual(managed, appserver.ThreadStartParams{}) {
		t.Fatalf("managed cloud params = %#v, want no client default overlay", managed)
	}
}

// Rust #57267 (app_server_session.rs:557-565): a cloud thread skips
// start_dynamic_tool_mcp and sends no dynamic tools or task-tools config on
// thread/start.
func TestCloudThreadStartSkipsClientToolTransportLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	startParams := make(chan appserver.ThreadStartParams, 1)
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
			case string(appserver.MethodThreadStart):
				var params appserver.ThreadStartParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				startParams <- params
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"thread": map[string]any{"id": "thread-cloud"}, "cwd": "/srv/cloud"},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	root := &cli.RootOptions{Shared: cli.SharedOptions{CWD: `D:\repo`, Model: "gpt-5"}}
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", CWD: `D:\repo`})
	messages := make(chan bubbletea.Msg, 8)
	client := &remoteAppServerTUIClient{
		endpoint:         endpoint,
		root:             root,
		state:            state,
		threadParamsMode: ThreadParamsModeCloud,
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
	threadID, err := client.startThread(ctx, root, state)
	if err != nil {
		t.Fatalf("startThread: %v", err)
	}
	if threadID != "thread-cloud" {
		t.Fatalf("thread id = %q, want thread-cloud", threadID)
	}
	select {
	case params := <-startParams:
		if !reflect.DeepEqual(params, appserver.ThreadStartParams{}) {
			t.Fatalf("cloud thread/start params = %#v, want an empty server-owned request", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("thread/start was not sent")
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// Rust #57267 (app_server_session.rs:1195-1197): cloud conversations refuse
// every TUI fork path with the Rust message.
func TestCloudThreadForkRejectedLikeRust(t *testing.T) {
	if err := cloudThreadForkError(ThreadParamsModeCloud); err == nil || err.Error() != cloudThreadForkUnsupportedMessage {
		t.Fatalf("cloud fork error = %v, want %q", err, cloudThreadForkUnsupportedMessage)
	}
	if err := cloudThreadForkError(ThreadParamsModeRemote); err != nil {
		t.Fatalf("remote fork error = %v, want nil", err)
	}

	state := codextui.NewState(nil)
	_, err := remoteSideThreadForkParamsForMode(ThreadParamsModeCloud, &cli.RootOptions{}, state, "thread-parent", nil)
	if err == nil || err.Error() != cloudThreadForkUnsupportedMessage {
		t.Fatalf("side fork params error = %v, want %q", err, cloudThreadForkUnsupportedMessage)
	}
	params, err := remoteSideThreadForkParamsForMode(ThreadParamsModeRemote, &cli.RootOptions{}, state, "thread-parent", nil)
	if err != nil {
		t.Fatalf("remote side fork params: %v", err)
	}
	if params.ThreadID != "thread-parent" {
		t.Fatalf("fork params thread = %q, want thread-parent", params.ThreadID)
	}
}
