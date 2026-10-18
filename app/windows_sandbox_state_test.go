package app

import (
	"context"
	"errors"
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
	"codex_go/sandbox"
)

// TestInteractiveRemoteWindowsSandboxPromptUsesAppServerStateLikeRust covers
// Rust #45821: the startup prompt derives its level and allowed setup choices
// from config/read and configRequirements/read instead of the local files.
func TestInteractiveRemoteWindowsSandboxPromptUsesAppServerStateLikeRust(t *testing.T) {
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
			case "":
				continue
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodWindowsSandboxReadiness):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": sandbox.WindowsReadinessResponse{Status: sandbox.WindowsReadinessNotConfigured},
				})
			case string(appserver.MethodConfigRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{
						"config": map[string]any{"windows": map[string]any{"sandbox": "elevated"}},
					},
				})
			case string(appserver.MethodConfigRequirementsRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{
						"requirements": map[string]any{
							"allowedWindowsSandboxImplementations": []string{"unelevated"},
						},
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
	prompt := interactiveRemoteWindowsSandboxStartupPrompt(ctx, &cli.RootOptions{}, endpoint, nil)
	if prompt == nil {
		t.Fatal("expected a Windows sandbox startup prompt")
	}
	if prompt.AllowElevated {
		t.Fatal("prompt offered elevated setup despite the server requirements")
	}
	if !prompt.AllowUnelevated || !prompt.SetupChoiceRequired {
		t.Fatalf("prompt = %#v", prompt)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

// TestInteractiveRemoteWindowsSandboxStateReadFailure covers the fail-closed
// behavior: a server that cannot report config yields no prompt decision.
func TestInteractiveRemoteWindowsSandboxStateReadFailure(t *testing.T) {
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
			case "":
				continue
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodWindowsSandboxReadiness):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": sandbox.WindowsReadinessResponse{Status: sandbox.WindowsReadinessNotConfigured},
				})
			case string(appserver.MethodConfigRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": -32000, "message": "config unavailable"},
				})
			default:
				remoteTUITestSendErr(serverErrs, errors.New("unexpected method "+req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if prompt := interactiveRemoteWindowsSandboxStartupPrompt(ctx, &cli.RootOptions{}, endpoint, nil); prompt != nil {
		t.Fatalf("prompt = %#v, want nil after a config read failure", prompt)
	}
}
