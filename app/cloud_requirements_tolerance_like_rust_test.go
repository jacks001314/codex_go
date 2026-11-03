package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	codextea "codex_go/tui/tea"
)

// Rust #57267 (codex-rs/tui/src/app_server_session.rs:824-833): a cloud
// transport may omit configRequirements/read with -32601, and the bootstrap
// keeps going without a requirements catalog.
func TestCloudConfigRequirementsMethodNotFoundToleratedLikeRust(t *testing.T) {
	settings, methods, err := readSettingsAgainstRequirementsServer(t, ThreadParamsModeCloud)
	if err != nil {
		t.Fatalf("cloud settings error = %v, want the -32601 tolerance", err)
	}
	if settings.ServiceTierRequirements != nil || settings.PermissionRequirements != nil {
		t.Fatalf("cloud requirements = %#v / %#v, want no catalog", settings.ServiceTierRequirements, settings.PermissionRequirements)
	}
	if !containsMethod(methods, string(appserver.MethodConfigRequirementsRead)) {
		t.Fatalf("methods = %v, want the requirements request on the wire", methods)
	}
}

// Rust #57267: the -32601 tolerance is scoped to Cloud; Embedded and Remote
// must still report the missing method instead of silently losing requirements.
func TestNonCloudConfigRequirementsMethodNotFoundFailsLikeRust(t *testing.T) {
	for _, mode := range []ThreadParamsMode{ThreadParamsModeEmbedded, ThreadParamsModeRemote} {
		_, methods, err := readSettingsAgainstRequirementsServer(t, mode)
		if err == nil {
			t.Fatalf("%s settings error = nil, want -32601", mode)
		}
		var rpcErr *remoteRPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != appserver.JSONRPCMethodNotFoundErrorCode {
			t.Fatalf("%s settings error = %v, want code %d", mode, err, appserver.JSONRPCMethodNotFoundErrorCode)
		}
		if !containsMethod(methods, string(appserver.MethodConfigRequirementsRead)) {
			t.Fatalf("%s methods = %v, want the requirements request", mode, methods)
		}
	}
}

func containsMethod(methods []string, want string) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}

// readSettingsAgainstRequirementsServer drives interactiveRemoteReadSettings
// against a websocket fake server whose configRequirements/read answers -32601.
func readSettingsAgainstRequirementsServer(t *testing.T, mode ThreadParamsMode) (codextea.SettingsWriteResult, []string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	methods := make([]string, 0, 4)
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
			mu.Lock()
			methods = append(methods, req.Method)
			mu.Unlock()
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodConfigRead):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"config": map[string]any{}}})
			case string(appserver.MethodConfigRequirementsRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": appserver.JSONRPCMethodNotFoundErrorCode, "message": "method not found"},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	client := &remoteAppServerTUIClient{
		endpoint:         endpoint,
		threadParamsMode: mode,
		messages:         make(chan bubbletea.Msg, 4),
		dial:             websocket.Dial,
	}
	if err := client.connect(ctx); err != nil {
		t.Fatalf("%s connect: %v", mode, err)
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("%s initialize: %v", mode, err)
	}
	settings, err := interactiveRemoteReadSettings(ctx, client)
	mu.Lock()
	captured := append([]string(nil), methods...)
	mu.Unlock()
	select {
	case serverErr := <-serverErrs:
		t.Fatalf("%s server error: %v", mode, serverErr)
	default:
	}
	return settings, captured, err
}
