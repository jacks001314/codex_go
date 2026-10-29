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
	codextea "codex_go/tui/tea"
)

const temporaryStructuredTestRecap = `{"recap":"ok"}`

// temporaryStructuredTestServer starts a fake app server that answers the
// initialize and config/read handshake, delegates thread/start to
// onThreadStart, and completes the structured turn for successful starts.
func temporaryStructuredTestServer(t *testing.T, ctx context.Context, onThreadStart func(remoteTUITestRequest, *websocket.Conn)) (*httptest.Server, *appserverdaemon.RemoteAppServerEndpoint) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
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
				// The managed requirements file sets :workspace as the default,
				// which takes precedence over the legacy sandbox override.
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"config": map[string]any{
							"default_permissions":         ":workspace",
							"allowed_permission_profiles": map[string]any{":read-only": true, ":workspace": true},
						},
					},
				})
			case string(appserver.MethodThreadStart):
				onThreadStart(req, conn)
			case string(appserver.MethodThreadUnsubscribe):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-1", "items": []any{}, "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationItemCompleted),
					"params": map[string]any{
						"threadId": "thread-temp",
						"turnId":   "turn-1",
						"item":     map[string]any{"id": "a1", "type": "agentMessage", "text": temporaryStructuredTestRecap},
					},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnCompleted),
					"params": map[string]any{
						"threadId": "thread-temp",
						"turn":     map[string]any{"id": "turn-1", "items": []any{}, "status": "completed"},
					},
				})
			default:
				return
			}
		}
	}))
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	return server, endpoint
}

// Mirrors Rust #46844: a managed `:workspace` default must not make the
// temporary structured thread writable, so the request carries
// default_permissions ":read-only" alongside the legacy read-only sandbox.
func TestTemporaryStructuredThreadRequestsReadOnlyPermissionsLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var params appserver.ThreadStartParams
	server, endpoint := temporaryStructuredTestServer(t, ctx, func(req remoteTUITestRequest, conn *websocket.Conn) {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Errorf("Unmarshal(thread/start params) error = %v", err)
		}
		remoteTUITestWrite(ctx, conn, map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"thread":  map[string]any{"id": "thread-temp"},
				"sandbox": map[string]any{"type": "readOnly", "networkAccess": false},
			},
		})
	})
	defer server.Close()

	response, err := interactiveRemoteRecapGenerateHandler(ctx, endpoint)("thread-1", codextea.RecapThreadOptions{
		Model: "gpt-test",
		CWD:   "/repo",
	}, "recap prompt", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("recap error = %v", err)
	}
	if response != temporaryStructuredTestRecap {
		t.Fatalf("response = %q, want %q", response, temporaryStructuredTestRecap)
	}
	if params.Sandbox != "read-only" {
		t.Fatalf("sandbox = %#v, want read-only", params.Sandbox)
	}
	if params.Permissions != nil {
		t.Fatalf("permissions = %q, want no custom profile", *params.Permissions)
	}
	if got, ok := params.Config["default_permissions"]; !ok || got != ":read-only" {
		t.Fatalf("default_permissions = %#v, want :read-only so a managed default cannot win", got)
	}
}

// Mirrors Rust #46844: the start is rejected when read-only is not the
// effective permission, instead of silently running the recap turn with more
// access than intended.
func TestTemporaryStructuredThreadRejectsWritableSandboxLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, endpoint := temporaryStructuredTestServer(t, ctx, func(req remoteTUITestRequest, conn *websocket.Conn) {
		remoteTUITestWrite(ctx, conn, map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"thread":  map[string]any{"id": "thread-temp"},
				"sandbox": map[string]any{"type": "workspaceWrite", "writableRoots": []string{"/repo"}},
			},
		})
	})
	defer server.Close()

	_, err := interactiveRemoteRecapGenerateHandler(ctx, endpoint)("thread-1", codextea.RecapThreadOptions{
		Model: "gpt-test",
		CWD:   "/repo",
	}, "recap prompt", map[string]any{"type": "object"})
	if err == nil || !strings.Contains(err.Error(), "temporary structured thread did not start with read-only permissions") {
		t.Fatalf("recap error = %v, want the read-only rejection", err)
	}
}

// Mirrors Rust #46844: an explicit custom profile keeps its own permissions
// path and is not overridden by the built-in read-only default.
func TestTemporaryStructuredThreadPreservesCustomProfileLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var params appserver.ThreadStartParams
	server, endpoint := temporaryStructuredTestServer(t, ctx, func(req remoteTUITestRequest, conn *websocket.Conn) {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Errorf("Unmarshal(thread/start params) error = %v", err)
		}
		remoteTUITestWrite(ctx, conn, map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"thread":                  map[string]any{"id": "thread-temp"},
				"activePermissionProfile": map[string]any{"id": "team-profile"},
			},
		})
	})
	defer server.Close()

	response, err := interactiveRemoteRecapGenerateHandler(ctx, endpoint)("thread-1", codextea.RecapThreadOptions{
		Model:             "gpt-test",
		CWD:               "/repo",
		PermissionProfile: "team-profile",
	}, "recap prompt", map[string]any{"type": "object"})
	if err != nil {
		t.Fatalf("recap error = %v", err)
	}
	if response != temporaryStructuredTestRecap {
		t.Fatalf("response = %q, want %q", response, temporaryStructuredTestRecap)
	}
	if params.Permissions == nil || *params.Permissions != "team-profile" {
		t.Fatalf("permissions = %#v, want team-profile", params.Permissions)
	}
	if params.Sandbox != nil {
		t.Fatalf("sandbox = %#v, want the custom profile to replace the legacy sandbox", params.Sandbox)
	}
	if _, ok := params.Config["default_permissions"]; ok {
		t.Fatalf("default_permissions = %#v, want no override for a custom profile", params.Config["default_permissions"])
	}
}

// Mirrors Rust #46844: a start that silently downgrades a custom profile is an
// error rather than a silently sandboxed turn.
func TestTemporaryStructuredThreadRejectsLostCustomProfileLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, endpoint := temporaryStructuredTestServer(t, ctx, func(req remoteTUITestRequest, conn *websocket.Conn) {
		remoteTUITestWrite(ctx, conn, map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"thread":                  map[string]any{"id": "thread-temp"},
				"activePermissionProfile": map[string]any{"id": ":read-only"},
			},
		})
	})
	defer server.Close()

	_, err := interactiveRemoteRecapGenerateHandler(ctx, endpoint)("thread-1", codextea.RecapThreadOptions{
		Model:             "gpt-test",
		CWD:               "/repo",
		PermissionProfile: "team-profile",
	}, "recap prompt", map[string]any{"type": "object"})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("did not preserve permission profile %s", "team-profile")) {
		t.Fatalf("recap error = %v, want the permission profile rejection", err)
	}
}
