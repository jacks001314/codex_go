package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/session"
	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	"codex_go/tui/chatwidget"
)

// TestInteractiveRemotePromptEditHandlerForksAndAttaches covers the backtrack
// prompt-edit round trip against the app server: resolve the selected transcript
// ordinal to a turn, fork before it, then attach to the branched thread.
func TestInteractiveRemotePromptEditHandlerForksAndAttaches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErrs := make(chan error, 1)

	turns := func() []any {
		return []any{
			map[string]any{"id": "turn-1", "status": "completed", "items": []any{
				map[string]any{"id": "u1", "type": "userMessage", "text": "first"},
				map[string]any{"id": "a1", "type": "agentMessage", "text": "one"},
			}},
			map[string]any{"id": "turn-2", "status": "completed", "items": []any{
				map[string]any{"id": "u2", "type": "userMessage", "text": "second"},
			}},
		}
	}

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
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				threadTurns := turns()
				if params.ThreadID == "thread-forked" {
					threadTurns = turns()[:1]
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"thread": map[string]any{
						"id":    params.ThreadID,
						"cwd":   "/repo",
						"turns": threadTurns,
					}},
				})
			case string(appserver.MethodThreadFork):
				var params appserver.ThreadForkParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-source" || params.BeforeTurnID != "turn-2" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/fork params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"thread": map[string]any{
						"id":    "thread-forked",
						"cwd":   "/repo",
						"turns": turns()[:1],
					}},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	handler := interactiveRemotePromptEditHandler(ctx, endpoint, nil, nil)
	response, err := handler(tuiapp.PromptEditSelection{
		ThreadID:    "thread-source",
		UserOrdinal: 1,
		Prompt:      chatwidget.ThreadComposerState{Text: "second"},
	})
	if err != nil {
		t.Fatalf("prompt edit error = %v", err)
	}
	if response.Summary == nil || response.Summary.ThreadID != "thread-forked" {
		t.Fatalf("summary = %#v", response.Summary)
	}
	if len(response.Messages) == 0 {
		t.Fatalf("branched thread messages = %#v, want the pre-edit history", response.Messages)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestInteractiveRemotePromptEditHandlerRejectsSteer covers the Rust guard that a
// steered prompt cannot be branched independently.
func TestInteractiveRemotePromptEditHandlerRejectsSteer(t *testing.T) {
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
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"thread": map[string]any{
						"id": "thread-source",
						"turns": []any{map[string]any{
							"id": "turn-1", "status": "completed",
							"items": []any{
								map[string]any{"id": "u1", "type": "userMessage", "text": "initial"},
								map[string]any{"id": "u2", "type": "userMessage", "text": "steer"},
							},
						}},
					}},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	handler := interactiveRemotePromptEditHandler(ctx, endpoint, nil, nil)
	if _, err := handler(tuiapp.PromptEditSelection{ThreadID: "thread-source", UserOrdinal: 1}); err == nil {
		t.Fatal("a steered prompt should not be branchable")
	} else if !strings.Contains(err.Error(), "steer") {
		t.Fatalf("error = %v, want the steer rejection", err)
	}
	select {
	case err := <-serverErrs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("server error: %v", err)
		}
	default:
	}
}

// TestPromptImageExtractionFromPersistedUserMessages covers the resumed-thread
// half of prompt restoration: a replayed user message must expose its local and
// remote images so backtracking can rebuild the composer attachments.
func TestPromptImageExtractionFromPersistedUserMessages(t *testing.T) {
	remoteItem := appserver.ThreadItem{
		ID: "u1", Type: "userMessage", Role: "user", Text: "look",
		Content: []appserver.ThreadItemContent{
			{Type: "local_image", ImageURL: "/tmp/a.png"},
			{Type: "input_image", ImageURL: "https://example.test/b.png"},
			{Type: "input_image"},
			{Type: "input_audio", AudioURL: "data:audio/wav;base64,zzz"},
		},
	}
	message, ok := remoteTUIMessageFromThreadItem(remoteItem, reasoningProjectionChatWidget, false)
	if !ok {
		t.Fatal("remote user message should convert")
	}
	if message.UserPrompt != "look" {
		t.Fatalf("remote UserPrompt = %q", message.UserPrompt)
	}
	if len(message.UserPromptLocalImages) != 1 || message.UserPromptLocalImages[0] != "/tmp/a.png" {
		t.Fatalf("remote local images = %#v", message.UserPromptLocalImages)
	}
	if len(message.UserPromptRemoteImages) != 1 || message.UserPromptRemoteImages[0] != "https://example.test/b.png" {
		t.Fatalf("remote remote images = %#v", message.UserPromptRemoteImages)
	}

	localItem := session.Item{
		ID: "u2", Type: "userMessage", Role: "user", Text: "look",
		Content: []session.ContentPart{
			{Type: "local_image", ImageURL: "/tmp/c.png"},
			{Type: "input_image", ImageURL: "https://example.test/d.png"},
		},
	}
	localMessage, ok := interactiveSessionMessageFromItem(localItem, reasoningProjectionChatWidget, false)
	if !ok {
		t.Fatal("local user message should convert")
	}
	if localMessage.UserPrompt != "look" {
		t.Fatalf("local UserPrompt = %q", localMessage.UserPrompt)
	}
	if len(localMessage.UserPromptLocalImages) != 1 || localMessage.UserPromptLocalImages[0] != "/tmp/c.png" {
		t.Fatalf("local local images = %#v", localMessage.UserPromptLocalImages)
	}
	if len(localMessage.UserPromptRemoteImages) != 1 || localMessage.UserPromptRemoteImages[0] != "https://example.test/d.png" {
		t.Fatalf("local remote images = %#v", localMessage.UserPromptRemoteImages)
	}
}

// TestRemotePromptEditCarriesTaskToolsNamespaceLikeRust covers the branched
// thread's task-tools namespace: with the session's hosted MCP server the fresh
// thread carries the `mcp_servers.codex_tui` override (not the callback
// namespace), its capability marker is persisted, and a fork keeps the same
// override through `ThreadForkParams.config` (Rust
// ThreadToolTransport::configure_mcp).
func TestRemotePromptEditCarriesTaskToolsNamespaceLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientConn, serverConn := net.Pipe()
	var mu sync.Mutex
	startParams := map[string]any{}
	forkParams := map[string]any{}
	go func() {
		defer serverConn.Close()
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		for {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			result := map[string]any{}
			switch request.Method {
			case string(appserver.MethodThreadStart):
				var params map[string]any
				_ = json.Unmarshal(request.Params, &params)
				mu.Lock()
				startParams = params
				mu.Unlock()
				result = map[string]any{"thread": map[string]any{"id": "thread-branched"}}
			case string(appserver.MethodThreadFork):
				var params map[string]any
				_ = json.Unmarshal(request.Params, &params)
				mu.Lock()
				forkParams = params
				mu.Unlock()
				result = map[string]any{"thread": map[string]any{"id": "thread-forked"}}
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}()

	state := codextui.NewState(nil)
	host := &taskToolsMCPHost{}
	defer host.close()
	client := &remoteAppServerTUIClient{
		endpoint:  appserverdaemon.NewUnixSocketEndpoint("/tmp/codex-prompt-edit.sock"),
		root:      &cli.RootOptions{},
		state:     state,
		unixDial:  func(context.Context, string) (net.Conn, error) { return clientConn, nil },
		taskTools: host,
	}
	if err := client.connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	template, err := remoteThreadStartParams(&cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	host.attach(client, template, client.registerDynamicToolThread)
	edit := remotePromptEditClient{client: client, root: &cli.RootOptions{}, state: state}

	started, err := edit.StartFreshThread(ctx, tuiapp.ThreadSessionState{})
	if err != nil {
		t.Fatalf("StartFreshThread: %v", err)
	}
	if started.Thread == nil || started.Thread.ID != "thread-branched" {
		t.Fatalf("started thread = %#v", started.Thread)
	}
	mu.Lock()
	startedParams := startParams
	mu.Unlock()
	if _, ok := startedParams["dynamicTools"]; ok {
		t.Fatalf("fresh thread still carries dynamicTools: %#v", startedParams["dynamicTools"])
	}
	config, _ := startedParams["config"].(map[string]any)
	server, _ := config["mcp_servers."+DynamicToolNamespace].(map[string]any)
	if server == nil || strings.TrimSpace(server["url"].(string)) == "" {
		t.Fatalf("fresh thread config is missing the codex_tui MCP server: %#v", startedParams["config"])
	}
	if !remoteTaskToolThreadAvailable(auth.DefaultCodexHome(), "thread-branched") {
		t.Fatal("fresh thread capability marker was not persisted")
	}

	if _, err := edit.ForkThread(ctx, appserver.ThreadForkParams{ThreadID: "thread-parent"}); err != nil {
		t.Fatalf("ForkThread: %v", err)
	}
	mu.Lock()
	forked := forkParams
	mu.Unlock()
	forkConfig, _ := forked["config"].(map[string]any)
	if forkConfig["mcp_servers."+DynamicToolNamespace] == nil {
		t.Fatalf("fork did not carry the codex_tui MCP override: %#v", forked["config"])
	}
}
