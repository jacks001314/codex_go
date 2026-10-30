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

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"
)

// TestRemoteNewSessionStartFailurePreservesComposedInputLikeRust covers Rust
// #42671: when a replacement thread cannot read the server's new-session
// defaults or cannot be started, the TUI reports Rust's error text and hands
// the composed message back to the composer instead of losing it.
func TestRemoteNewSessionStartFailurePreservesComposedInputLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	cases := []struct {
		name       string
		handle     func(ctx context.Context, conn *websocket.Conn, req remoteTUITestRequest)
		wantPrefix string
	}{
		{
			name: "config read failure",
			handle: func(ctx context.Context, conn *websocket.Conn, req remoteTUITestRequest) {
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32603, "message": "config read failed"},
				})
			},
			wantPrefix: "Failed to read new session defaults: ",
		},
		{
			name: "thread start failure",
			handle: func(ctx context.Context, conn *websocket.Conn, req remoteTUITestRequest) {
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32603, "message": "thread start failed"},
				})
			},
			wantPrefix: "Failed to start a fresh session through the app server: ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
					case string(appserver.MethodConfigRead):
						if tc.wantPrefix == "Failed to read new session defaults: " {
							tc.handle(ctx, conn, req)
							continue
						}
						remoteTUITestWrite(ctx, conn, map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"result":  map[string]any{"config": map[string]any{}, "origins": map[string]any{}},
						})
					case string(appserver.MethodConfigRequirementsRead):
						remoteTUITestWrite(ctx, conn, map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"result":  map[string]any{"requirements": map[string]any{}},
						})
					case string(appserver.MethodThreadStart):
						var params map[string]any
						if err := json.Unmarshal(req.Params, &params); err != nil {
							remoteTUITestSendErr(serverErrs, err)
							return
						}
						tc.handle(ctx, conn, req)
					default:
						remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
						return
					}
				}
			}))
			defer server.Close()

			endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			state := codextui.NewState(nil)
			request := codextea.SubmitRequest{Prompt: "keep this message"}
			messages := make(chan tea.Msg, 16)
			runInteractiveRemoteTurn(ctx, &cli.RootOptions{}, endpoint, state, request, messages, remoteTUIBrokers{}, nil)

			var failure *codextea.NewSessionStartFailedMsg
			for message := range messages {
				if failed, ok := message.(codextea.NewSessionStartFailedMsg); ok {
					copied := failed
					failure = &copied
				}
			}
			if failure == nil {
				t.Fatal("no NewSessionStartFailedMsg reported")
			}
			if !strings.HasPrefix(failure.ErrorMessage, tc.wantPrefix) {
				t.Fatalf("error = %q, want prefix %q", failure.ErrorMessage, tc.wantPrefix)
			}
			if failure.Request.Prompt != request.Prompt {
				t.Fatalf("restored prompt = %q, want %q", failure.Request.Prompt, request.Prompt)
			}
			if got := strings.TrimSpace(state.ThreadID); got != "" {
				t.Fatalf("thread id = %q, want the session to stay unstarted", got)
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
		})
	}
}

// TestRemoteNewSessionDefaultsUnsupportedOnlyForOldServersLikeRust keeps the
// Rust read_effective_config_if_supported matcher narrow: only a missing
// method/variant falls back, while a real failure is reported.
func TestRemoteNewSessionDefaultsUnsupportedOnlyForOldServersLikeRust(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "method not found", err: &remoteRPCError{Code: -32601, Message: "Method not found"}, want: true},
		{name: "invalid request names config read", err: &remoteRPCError{Code: -32600, Message: "unknown method config/read"}, want: true},
		{name: "invalid request for another method", err: &remoteRPCError{Code: -32600, Message: "unknown method thread/start"}, want: false},
		{name: "internal failure", err: &remoteRPCError{Code: -32603, Message: "config read failed"}, want: false},
		{name: "transport failure", err: fmt.Errorf("connection reset"), want: false},
	}
	for _, tc := range cases {
		if got := remoteNewSessionDefaultsUnsupported(tc.err); got != tc.want {
			t.Fatalf("%s: unsupported = %v, want %v", tc.name, got, tc.want)
		}
	}
}
